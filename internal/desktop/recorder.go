package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/recorder"
)

// 录音纪要的命令面。
//
// 采集实现（WASAPI/malgo，要 cgo，只有 Windows 有）住在外壳里，由 SetCapturer
// 注进来——和 Dialoger 同一个套路，为的是同一件事：internal/desktop 不依赖
// Wails，也不依赖任何平台专有的东西，否则它就没法在别的宿主里跑、也没法测。
//
// 会话本身（双轨采集 → 落盘 → 上行）由 internal/recorder 管，这里只做三件事：
// 生命周期（一次只允许一场）、DTO 转换、把回调接到事件总线上。

const (
	// recorderSettingsFile 是录音设置的落盘文件名，与 disabled.json 同级。
	recorderSettingsFile = "recorder.json"
	// levelEmitInterval 是电平事件的节奏。采集层已经把回调节流到约 20 Hz，
	// 这里再定时聚合一次是为了把两条轨合成一条事件——而且发事件这件事绝不能
	// 在音频线程上做。
	levelEmitInterval = recorder.LevelInterval
	// stopTimeout 是「结束录音」等服务端 flush 最后一块的上限。
	stopTimeout = 15 * time.Second
)

// recorderCtl 是录音纪要的进程内状态。零值可用。
type recorderCtl struct {
	// mu 串行化整个生命周期。它不与 App.mu / App.startMu 有任何嵌套关系——
	// 录音与对话是两条互不相干的线，故意不共用锁。
	mu       sync.Mutex
	capturer recorder.Capturer
	sess     *recorder.Session
	// finished 是最近一场已结束录音的快照。StopRecording 返回它，界面据此
	// 决定要不要提示「有缺口，建议补转写」。
	finished *protocol.RecordingInfo
	// lastErr 记下把录音打断的错误，随下一条 recorder:state 发出去。
	//
	// 用原子而不是 mu：emitRecorderState 是在 recorder 的会话锁下被调的，
	// 而 StartRecording 又持着 mu 调 Start()——它碰任何一把锁都是死锁。
	lastErr atomic.Value // string

	// micLevel / sysLevel 由两条音频线程写、由 ticker 读，存的是
	// math.Float32bits。用原子而不是锁：音频回调里不能等锁。
	micLevel atomic.Uint32
	sysLevel atomic.Uint32
	// uplink 是两条轨里"最差"的那条的状态（见 worseUplink）。
	uplink atomic.Value // string

	// uplinker 可注入。零值走真的 WebSocket——测试要靠它把网关换成假的，
	// 否则这一层就只能靠一个真网关才能测。
	uplinker recorder.Uplinker

	levelStop chan struct{}
	levelWG   sync.WaitGroup
}

// SetCapturer 安装系统音频采集实现（由外壳调用）。
func (a *App) SetCapturer(c recorder.Capturer) { a.rec.capturer = c }

// RecorderDevices 列出可用的录音设备。
//
// 不支持时不返回 error 而是 Supported=false + 理由：界面要把入口置灰并说明
// 原因，而不是等用户点下去才弹一个错。
func (a *App) RecorderDevices() (protocol.RecorderDeviceList, error) {
	capt := a.rec.capturer
	if capt == nil {
		return protocol.RecorderDeviceList{
			Supported: false,
			Reason:    "当前系统不支持录音采集（录音纪要目前只支持 Windows）",
		}, nil
	}
	mic, err := capt.Devices(recorder.SourceMic)
	if err != nil {
		return protocol.RecorderDeviceList{Supported: false, Reason: err.Error()}, nil
	}
	sys, err := capt.Devices(recorder.SourceLoopback)
	if err != nil {
		return protocol.RecorderDeviceList{Supported: false, Reason: err.Error()}, nil
	}
	return protocol.RecorderDeviceList{
		Supported: true,
		Mic:       toWireDevices(mic),
		Sys:       toWireDevices(sys),
	}, nil
}

func toWireDevices(in []recorder.DeviceInfo) []protocol.RecorderDevice {
	out := make([]protocol.RecorderDevice, 0, len(in))
	for _, d := range in {
		out = append(out, protocol.RecorderDevice{ID: d.ID, Name: d.Name, IsDefault: d.IsDefault})
	}
	return out
}

