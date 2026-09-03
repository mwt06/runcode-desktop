package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	// defaultGatewayURL 是转写服务的内置默认地址：分发出去的包装完就能录，
	// 不必先让每个人去设置页抄一串地址。设置里填过的显式值优先，改这里不会
	// 动到已经配好的机器——那种机器要换服务得自己在设置页改，或者清空那一栏。
	//
	// 路径必须带 /ws：这台网关上 / 与 /asr 都直接 403。
	defaultGatewayURL = "ws://202.205.160.8:18016/ws"
	// defaultGatewayToken 是上面那台网关的 FUNASR_AUTH_TOKEN。
	//
	// 它必须跟着地址一起内置：这台网关强制校验令牌，握手照样给 101，首帧一到
	// 就以 close 1008 unauthorized 断开——界面上只剩一句「离线录制中」，没人能
	// 从那句话猜到是缺令牌。「留空退回通行证令牌」的老路在这台服务上同样被拒。
	//
	// 代价说清楚：它跟着二进制分发，strings 一抓就有，等同于「拿到安装包的人
	// 都能用这台转写服务」。这是这台内网服务当前的部署前提。哪天要收紧，改成
	// 每人一枚令牌（设置页那一栏本来就是为此留的）或走通行证换发。
	defaultGatewayToken = "1f6a46e72b3dbe13852683f43da57a96426a24f2628de5c7"
	// recorderSettingsVersion 是录音设置的迁移版本。
	//
	// 每当「内置默认值变了、但已经装过的机器必须跟着变」时 +1，并在
	// migrateRecorderSettings 里补上这一版要做的事。落后的配置读到时迁移一次
	// 并写回，之后用户自己在设置页怎么改都不会再被覆盖。
	recorderSettingsVersion = 3
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
	// lastState 是最近一次发出去的状态事件。链路状态变化要靠它补发——那条路
	// 不能回头去问会话（见 noteUplink）。
	lastState atomic.Value // protocol.RecorderState

	// micLevel / sysLevel 由两条音频线程写、由 ticker 读，存的是
	// math.Float32bits。用原子而不是锁：音频回调里不能等锁。
	micLevel atomic.Uint32
	sysLevel atomic.Uint32
	// uplinkMu 保护下面两个字段。它们只被两条上行 goroutine 与界面查询碰，
	// 与 mu 没有嵌套关系——**尤其不能**换成 mu：StartRecording 持着 mu 调
	// Start()，而 Start() 里建好上行就会立刻回调链路状态。
	uplinkMu sync.Mutex
	// micUplink / sysUplink 是两条轨**各自当前**的链路状态。界面上只有一个指示
	// 灯，取两者里更该让用户知道的那个（见 worstUplink）。
	//
	// 分开存而不是就地取最差：那样是对时间取最大值，一旦掉过线就永远停在
	// offline，重连上了灯也不会变回去。
	micUplink string
	sysUplink string

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
	// 版本戳由后端独占，不采信前端回传的数字：设置页在 recorderSettings() 还没
	// 返回时就保存的话会送来 0，那会让一次性迁移再跑一遍，把用户刚打开的开关又
	// 关掉。界面上本来也没有这个字段的控件。
	s.Version = recorderSettingsVersion
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

	// 转写服务的令牌优先用设置里配的那个；没配就退回通行证令牌（本机匿名模式下
	// 服务端不校验，照样能跑）。取不到也不算致命：本地照录，上行会被服务端拒掉，
	// 界面上表现为「离线录制中」——比直接不让开始要好。
	auth := strings.TrimSpace(set.GatewayToken)
	if auth == "" {
		auth, _ = a.tokens.Token()
	}

	cfg := recorder.SessionConfig{
		Root:        root,
		Title:       strings.TrimSpace(req.Title),
		GatewayURL:  set.GatewayURL,
		Auth:        auth,
		Lang:        firstNonEmpty(req.Lang, set.Lang),
		SpeakerName: firstNonEmpty(set.SpeakerName, a.passportDisplayName()),
		KeepAudio:   set.KeepAudio,
		MicDiarize:  set.MicDiarize,
		MicDeviceID: firstNonEmpty(req.MicDeviceID, set.MicDeviceID),
		SysDeviceID: firstNonEmpty(req.SysDeviceID, set.SysDeviceID),
		Capturer:    a.rec.capturer,
		Uplinker:    a.rec.uplinkerOrDefault(),
		OnEvent:     a.emitTranscript,
		OnState:     a.emitRecorderState,
		OnLevel:     a.rec.noteLevel,
		OnUplink:    a.noteUplink,
		OnError:     a.noteRecorderError,
	}

	sess, err := recorder.NewSession(cfg)
	if err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}
	// 链路状态的初值必须在 Start() **之前**写。
	//
	// 写在后面会把回调刚设好的值又抹回 connecting：openTrack 里上行一建好就开始
	// 拨号，快的话 Start() 返回时两条轨都已经是 connected 了。此后不会再有状态
	// 变化，于是「正在连接」永远挂着——实测就是这么挂住的。
	a.rec.uplinkMu.Lock()
	a.rec.micUplink = string(recorder.UplinkConnecting)
	a.rec.sysUplink = string(recorder.UplinkConnecting)
	a.rec.uplinkMu.Unlock()

	if err := sess.Start(); err != nil {
		return protocol.RecordingInfo{}, wireError(err)
	}

	a.rec.sess = sess
	a.rec.finished = nil
	a.rec.lastErr.Store("")
	a.rec.startLevelPump(a.sink)

	out := toWireRecording(sess.Meta(), sess.Dir())
	out.Uplink = a.rec.uplinkState()
	return out, nil
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

	meta, dir := sess.Meta(), sess.Dir()
	info := toWireRecording(meta, dir)
	a.rec.finish(&info)

	if stopErr != nil {
		return info, wireError(stopErr)
	}
	// 录完在工作区留一份（音频 + 转写 + meta，见 recorder_archive.go）。放在 Stop
	// 成功之后：这时会话目录才是完整的。
	//
	// 异步做：一小时双轨是几百 MB，同步复制会把「停止」这一下卡上几秒，而用户按下
	// 停止之后紧接着就是自动生成纪要，本来就还要等。复制走的是"临时目录 + 改名"，
	// 所以中途被关掉也不会在工作区留下半截目录。
	//
	// 失败不报成"停止失败"——录音本体好端端在应用数据目录里，那才是要紧的东西，
	// 所以只发一条警告说清楚工作区这份没存上。
	go func() {
		dest, err := a.archiveRecording(dir, meta)
		switch {
		case err != nil:
			a.sink.Emit(EventWarning, Warning{Message: "录音没能在工作区存一份（原件仍在录音目录里）：" + err.Error()})
		case dest != "":
			debugLog("recording archived to workspace: %s", dest)
		}
	}()
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
	out := protocol.RecorderState{
		ID:      m.ID,
		State:   string(m.State),
		AudioMS: m.AudioMS,
		Uplink:  a.rec.uplinkState(),
		Error:   msg,
	}
	a.rec.lastState.Store(out)
	a.sink.Emit(protocol.EventRecorderState, out)
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

