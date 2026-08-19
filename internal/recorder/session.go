package recorder

// 录音会话状态机：一场录音 = 一个 room + 两条 track（mic / sys）。
//
// 它把采集、落盘、上行三件事串起来，并负责一条硬承诺：
// **用户的录音永远不会因为网络或服务端的问题丢。**
//
// 帧在音频线程上到达，走两条独立的路：
//
//	Frame ─┬─→ 写盘队列 ─→ WAVSink        必达。挤不动就停录并报错，绝不静默丢
//	       └─→ Uplink                     尽力。断线/拥塞就丢，丢多少记进 GapMS
//
// 两条路都不在音频回调里做阻塞的事——回调被拖住会直接导致丢音，而丢的是本地
// 录音，比丢上行严重得多。
//
// 暂停的语义是「这段不录」：帧在会话层就被丢掉，本地 WAV 和服务端时间轴都不
// 前进，两边自动保持一致（服务端的时间戳是按收到的音频样本推的，不是墙上时钟）。
// 代价是墙上时长 ≠ 音频时长，meta 里两个都记。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// SessionState 是录音会话的状态。
type SessionState string

// 会话状态。Stopping 是「已按结束、正在等服务端 flush 最后一块」那几秒，
// 界面上要有反馈，不能看着像卡住了。
const (
	SessionIdle      SessionState = "idle"
	SessionRecording SessionState = "recording"
	SessionPaused    SessionState = "paused"
	SessionStopping  SessionState = "stopping"
	SessionStopped   SessionState = "stopped"
)

const (
	metaFileName = "meta.json"
	// transcriptFileName 是最终稿。Markdown 而不是 JSON：它既是生成纪要的输入，
	// 也是设计稿里「原始记录」那一栏直接给人看的东西。
	transcriptFileName = "transcript.md"
	// diskQueueDepth 是写盘队列深度，约 38 秒音频。磁盘写 32 KB/s 正常永远追得上；
	// 真挤满了说明磁盘出了问题（写满、被安全软件锁住），那时要停录报错而不是丢帧。
	diskQueueDepth = 64
	// stopGrace 是等服务端 flush 最后一块的上限。实测「说完话→出确认文本」约
	// 1.5 秒，给到 10 秒足够，超了就强断——不能让「结束录音」按钮无限期挂着。
	stopGrace = 10 * time.Second
)

// Link 是上行链路的接口面，*Uplink 实现它。
// 抽出来只为一件事：Session 的测试不该需要一个真的网关。
type Link interface {
	Send(pcm []int16)
	Flush()
	Stop(ctx context.Context) error
	Abort()
	Stats() UplinkStats
	State() UplinkState
}

// Uplinker 按配置建一条上行链路。
type Uplinker func(UplinkConfig) (Link, error)

// DefaultUplinker 走真的 WebSocket。
func DefaultUplinker(cfg UplinkConfig) (Link, error) { return NewUplink(cfg) }

// TrackMeta 是一条音轨落盘后的记录。
type TrackMeta struct {
	Source     Source `json:"source"`
	WAV        string `json:"wav"` // 相对会话目录
	Format     Format `json:"format"`
	DurationMS int64  `json:"duration_ms"`

	// GapMS 是没能上行的音频总时长。>0 意味着服务端那份转写有缺口，
	// 会后要拿本地 WAV 走一次补转写。
	GapMS      int64 `json:"gap_ms"`
	SentFrames int64 `json:"sent_frames"`
	Dropped    int64 `json:"dropped_frames"`
	Reconnects int64 `json:"reconnects"`
}

// SessionMeta 是会话的元数据，随 WAV 一起落在会话目录里。
//
// 它是崩溃恢复的另一半（一半是 RepairWAV）：进程被强杀后，靠它才知道这场录音
// 是哪个房间、什么时候开始、录到哪儿、有没有缺口。
type SessionMeta struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Room      string       `json:"room"`
	State     SessionState `json:"state"`
	Lang      string       `json:"lang,omitempty"`
	KeepAudio bool         `json:"keep_audio"`

	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// AudioMS 是实际录到的音频时长（不含暂停），与墙上时长可能不同。
	AudioMS int64 `json:"audio_ms"`

	// Interrupted 表示这场录音不是正常结束的（进程被强杀/断电），
	// 由 RecoverSessions 打上。界面据此提示「有一场未完成的录音」。
	Interrupted bool `json:"interrupted,omitempty"`

	// Transcript 是最终稿的文件名（相对会话目录）。为空表示这场没有文本。
	Transcript string `json:"transcript,omitempty"`

	Tracks []TrackMeta `json:"tracks"`
}