// RecorderSettings 读回录音设置。文件不存在时返回带默认值的空设置。
func (a *App) RecorderSettings() (protocol.RecorderSettings, error) {
	s, err := loadRecorderSettings()
	if err != nil {
		return protocol.RecorderSettings{}, wireError(err)
	}
	return s, nil
}

// SaveRecorderSettings 写回录音设置。
func (a *App) SaveRecorderSettings(s protocol.RecorderSettings) error {
	if err := saveRecorderSettings(s); err != nil {
		return wireError(err)
	}
	return nil
}

// RecorderStatus 报告当前录音状态。没有正在进行的录音时 ID 为空。
func (a *App) RecorderStatus() protocol.RecordingInfo {
	a.rec.mu.Lock()
	sess := a.rec.sess
	fin := a.rec.finished
	a.rec.mu.Unlock()

	if sess == nil {
		if fin != nil {
			return *fin
		}
		return protocol.RecordingInfo{State: string(recorder.SessionIdle)}
	}
	info := toWireRecording(sess.Meta(), sess.Dir())
	info.Uplink = a.rec.uplinkState()
	return info
}

// ListRecordings 列出历史录音，最近的在前。
//
// 顺带做崩溃恢复：进程被强杀过的会话在这里被标成 Interrupted，被截断的 WAV 也
// 在这里修好——列表是用户第一眼会看到的地方，修复放在这里最自然。
// RecoverSessions 只回报它救过的那几场，所以完整清单要另取一次。
func (a *App) ListRecordings() ([]protocol.RecordingInfo, error) {
	root, err := a.recorderRoot()
	if err != nil {
		return nil, wireError(err)
	}
	if _, err := recorder.RecoverSessions(root); err != nil {
		return nil, wireError(err)
	}
	metas, err := recorder.ListSessions(root)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]protocol.RecordingInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, toWireRecording(m, filepath.Join(root, m.ID)))
	}
	return out, nil
}

// DeleteRecording 删掉一场录音（连同音频）。未知 id 是幂等 no-op。
func (a *App) DeleteRecording(id string) error {
	if err := validRecordingID(id); err != nil {
		return wireError(err)
	}
	a.rec.mu.Lock()
	live := a.rec.sess != nil && a.rec.sess.Meta().ID == id
	a.rec.mu.Unlock()
	if live {
		return wireError(errors.New("不能删除正在进行的录音，请先结束"))
	}
	root, err := a.recorderRoot()
	if err != nil {
		return wireError(err)
	}
	if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
		return wireError(err)
	}
	a.rec.mu.Lock()
	if a.rec.finished != nil && a.rec.finished.ID == id {
		a.rec.finished = nil
	}
	a.rec.mu.Unlock()
	return nil
}

// validRecordingID 挡住会跳出录音根目录的 id。
//
// 它是外部输入（界面把列表里的 id 原样传回来，但命令面对任何调用者开放），
// 而下面要对它做 RemoveAll —— 这一处校验错了就是删掉别的目录。
func validRecordingID(id string) error {
	if id == "" {
		return errors.New("录音 id 不能为空")
	}
	if id != filepath.Base(id) || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("非法的录音 id %q", id)
	}
	return nil
}