// noteUplink 记下某条轨的链路状态，聚合结果变了就补一条状态事件。
//
// 必须补发。会话状态事件只在开始/暂停/恢复/结束那几个瞬间才有，而链路是自己
// 在退避重连的——不发的话，「正在连接」会一直挂在界面上直到下一次有人按按钮，
// 用户看不出到底连上了没有。这正是配错网关地址时最需要说清楚的一件事。
func (a *App) noteUplink(src recorder.Source, st recorder.UplinkState) {
	// 「读旧值 → 写自己这轨 → 读新值」必须是一个整体。两条轨各有一个 goroutine
	// 在这里跑，不加锁的话它们会交错成：麦克风算出「变了」并发了 connecting，
	// 回环随后算出「没变」而不发——于是两条轨明明都连上了，指示灯永远停在
	// 「正在连接」。实测就是这么挂住的。
	a.rec.uplinkMu.Lock()
	before := a.rec.uplinkStateLocked()
	if src == recorder.SourceMic {
		a.rec.micUplink = string(st)
	} else {
		a.rec.sysUplink = string(st)
	}
	after := a.rec.uplinkStateLocked()
	a.rec.uplinkMu.Unlock()

	if after == before {
		return
	}
	// 用缓存的快照补发，**不能**回头去问会话。
	//
	// 这条回调是上行链路调的，而链路是在 Session.Start() 里建起来的——Start()
	// 全程持着会话锁。真实上行在自己的 goroutine 上回调，顶多阻塞一下；但只要
	// 有一条同步回调的路径（测试替身就是），sess.Meta() 就会直接死锁在这里。
	// 快照由 emitRecorderState 顺手存下，够用了：时长的实时推进由界面本地计时器
	// 负责，这条事件只是来改链路指示灯的。
	last, _ := a.rec.lastState.Load().(protocol.RecorderState)
	if last.ID == "" {
		return
	}
	last.Uplink = after
	a.sink.Emit(protocol.EventRecorderState, last)
}

func (r *recorderCtl) uplinkState() string {
	r.uplinkMu.Lock()
	defer r.uplinkMu.Unlock()
	return r.uplinkStateLocked()
}