// NeedsBackfill 报告服务端那份转写是否有缺口，需要拿本地音频补跑。
func (m SessionMeta) NeedsBackfill() bool {
	for _, t := range m.Tracks {
		if t.GapMS > 0 {
			return true
		}
	}
	return m.Interrupted
}

// SessionConfig 描述一场录音。
type SessionConfig struct {
	// Root 是录音根目录，会话落在 Root/<ID>/ 下。
	Root string
	// ID 为空则按开始时间生成。
	ID string
	// Title 为空则取 NextTitle（「新录音 N」，自增序号）。
	Title string

	GatewayURL  string
	Auth        string
	Lang        string
	SpeakerName string
	KeepAudio   bool

	MicDeviceID string
	SysDeviceID string

	Capturer Capturer
	Uplinker Uplinker

	// OnEvent 透传网关下行的转写事件给界面。
	OnEvent func(Event)
	// OnState 报告会话状态变化，带上此刻的完整快照。
	//
	// 之所以把快照一起给出去而不是让回调自己回来调 Meta()：它是**持着会话锁**
	// 调用的，回调里再调任何一个要锁的方法都会死锁。带上快照就没有回调的理由了。
	// 同理，回调里不要做阻塞的事。
	OnState func(SessionMeta)
	// OnUplink 报告某条轨的上行链路状态，驱动界面上的「离线录制中」。
	OnUplink func(Source, UplinkState)
	// OnLevel 报告电平，驱动界面上那根跳动的条。
	OnLevel func(Source, float32)
	// OnError 报告致命错误（磁盘写不动）。会话会自行停止。
	OnError func(error)

	// Now 可注入，测试用。
	Now func() time.Time
}

// Session 是一场正在进行或已结束的录音。
type Session struct {
	cfg SessionConfig
	dir string
	now func() time.Time
	// tx 把下行事件折成最终稿。它是这场录音唯一的持久文本——界面那份只活在
	// WebView 内存里，刷新一下就没了，更不可能拿去生成纪要。
	tx *Transcript

	mu     sync.Mutex
	state  SessionState
	meta   SessionMeta
	tracks []*trackRun

	paused atomic.Bool
	fatal  atomic.Pointer[error]

	stopOnce sync.Once
}

// trackRun 是一条正在跑的音轨。
type trackRun struct {
	src    Source
	stream Stream
	sink   *WAVSink
	link   Link
	format Format

	disk     chan []int16
	wg       sync.WaitGroup
	diskErr  atomic.Pointer[error]
	closeOne sync.Once

	// writtenMS 是已落盘的音频时长。单独用一个原子量记，而不是让别人去读
	// WAVSink.Duration()——WAVSink 的契约是单 goroutine 独占，由写盘 goroutine
	// 持有。界面要按秒刷录音计时，Meta() 会被频繁调用，直接读那边必然竞争
	// （-race 抓到过，位置就在 Resume → writeMeta → snapshot）。
	writtenMS atomic.Int64
}