// StartRecording 开一场录音。同一时刻只允许一场。
func (a *App) StartRecording(req protocol.StartRecordingRequest) (protocol.RecordingInfo, error) {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()

	if a.rec.sess != nil {
		return protocol.RecordingInfo{}, wireError(errors.New("已经有一场录音在进行中"))
	}
	if a.rec.capturer == nil {
		return protocol.RecordingInfo{}, wireError(errors.New("当前系统不支持录音采集（录音纪要目前只支持 Windows）"))
	}

	set, err := loadRecorderSettings()
	if err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}
	if strings.TrimSpace(set.GatewayURL) == "" {
		return protocol.RecordingInfo{}, wireError(errors.New("还没有配置转写服务地址，请先在设置里填好"))
	}
	root, err := a.recorderRoot()
	if err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}

	// 网关的鉴权用的就是通行证令牌。取不到不算致命：本地照录，转写那一路
	// 会被服务端拒掉，界面上表现为「离线录制中」——比直接不让开始要好。
	auth, _ := a.tokens.Token()

	cfg := recorder.SessionConfig{
		Root:        root,
		Title:       strings.TrimSpace(req.Title),
		GatewayURL:  set.GatewayURL,
		Auth:        auth,
		Lang:        firstNonEmpty(req.Lang, set.Lang),
		SpeakerName: firstNonEmpty(set.SpeakerName, a.passportDisplayName()),
		KeepAudio:   set.KeepAudio,
		MicDeviceID: firstNonEmpty(req.MicDeviceID, set.MicDeviceID),
		SysDeviceID: firstNonEmpty(req.SysDeviceID, set.SysDeviceID),
		Capturer:    a.rec.capturer,
		Uplinker:    a.rec.uplinkerOrDefault(),
		OnEvent:     a.emitTranscript,
		OnState:     a.emitRecorderState,
		OnLevel:     a.rec.noteLevel,
		OnUplink:    a.rec.noteUplink,
		OnError:     a.noteRecorderError,
	}

	sess, err := recorder.NewSession(cfg)
	if err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}
	if err := sess.Start(); err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}

	a.rec.sess = sess
	a.rec.finished = nil
	a.rec.lastErr.Store("")
	a.rec.uplink.Store(string(recorder.UplinkConnecting))
	a.rec.startLevelPump(a.sink)

	return toWireRecording(sess.Meta(), sess.Dir()), nil
}

// PauseRecording 暂停。本地 WAV 与服务端时间轴一起停住，恢复后接着走。
func (a *App) PauseRecording() error { return a.withSession((*recorder.Session).Pause) }

// ResumeRecording 恢复。
func (a *App) ResumeRecording() error { return a.withSession((*recorder.Session).Resume) }

func (a *App) withSession(fn func(*recorder.Session) error) error {
	a.rec.mu.Lock()
	sess := a.rec.sess
	a.rec.mu.Unlock()
	if sess == nil {
		return wireError(errors.New("当前没有正在进行的录音"))
	}
	if err := fn(sess); err != nil {
		return wireError(err)
	}
	return nil
}