func (r *recorderCtl) uplinkStateLocked() string {
	return worstUplink(r.micUplink, r.sysUplink)
}

// worstUplink 取两条轨里"更该让用户知道"的那个状态。
//
// 界面上只有一个指示灯，而两条轨可能一条通一条断。断了的那条才是要说的事——
// 显示"已连接"而实际有一条轨在丢音，是最坏的呈现。
func worstUplink(a, b string) string {
	rank := map[string]int{
		string(recorder.UplinkConnected):  1,
		string(recorder.UplinkConnecting): 2,
		string(recorder.UplinkStopped):    3,
		string(recorder.UplinkOffline):    4,
	}
	if rank[b] > rank[a] {
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
		Transcript:    m.Transcript,
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

// recorderDefaults 是没有配置文件、或配置文件读不出来时的录音设置。
//
// 默认保留音频：它是补转写唯一的依据，删早了没有第二次机会。
// 默认开启麦克风说话人分离：主要场景是面对面开会，代价见 migrateRecorderSettings。
// 默认转写地址与令牌见 defaultGatewayURL / defaultGatewayToken。
func recorderDefaults() protocol.RecorderSettings {
	return protocol.RecorderSettings{
		KeepAudio:    true,
		MicDiarize:   true,
		GatewayURL:   defaultGatewayURL,
		GatewayToken: defaultGatewayToken,
	}
}

func loadRecorderSettings() (protocol.RecorderSettings, error) {
	out := recorderDefaults()
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
	if err := json.Unmarshal(stripBOM(b), &out); err != nil {
		return recorderDefaults(), fmt.Errorf("解析 %s: %w", recorderSettingsFile, err)
	}
	// 一次性迁移只对**已经存在的配置文件**做。没有文件时走上面的早返回，什么都
	// 不写——否则新装的机器一启动就把当前内置值固化成显式值，下次换服务器时它又
	// 成了要迁移的陈旧值，等于自己给自己制造存量。
	if migrateRecorderSettings(&out) {
		// 写不回去不算致命：本次读到的已经是迁移后的值，功能是对的，只是下次
		// 启动还要再迁一次。比因为写不动配置就让整个录音不可用要好。
		if err := saveRecorderSettings(out); err != nil {
			debugLog("recorder: 迁移后的设置写回失败: %v", err)
		}
	}
	// 配置文件里这两栏为空时补回默认值。空值有两个来源：内置默认值出现之前的
	// 老版本写下的配置，以及用户自己把那一栏清空——两种都当成「用默认」，否则
	// 升上来的机器会一直卡在「还没有配置转写服务地址」或「离线录制中」。
	if strings.TrimSpace(out.GatewayURL) == "" {
		out.GatewayURL = defaultGatewayURL
	}
	if strings.TrimSpace(out.GatewayToken) == "" {
		out.GatewayToken = defaultGatewayToken
	}
	return out, nil
}

// legacyGatewayURLs 是历史上内置过、现在已经作废的转写地址。
//
// 为什么需要这张表：内置默认值只在设置里那一栏**为空**时才生效，而绝大多数机器
// 上那一栏是有值的——上一版的内置默认值早就被写进去了。换服务器时光改默认值，
// 这些机器一台都不会跟着变，症状是「装了新包还是连旧地址」。所以把作废的地址列
// 在这里，迁移时原样升级成当前默认值。用户自己手填的地址不在表里，不会被动到。
//
// v3 起新写下的配置不会再出现这种陈旧值（落盘前会归一化掉，见
// normalizeRecorderSettings），这张表是给「跨过多个版本才升上来」的存量机器兜底的：
// 它们文件里那个值等于某个**历史**默认值，归一化只认当前默认值，认不出来。
//
// 换服务器的完整动作因此只剩两步：改 defaultGatewayURL、把旧值追加到这张表
// （版本号只在需要重写存量文件时才动）。
var legacyGatewayURLs = []string{
	"ws://202.205.160.8:8000/ws",
}

// legacyGatewayTokens 同理，令牌轮换时把作废的那串追加进来。
var legacyGatewayTokens []string

// migrateRecorderSettings 把落后版本的配置升级到当前版本，返回是否动过。
//
// 迁移每台机器只跑一次（跑完写回版本戳），所以这里可以做「一次性的决定」——
// 比如替用户关掉某个开关；用户之后自己再打开，不会被反复覆盖。
func migrateRecorderSettings(s *protocol.RecorderSettings) bool {
	if s.Version >= recorderSettingsVersion {
		return false
	}
	if s.Version < 1 {
		if slices.Contains(legacyGatewayURLs, strings.TrimSpace(s.GatewayURL)) {
			s.GatewayURL = defaultGatewayURL
		}
		if slices.Contains(legacyGatewayTokens, strings.TrimSpace(s.GatewayToken)) {
			s.GatewayToken = defaultGatewayToken
		}
		// v1 曾在这里关掉麦克风说话人分离，v2 又把它打开了（见下）。这一行留着
		// 只为让版本链读起来完整——从 0 迁上来的机器会连着跑 v1、v2，净结果是开。
		s.MicDiarize = false
	}
	if s.Version < 2 {
		// 默认开启麦克风说话人分离：主要场景是几个人围着一台电脑面对面开会，
		// 不开的话整场会被标成你一个人。
		//
		// 代价是明确的、实测过的：服务端一旦对某条轨做盲聚类，就不再回显姓名，
		// 只给 S1/S2 这类编号——你自己说的话在字幕和纪要里都不会显示姓名
		// （对比见 recorder_test 里那两条注释）。纯线上会议用不上它，可以在
		// 设置页关掉；关掉之后麦克风轨会直接带上你的姓名。
		s.MicDiarize = true
	}
	// v3 没有自己的动作块：它要的效果全在 saveRecorderSettings 的归一化里
	// （见 normalizeRecorderSettings）。版本号 +1 的作用就是让存量配置被重写一遍，
	// 把等于内置默认值的网关地址/令牌清掉。
	s.Version = recorderSettingsVersion
	return true
}

// normalizeRecorderSettings 在落盘前把「与内置默认值相同的部署字段」清空。
//
// 只对部署事实（网关地址与令牌）这么做，绝不碰用户偏好——两类字段在「内置默认值
// 变了」时该有相反的行为：
//
//   - 部署事实：换服务器时所有机器都该跟着走。文件里不留显式值，内置默认就能生效，
//     不必再靠 legacyGatewayURLs 一条条追。这两天连着踩的三次「改了默认不生效」
//     全是因为文件里躺着一份和当时默认值一模一样的显式值。
//   - 用户偏好（micDiarize / keepAudio / speakerName / 设备选择）：发版翻转默认值
//     时，用户当初特意做的选择不该被静默改掉，所以必须留显式值。micDiarize 刚从
//     「默认关」翻成「默认开」，若省略存储，特意关掉它的人会连带被改回开。
//
// 界面上看不出区别：读设置时空值会被补回当前默认值（见 loadRecorderSettings），
// 设置页照样显示当前用的是哪台服务。
func normalizeRecorderSettings(s *protocol.RecorderSettings) {
	if strings.TrimSpace(s.GatewayURL) == defaultGatewayURL {
		s.GatewayURL = ""
	}
	if strings.TrimSpace(s.GatewayToken) == defaultGatewayToken {
		s.GatewayToken = ""
	}
}

func saveRecorderSettings(s protocol.RecorderSettings) error {
	// 入参是值拷贝，归一化只影响落盘的那一份——调用方手里的设置不受影响，
	// 界面照旧显示当前用的是哪台服务。
	normalizeRecorderSettings(&s)
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

// ReadRecordingTranscript 读回某场录音的最终稿（Markdown）。没有文本时返回空串。
//
// 界面拿它做两件事：生成纪要时当输入，以及在预览栏里当「原始记录」那一栏。
func (a *App) ReadRecordingTranscript(id string) (string, error) {
	if err := validRecordingID(id); err != nil {
		return "", wireError(err)
	}
	root, err := a.recorderRoot()
	if err != nil {
		return "", wireError(err)
	}
	m, err := recorder.ReadMeta(filepath.Join(root, id))
	if err != nil {
		return "", wireError(err)
	}
	if m.Transcript == "" {
		return "", nil
	}
	// 文件名取自我们自己写的 meta，不是外部输入；id 已经过 validRecordingID。
	b, err := os.ReadFile(filepath.Join(root, id, filepath.Base(m.Transcript))) //nolint:gosec // 路径由校验过的 id 与自写的 meta 拼出
	if err != nil {
		return "", wireError(err)
	}
	return string(b), nil
}

// utf8BOM 是 UTF-8 字节序标记。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// stripBOM 去掉开头的 UTF-8 BOM。
//
// encoding/json 不认它，会报 `invalid character 'ï' looking for beginning of
// value`——这条信息对着一个看起来完全正常的配置文件，没人猜得出是怎么回事。
//
// 而这个文件在 Windows 上非常容易被写成带 BOM 的：记事本「另存为 UTF-8」、
// PowerShell 5.1 的 `Out-File -Encoding utf8`，都会加上。它是用户可以手工编辑的
// 配置，容得下这一点。
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, utf8BOM)
}