// NewSession 建会话目录并写下初始 meta，但**不开始录音**——调用 Start 才开始。
//
// 分成两步是因为建目录/写 meta 可能失败（磁盘满、权限），那时不该已经占着麦克风。
func NewSession(cfg SessionConfig) (*Session, error) {
	if cfg.Root == "" {
		return nil, errors.New("录音根目录为空")
	}
	if cfg.Capturer == nil {
		return nil, errors.New("未注入 Capturer")
	}
	if cfg.Uplinker == nil {
		cfg.Uplinker = DefaultUplinker
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	started := now()
	id := cfg.ID
	dir := ""
	if id == "" {
		var err error
		id, dir, err = claimSessionDir(cfg.Root, "rec_"+started.Format("20060102_150405"))
		if err != nil {
			return nil, err
		}
	} else {
		dir = filepath.Join(cfg.Root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("建录音目录: %w", err)
		}
	}
	title := cfg.Title
	if title == "" {
		title = NextTitle(cfg.Root)
	}

	s := &Session{
		cfg:   cfg,
		dir:   dir,
		now:   now,
		state: SessionIdle,
		tx:    NewTranscript(),
		meta: SessionMeta{
			ID: id, Title: title, Room: id, State: SessionIdle,
			Lang: cfg.Lang, KeepAudio: cfg.KeepAudio, StartedAt: started,
		},
	}
	if err := s.writeMeta(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir 返回会话目录。
func (s *Session) Dir() string { return s.dir }

// Meta 返回元数据快照（含实时统计）。
func (s *Session) Meta() SessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// State 返回当前状态。
func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Start 打开两条音轨开始录音。
//
// 任何一条打不开都算失败，并把已经打开的那条收回去——只录到自己声音的
// 「会议纪要」没有意义，不如明确报错让用户去处理（改设备、关掉占用麦克风的程序）。
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != SessionIdle {
		return fmt.Errorf("会话已是 %s 状态，不能重复开始", s.state)
	}

	specs := []struct {
		src      Source
		deviceID string
		diarize  bool
	}{
		// 麦克风天然单人，不跑说话人聚类——单卡 A10 下这不是小事。
		{SourceMic, s.cfg.MicDeviceID, false},
		{SourceLoopback, s.cfg.SysDeviceID, true},
	}

	for _, sp := range specs {
		tr, err := s.openTrack(sp.src, sp.deviceID, sp.diarize)
		if err != nil {
			// 回滚路径的清理错误显式丢弃：此时已有更根本的错误要上报（某条轨
			// 打不开），清理本身再失败也无从处置。
			for _, done := range s.tracks {
				_ = done.close(context.Background(), false)
			}
			s.tracks = nil
			return err
		}
		s.tracks = append(s.tracks, tr)
	}

	s.setStateLocked(SessionRecording)
	return s.writeMetaLocked()
}

func (s *Session) openTrack(src Source, deviceID string, diarize bool) (*trackRun, error) {
	wavName := "track_" + string(src) + ".wav"
	sink, err := NewWAVSink(filepath.Join(s.dir, wavName), TargetSampleRate, 1)
	if err != nil {
		return nil, err
	}

	tr := &trackRun{src: src, sink: sink, disk: make(chan []int16, diskQueueDepth)}
	tr.wg.Add(1)
	go func() {
		defer tr.wg.Done()
		for pcm := range tr.disk {
			if err := tr.sink.Write(pcm); err != nil {
				tr.diskErr.Store(&err)
				s.fail(fmt.Errorf("[%s] 写录音文件失败: %w", src, err))
				return
			}
			tr.writtenMS.Store(tr.sink.Duration().Milliseconds())
		}
	}()

	link, err := s.cfg.Uplinker(UplinkConfig{
		URL: s.cfg.GatewayURL, Room: s.meta.Room, Track: src,
		Name: s.cfg.SpeakerName, Auth: s.cfg.Auth,
		Diarize: diarize, Lang: s.cfg.Lang,
		OnEvent: func(ev Event) {
			s.tx.Apply(ev)
			if s.cfg.OnEvent != nil {
				s.cfg.OnEvent(ev)
			}
		},
		OnState: func(st UplinkState) {
			if s.cfg.OnUplink != nil {
				s.cfg.OnUplink(src, st)
			}
		},
	})
	if err != nil {
		tr.shutdownDisk()
		return nil, fmt.Errorf("[%s] 建上行链路: %w", src, err)
	}
	tr.link = link

	stream, err := s.cfg.Capturer.Open(OpenConfig{
		Source: src, DeviceID: deviceID,
		OnFrame: func(f Frame) { s.onFrame(tr, f) },
		OnLevel: func(peak float32) {
			if s.cfg.OnLevel != nil {
				s.cfg.OnLevel(src, peak)
			}
		},
		OnError: func(err error) {
			if s.cfg.OnError != nil {
				s.cfg.OnError(fmt.Errorf("[%s] 采集: %w", src, err))
			}
		},
	})
	if err != nil {
		link.Abort()
		tr.shutdownDisk()
		return nil, err
	}
	tr.stream = stream
	tr.format = stream.Format()
	return tr, nil
}

// onFrame 跑在音频线程上，必须快速返回。
func (s *Session) onFrame(tr *trackRun, f Frame) {
	// 暂停时整帧丢弃：本地 WAV 与服务端时间轴同步不前进。
	if s.paused.Load() {
		return
	}

	// Frame.PCM 由采集层复用，回调返回即失效——两条路各自拿一份拷贝。
	buf := make([]int16, len(f.PCM))
	copy(buf, f.PCM)

	// 落盘这条路必达。挤不动是磁盘出了问题，停录报错，不静默丢——
	// 静默丢意味着用户以为录着、实际有洞，那是最坏的失败方式。
	select {
	case tr.disk <- buf:
	default:
		s.fail(fmt.Errorf("[%s] 写盘跟不上，磁盘可能已满", tr.src))
		return
	}

	// 上行这条路尽力而为。回环轨的数字静音直接不发——省下的 GPU 时间在单卡
	// A10 上直接换算成能同时开几场会。麦克风轨一律发，交给服务端 VAD
	// （原因见 IsSilent 的注释：麦克风永远达不到数字静音）。
	if f.Silent && f.Source == SourceLoopback {
		return
	}
	tr.link.Send(buf)
}

// fail 记下致命错误并停掉会话。多次调用只有第一次生效。
func (s *Session) fail(err error) {
	if !s.fatal.CompareAndSwap(nil, &err) {
		return
	}
	if s.cfg.OnError != nil {
		s.cfg.OnError(err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
		defer cancel()
		_ = s.Stop(ctx)
	}()
}

// Pause 暂停录音。已录部分保持完好。
func (s *Session) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != SessionRecording {
		return fmt.Errorf("只有录制中才能暂停，当前 %s", s.state)
	}
	s.paused.Store(true)
	s.setStateLocked(SessionPaused)
	return s.writeMetaLocked()
}

// Resume 继续录音。
func (s *Session) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != SessionPaused {
		return fmt.Errorf("只有暂停中才能继续，当前 %s", s.state)
	}
	s.paused.Store(false)
	s.setStateLocked(SessionRecording)
	return s.writeMetaLocked()
}

// Flush 请求服务端立即出结果，录音继续。
func (s *Session) Flush() {
	s.mu.Lock()
	tracks := append([]*trackRun(nil), s.tracks...)
	s.mu.Unlock()
	for _, tr := range tracks {
		tr.link.Flush()
	}
}

// Stop 正常结束：停采集、把尾巴刷出去、等服务端 flush 最后一块、收尾落盘。
//
// 幂等。ctx 到期就强断，不能让「结束录音」按钮无限期挂着。
func (s *Session) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() { err = s.doStop(ctx, true) })
	return err
}