// StopRecording 结束录音，返回这一场的最终记录。
//
// 会等服务端 flush 最后一块（上限 stopTimeout）——实测「说完话 → 出确认文本」
// 约 1.5 秒，等这一下换来的是最后一句话不丢。
func (a *App) StopRecording() (protocol.RecordingInfo, error) {
	a.rec.mu.Lock()
	sess := a.rec.sess
	a.rec.mu.Unlock()
	if sess == nil {
		return protocol.RecordingInfo{}, wireError(errors.New("当前没有正在进行的录音"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	stopErr := sess.Stop(ctx)

	info := toWireRecording(sess.Meta(), sess.Dir())
	a.rec.finish(&info)

	if stopErr != nil {
		return info, wireError(stopErr)
	}
	return info, nil
}

// DiscardRecording 放弃这一场：停掉并删掉整个目录。
func (a *App) DiscardRecording() error {
	a.rec.mu.Lock()
	sess := a.rec.sess
	a.rec.mu.Unlock()
	if sess == nil {
		return wireError(errors.New("当前没有正在进行的录音"))
	}
	err := sess.Discard()
	a.rec.finish(nil)
	if err != nil {
		return wireError(err)
	}
	return nil
}

// closeRecorder 在退出时收尾。外壳的 OnShutdown 调它。
//
// 强杀进程时它不会被调用——那正是 SessionMeta.Interrupted 与 RepairWAV 存在的
// 理由，下次开列表时补上。
func (a *App) closeRecorder() {
	a.rec.mu.Lock()
	sess := a.rec.sess
	a.rec.mu.Unlock()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = sess.Stop(ctx)
	a.rec.finish(nil)
}

// finish 把会话摘掉并停掉电平泵。info 非空时记为「最近一场」。
//
// 停泵放在锁里：Stop 与 Discard 可能撞在一起（用户点了结束，紧接着又点放弃），
// 两条路都会来关同一个 channel。泵本身不碰 mu，所以在锁里 Wait 不会自锁。
func (r *recorderCtl) finish(info *protocol.RecordingInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLevelPump()
	r.sess = nil
	r.finished = info
}

// ---- 回调 → 事件总线 --------------------------------------------------------

func (a *App) emitTranscript(ev recorder.Event) {
	a.sink.Emit(protocol.EventRecorderTranscript, protocol.RecorderTranscript{
		Type: ev.Type, Track: ev.Track, Seg: ev.Seg, Rev: ev.Rev, Text: ev.Text,
		BT: ev.BT, ET: ev.ET,
		Spk: ev.Spk, Name: ev.Name, UserID: ev.UserID, Conf: ev.Conf,
		Segs:    ev.Segs,
		Silence: ev.Silence, Need: ev.Need,
		Stage: ev.Stage, QueuePos: ev.QueuePos,
	})
}

// emitRecorderState 把状态变化发给界面。
//
// 它跑在 recorder 的会话锁下（见 SessionConfig.OnState），所以整条路径**不许
// 碰任何一把锁**——快照是参数带进来的，错误信息走原子读。
func (a *App) emitRecorderState(m recorder.SessionMeta) {
	msg, _ := a.rec.lastErr.Load().(string)
	a.sink.Emit(protocol.EventRecorderState, protocol.RecorderState{
		ID:      m.ID,
		State:   string(m.State),
		AudioMS: m.AudioMS,
		Uplink:  a.rec.uplinkState(),
		Error:   msg,
	})
}

// noteRecorderError 记下把录音打断的错误。会话会自行停止，随后的状态事件带上它。
//
// 它可能跑在音频线程上（写盘跟不上是在 onFrame 里发现的），所以同样不碰锁。
func (a *App) noteRecorderError(err error) {
	a.rec.lastErr.Store(err.Error())
	a.sink.Emit(EventWarning, Warning{Message: "录音中断：" + err.Error()})
}

func (r *recorderCtl) noteLevel(src recorder.Source, peak float32) {
	bits := math.Float32bits(peak)
	if src == recorder.SourceMic {
		r.micLevel.Store(bits)
		return
	}
	r.sysLevel.Store(bits)
}

func (r *recorderCtl) noteUplink(_ recorder.Source, st recorder.UplinkState) {
	cur, _ := r.uplink.Load().(string)
	r.uplink.Store(worseUplink(cur, string(st)))
}

func (r *recorderCtl) uplinkState() string {
	s, _ := r.uplink.Load().(string)
	return s
}

// worseUplink 取两条轨里"更该让用户知道"的那个状态。
//
// 界面上只有一个指示灯，而两条轨可能一条通一条断。断了的那条才是要说的事——
// 显示"已连接"而实际有一条轨在丢音，是最坏的呈现。
func worseUplink(a, b string) string {
	rank := map[string]int{
		string(recorder.UplinkConnected):  0,
		string(recorder.UplinkConnecting): 1,
		string(recorder.UplinkStopped):    2,
		string(recorder.UplinkOffline):    3,
	}
	if rank[b] > rank[a] {
		return b
	}
	if a == "" {
		return b
	}
	return a
}

// startLevelPump 起一条定时器把两条轨的电平合成一条事件发出去。
//
// 不在音频回调里直接发：那条路是实时的，往事件总线上写（要过 envelopeSink 的
// 锁、再进 Wails）足以造成丢音。
func (r *recorderCtl) startLevelPump(sink EventSink) {
	r.levelStop = make(chan struct{})
	stop := r.levelStop
	r.levelWG.Add(1)
	go func() {
		defer r.levelWG.Done()
		t := time.NewTicker(levelEmitInterval)
		defer t.Stop()
		var lastMic, lastSys float32
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				mic := math.Float32frombits(r.micLevel.Load())
				sys := math.Float32frombits(r.sysLevel.Load())
				// 两边都还是 0 就不发。整场会议里大部分时间是安静的，
				// 每 50 ms 发一条"还是 0"没有任何信息量。
				if mic == 0 && sys == 0 && lastMic == 0 && lastSys == 0 {
					continue
				}
				lastMic, lastSys = mic, sys
				sink.Emit(protocol.EventRecorderLevel, protocol.RecorderLevel{Mic: mic, Sys: sys})
				// 清零：下一个窗口没有新数据就自然回落到 0，界面上的条会
				// 掉下来，而不是停在最后一次的高度上。
				r.micLevel.Store(0)
				r.sysLevel.Store(0)
			}
		}
	}()
}

func (r *recorderCtl) stopLevelPump() {
	if r.levelStop == nil {
		return
	}
	close(r.levelStop)
	r.levelWG.Wait()
	r.levelStop = nil
	r.micLevel.Store(0)
	r.sysLevel.Store(0)
}

// ---- DTO 转换 ---------------------------------------------------------------

func toWireRecording(m recorder.SessionMeta, dir string) protocol.RecordingInfo {
	out := protocol.RecordingInfo{
		ID: m.ID, Title: m.Title, Room: m.Room,
		State: string(m.State), Lang: m.Lang,
		StartedAt:     m.StartedAt.Format(time.RFC3339),
		AudioMS:       m.AudioMS,
		Interrupted:   m.Interrupted,
		NeedsBackfill: m.NeedsBackfill(),
		Dir:           dir,
	}
	if m.EndedAt != nil {
		out.EndedAt = m.EndedAt.Format(time.RFC3339)
	}
	for _, t := range m.Tracks {
		out.Tracks = append(out.Tracks, protocol.RecorderTrack{
			Source: string(t.Source), WAV: t.WAV,
			SampleRate: t.Format.SampleRate, Channels: t.Format.Channels,
			DurationMS: t.DurationMS, GapMS: t.GapMS,
			SentFrames: t.SentFrames, Dropped: t.Dropped, Reconnects: t.Reconnects,
		})
	}
	return out
}

// ---- 设置与路径 -------------------------------------------------------------

func recorderSettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", recorderSettingsFile), nil
}