// Discard 放弃本次录音：不给服务端发 stop，并删掉整个会话目录。
//
// 不发 stop 会让服务端把本轨当意外断开处理（保留会话 120 秒、不 flush），
// 正合我们的意——放弃的录音不该在服务端留下半句话。
func (s *Session) Discard() error {
	var err error
	s.stopOnce.Do(func() { err = s.doStop(context.Background(), false) })
	if rmErr := os.RemoveAll(s.dir); err == nil {
		err = rmErr
	}
	return err
}

func (s *Session) doStop(ctx context.Context, clean bool) error {
	s.mu.Lock()
	if s.state == SessionStopped {
		s.mu.Unlock()
		return nil
	}
	s.paused.Store(false)
	s.setStateLocked(SessionStopping)
	tracks := append([]*trackRun(nil), s.tracks...)
	s.mu.Unlock()

	var firstErr error
	for _, tr := range tracks {
		if err := tr.close(ctx, clean); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.now()
	s.meta.EndedAt = &end

	// 先落转写再写 meta：meta 里那个文件名是给界面找文件用的，指向一个还没写出来
	// 的文件是最难查的一类错。写失败不影响录音本身——音频还在，最多是这一场没有
	// 文本，所以只记进 firstErr 而不回滚。
	if name, err := s.writeTranscriptLocked(); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else {
		s.meta.Transcript = name
	}

	s.setStateLocked(SessionStopped)
	if err := s.writeMetaLocked(); err != nil && firstErr == nil {
		firstErr = err
	}
	if fatal := s.fatal.Load(); fatal != nil && firstErr == nil {
		firstErr = *fatal
	}
	return firstErr
}

// close 收一条音轨：先停采集（会 Drain 出最后不足一帧的尾巴），再等写盘排空，
// 最后收上行。顺序不能换——先关写盘队列会把尾帧丢掉。
func (tr *trackRun) close(ctx context.Context, clean bool) error {
	var err error
	tr.closeOne.Do(func() {
		if tr.stream != nil {
			_ = tr.stream.Close()
		}
		tr.shutdownDisk()

		if tr.link != nil {
			// 已经离线的链路没有什么可等的：那段音频服务端根本没收到，等它 flush
			// 只会让「结束」按钮白挂十几秒，末了再抛一句「等待服务端收尾超时」——
			// 而本地录音其实完好无损。网关地址填错、或转写服务没起，走的都是这条
			// 路，不能让它看起来像出了大事。
			if clean && tr.link.State() != UplinkOffline {
				err = tr.link.Stop(ctx)
			} else {
				tr.link.Abort()
			}
		}
	})
	return err
}

func (tr *trackRun) shutdownDisk() {
	if tr.disk != nil {
		close(tr.disk)
		tr.wg.Wait()
		tr.disk = nil
	}
	if tr.sink != nil {
		_ = tr.sink.Close()
	}
}

func (s *Session) setStateLocked(st SessionState) {
	if s.state == st {
		return
	}
	s.state = st
	s.meta.State = st
	if s.cfg.OnState != nil {
		s.cfg.OnState(s.snapshotLocked())
	}
}

// snapshotLocked 把各轨的实时统计并进 meta。
func (s *Session) snapshotLocked() SessionMeta {
	m := s.meta
	m.Tracks = nil
	var audioMS int64
	for _, tr := range s.tracks {
		st := tr.link.Stats()
		d := tr.writtenMS.Load()
		audioMS = max(audioMS, d)
		m.Tracks = append(m.Tracks, TrackMeta{
			Source: tr.src, WAV: "track_" + string(tr.src) + ".wav",
			Format: tr.format, DurationMS: d,
			GapMS: st.GapMS, SentFrames: st.Sent,
			Dropped: st.Dropped, Reconnects: st.Reconnects,
		})
	}
	m.AudioMS = audioMS
	return m
}

func (s *Session) writeMeta() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeMetaLocked()
}