func loadRecorderSettings() (protocol.RecorderSettings, error) {
	// 默认保留音频：它是补转写唯一的依据，删早了没有第二次机会。
	out := protocol.RecorderSettings{KeepAudio: true}
	path, err := recorderSettingsPath()
	if err != nil {
		return out, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // 路径由 os.UserConfigDir 拼出，非外部输入
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return protocol.RecorderSettings{KeepAudio: true}, fmt.Errorf("解析 %s: %w", recorderSettingsFile, err)
	}
	return out, nil
}

func saveRecorderSettings(s protocol.RecorderSettings) error {
	path, err := recorderSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// recorderRoot 是录音的落盘根目录。设置里指定了就用设置的。
func (a *App) recorderRoot() (string, error) {
	set, err := loadRecorderSettings()
	if err != nil {
		return "", err
	}
	if r := strings.TrimSpace(set.Root); r != "" {
		return r, nil
	}
	return defaultRecorderRoot()
}

// defaultRecorderRoot 按平台选一个放得下大文件的地方。
//
// Windows 上**不能**用 os.UserConfigDir()：那是 Roaming，域环境下会把录音跟着
// 用户配置来回同步——一小时双轨就是 230 MB。UserCacheDir 在 Windows 上是
// LocalAppData，正是这类"应用自管的大块数据"该待的地方。
//
// macOS 反过来：UserCacheDir 是 ~/Library/Caches，系统可能自行清理，而录音在
// 转写完成前是删不得的，所以那边用 UserConfigDir（~/Library/Application Support）。
func defaultRecorderRoot() (string, error) {
	var base string
	var err error
	if runtime.GOOS == "windows" {
		base, err = os.UserCacheDir()
	} else {
		base, err = os.UserConfigDir()
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runcode", "recordings"), nil
}

func (a *App) passportDisplayName() string {
	a.mu.Lock()
	u := a.passportUser
	a.mu.Unlock()
	if u == nil {
		return ""
	}
	return firstNonEmpty(u.Name, u.Nickname, u.UserName)
}

// Shutdown 在应用退出时收尾（外壳的 OnShutdown 调它）。
//
// 它只负责录音：对话会话由 CloseSession 收。分开是因为两者的失败后果不同——
// 对话没收干净下次还能续上，录音没收干净就是一个被截断的 WAV 加一份状态停在
// "recording" 的 meta。
func (a *App) Shutdown() { a.closeRecorder() }

func (r *recorderCtl) uplinkerOrDefault() recorder.Uplinker {
	if r.uplinker != nil {
		return r.uplinker
	}
	return recorder.DefaultUplinker
}