func (s *Session) writeMetaLocked() error {
	return WriteMeta(s.dir, s.snapshotLocked())
}

// WriteMeta 原子写会话元数据。
//
// 比 internal/desktop 那个 writeFileAtomic 多一步 fsync：这个文件的存在意义就是
// 扛住进程被强杀，rename 之后内容还在页缓存里的话，断电就白写了。
func WriteMeta(dir string, m SessionMeta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("编码会话元数据: %w", err)
	}
	tmp, err := os.CreateTemp(dir, metaFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("建临时元数据文件: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("写会话元数据: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("fsync 会话元数据: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("关闭临时元数据文件: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, metaFileName)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("替换会话元数据: %w", err)
	}
	return nil
}

// ReadMeta 读会话元数据。
func ReadMeta(dir string) (SessionMeta, error) {
	var m SessionMeta
	data, err := os.ReadFile(filepath.Join(dir, metaFileName))
	if err != nil {
		return m, fmt.Errorf("读会话元数据: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("解析会话元数据: %w", err)
	}
	return m, nil
}

// titleSeq 从「新录音 12」里抠出 12。
var titleSeq = regexp.MustCompile(`^新录音\s*(\d+)$`)

// NextTitle 返回下一个「新录音 N」。
//
// 序号按 Root 下已有会话的最大值加一，不是按数量——删掉中间某场之后不应该
// 出现两场同名的录音。
func NextTitle(root string) string {
	maxSeq := 0
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			m, err := ReadMeta(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			if g := titleSeq.FindStringSubmatch(m.Title); g != nil {
				if n, err := strconv.Atoi(g[1]); err == nil {
					maxSeq = max(maxSeq, n)
				}
			}
		}
	}
	return "新录音 " + strconv.Itoa(maxSeq+1)
}

// ListSessions 列出 Root 下所有会话，按开始时间从新到旧。
func ListSessions(root string) ([]SessionMeta, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读录音根目录: %w", err)
	}
	var out []SessionMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ReadMeta(filepath.Join(root, e.Name()))
		if err != nil {
			continue // 目录里没有 meta 就不是会话，跳过而不是报错
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// RecoverSessions 扫出上次没有正常结束的录音，修复它们的 WAV 并订正元数据。
//
// 这是方案 §08 那条必做项：Wails v3 单进程下录音窗崩了等于整个 app 崩，没有
// 第二个进程兜底，所以「录音状态可从磁盘恢复」从锦上添花变成了必须。
//
// 返回已恢复的会话，界面据此提示「有一场未完成的录音」。
func RecoverSessions(root string) ([]SessionMeta, error) {
	all, err := ListSessions(root)
	if err != nil {
		return nil, err
	}
	var out []SessionMeta
	for _, m := range all {
		if m.State == SessionStopped {
			continue
		}
		dir := filepath.Join(root, m.ID)
		for i := range m.Tracks {
			t := &m.Tracks[i]
			frames, err := RepairWAV(filepath.Join(dir, t.WAV))
			if err != nil {
				continue // 这一轨救不回来，别拖累另一轨
			}
			t.DurationMS = frames * 1000 / TargetSampleRate
			m.AudioMS = max(m.AudioMS, t.DurationMS)
		}
		m.State = SessionStopped
		m.Interrupted = true
		if err := WriteMeta(dir, m); err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// claimSessionDir 用 os.Mkdir 独占地认领一个会话目录，必要时给 id 加后缀。
//
// 非要独占不可：id 只精确到秒，而「停一场、马上再开一场」是再自然不过的操作。
// 撞上同一秒时若沿用同一个目录，上一场的 WAV 会被新的 WAVSink 从头覆写——录了
// 一小时的会就这么没了；而且 Room 取自 id，服务端那边还会把两场会并进同一条
// 时间轴。os.Mkdir 在目录已存在时报错，正好是需要的原子认领。
func claimSessionDir(root, base string) (id, dir string, err error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", fmt.Errorf("建录音根目录: %w", err)
	}
	for n := 1; n <= 1000; n++ {
		cand := base
		if n > 1 {
			cand = fmt.Sprintf("%s_%d", base, n)
		}
		d := filepath.Join(root, cand)
		err := os.Mkdir(d, 0o755)
		if err == nil {
			return cand, d, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("建录音目录: %w", err)
		}
	}
	return "", "", fmt.Errorf("同一秒内已有太多录音目录（%s）", base)
}

// writeTranscriptLocked 把最终稿写进会话目录，返回文件名。
//
// 一段文字都没有时不建文件、返回空名——「有个空文件」比「没有文件」更难解释，
// 而界面靠 meta.Transcript 是否为空来决定要不要给出「原始记录」入口。
func (s *Session) writeTranscriptLocked() (string, error) {
	if len(s.tx.Segments()) == 0 {
		return "", nil
	}
	path := filepath.Join(s.dir, transcriptFileName)
	if err := os.WriteFile(path, []byte(s.tx.Markdown(s.meta.Title)), 0o600); err != nil {
		return "", fmt.Errorf("写转写文件: %w", err)
	}
	return transcriptFileName, nil
}

// Transcript 返回这场录音的最终稿（实时的，录音进行中也能取）。
func (s *Session) Transcript() *Transcript { return s.tx }
