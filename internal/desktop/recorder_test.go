package desktop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/recorder"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// ---- 测试替身 ---------------------------------------------------------------

type fakeStream struct {
	cfg    recorder.OpenConfig
	closed atomic.Bool
}

func (s *fakeStream) Format() recorder.Format {
	return recorder.Format{SampleRate: recorder.TargetSampleRate, Channels: 1}
}
func (s *fakeStream) Close() error { s.closed.Store(true); return nil }

type fakeCapturer struct {
	mu      sync.Mutex
	streams map[recorder.Source]*fakeStream
	openErr error
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{streams: map[recorder.Source]*fakeStream{}}
}

func (c *fakeCapturer) Devices(src recorder.Source) ([]recorder.DeviceInfo, error) {
	if src == recorder.SourceMic {
		return []recorder.DeviceInfo{{ID: "mic-0", Name: "阵列麦克风", IsDefault: true}}, nil
	}
	return []recorder.DeviceInfo{{ID: "sys-0", Name: "扬声器（回环）", IsDefault: true}}, nil
}

func (c *fakeCapturer) Open(cfg recorder.OpenConfig) (recorder.Stream, error) {
	if c.openErr != nil {
		return nil, c.openErr
	}
	s := &fakeStream{cfg: cfg}
	c.mu.Lock()
	c.streams[cfg.Source] = s
	c.mu.Unlock()
	return s, nil
}

func (c *fakeCapturer) stream(t *testing.T, src recorder.Source) *fakeStream {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.streams[src]
	if s == nil {
		t.Fatalf("音源 %s 没有被打开", src)
	}
	return s
}

type fakeLink struct {
	cfg  recorder.UplinkConfig
	sent atomic.Int64
}

func (l *fakeLink) Send(pcm []int16)            { _ = pcm; l.sent.Add(1) }
func (l *fakeLink) Flush()                      {}
func (l *fakeLink) Stop(context.Context) error  { return nil }
func (l *fakeLink) Abort()                      {}
func (l *fakeLink) State() recorder.UplinkState { return recorder.UplinkConnected }
func (l *fakeLink) Stats() recorder.UplinkStats {
	return recorder.UplinkStats{Sent: l.sent.Load()}
}

type fakeUplinks struct {
	// onOpen 在链路刚建好时调用，用来模拟「Start() 返回前就连上了」。
	onOpen func(recorder.UplinkConfig)
	mu     sync.Mutex
	links  map[recorder.Source]*fakeLink
}

func (u *fakeUplinks) open(cfg recorder.UplinkConfig) (recorder.Link, error) {
	l := &fakeLink{cfg: cfg}
	u.mu.Lock()
	u.links[cfg.Track] = l
	if u.onOpen != nil {
		u.onOpen(cfg)
	}
	u.mu.Unlock()
	return l, nil
}

func (u *fakeUplinks) link(t *testing.T, src recorder.Source) *fakeLink {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	l := u.links[src]
	if l == nil {
		t.Fatalf("音轨 %s 没有建链路", src)
	}
	return l
}

// newRecorderApp 造一个只用于录音测试的 App：假采集、假上行、隔离的配置目录。
func newRecorderApp(t *testing.T) (*App, *recordingSink, *fakeCapturer, *fakeUplinks) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APPDATA", home)      // Windows: os.UserConfigDir
	t.Setenv("LOCALAPPDATA", home) // Windows: os.UserCacheDir（录音默认根目录）
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("HOME", home)

	sink := &recordingSink{}
	app := New(sink)
	capt := newFakeCapturer()
	up := &fakeUplinks{links: map[recorder.Source]*fakeLink{}}
	app.SetCapturer(capt)
	app.rec.uplinker = up.open
	return app, sink, capt, up
}

// payloadOf 剥掉信封，取出事件载荷。
func payloadOf[T any](t *testing.T, sink *recordingSink, name string) (T, bool) {
	t.Helper()
	var zero T
	ev, ok := sink.lastOf(name)
	if !ok {
		return zero, false
	}
	env, ok := ev.data.(hostproto.Envelope)
	if !ok {
		t.Fatalf("事件 %s 不是信封：%T", name, ev.data)
	}
	p, ok := env.Payload.(T)
	if !ok {
		t.Fatalf("事件 %s 的载荷类型是 %T，不是 %T", name, env.Payload, zero)
	}
	return p, true
}

func voice(n int) []int16 {
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(2000 + i%97)
	}
	return pcm
}

// ---- 用例 -------------------------------------------------------------------

func TestRecorderDevicesWithoutCapturer(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)
	app.rec.capturer = nil

	got, err := app.RecorderDevices()
	if err != nil {
		t.Fatalf("RecorderDevices: %v", err)
	}
	if got.Supported {
		t.Fatal("没有采集实现时不该报 Supported")
	}
	if got.Reason == "" {
		t.Fatal("不支持时必须给出理由，界面要拿它去解释为什么入口是灰的")
	}
}

func TestRecorderDevicesLists(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	got, err := app.RecorderDevices()
	if err != nil {
		t.Fatalf("RecorderDevices: %v", err)
	}
	if !got.Supported || len(got.Mic) != 1 || len(got.Sys) != 1 {
		t.Fatalf("设备列表不对：%+v", got)
	}
	if !got.Mic[0].IsDefault || got.Mic[0].ID != "mic-0" {
		t.Fatalf("麦克风项不对：%+v", got.Mic[0])
	}
}

// TestStartRecordingUsesDefaultGateway 盯住「一次都没配也能录」。
//
// 这条原来盯的是反面（没配就拒绝开始）。有了内置默认地址之后「没配」不再存在：
// 分发出去的包装完直接按录音，就该连上部署好的那台服务。StartRecording 里那道
// 空地址的闸门留着给将来兜底（默认值被清空、或换成按环境注入），正常路径走不到。
func TestStartRecordingUsesDefaultGateway(t *testing.T) {
	app, _, _, up := newRecorderApp(t)

	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err != nil {
		t.Fatalf("没配网关地址时应当退回内置默认地址开始录音，却失败了：%v", err)
	}
	defer func() { _, _ = app.StopRecording() }()

	link := up.link(t, recorder.SourceMic)
	if link.cfg.URL != defaultGatewayURL {
		t.Fatalf("拨的是 %q，应为内置默认 %q", link.cfg.URL, defaultGatewayURL)
	}
	// 令牌同样得跟上。这台网关握手照样给 101，首帧一到才以 1008 unauthorized
	// 断开，界面上只剩一句「离线录制中」——漏了令牌和漏了地址一样录不出字。
	if link.cfg.Auth != defaultGatewayToken {
		t.Fatalf("带的令牌是 %q，应为内置默认令牌", link.cfg.Auth)
	}
}

func TestRecorderSettingsRoundTrip(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	// 未落盘时的默认值：保留音频。它是补转写唯一的依据。
	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatalf("RecorderSettings: %v", err)
	}
	if !got.KeepAudio {
		t.Fatal("默认必须保留音频")
	}

	want := protocol.RecorderSettings{
		GatewayURL: "ws://127.0.0.1:8000/ws", GatewayToken: "tok-round-trip",
		SpeakerName: "马文涛", Lang: "zh", KeepAudio: false, SummaryModel: "glm-5.1",
	}
	if err := app.SaveRecorderSettings(want); err != nil {
		t.Fatalf("SaveRecorderSettings: %v", err)
	}
	got, err = app.RecorderSettings()
	if err != nil {
		t.Fatalf("RecorderSettings: %v", err)
	}
	if got != want {
		t.Fatalf("读回来的设置不一致：\n got %+v\nwant %+v", got, want)
	}
}

func TestDeleteRecordingRejectsTraversal(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	// 这一处校验错了就是对着别人的目录做 RemoveAll。
	for _, id := range []string{"", ".", "..", "../../Windows", `..\..\Windows`, "a/b"} {
		if err := app.DeleteRecording(id); err == nil {
			t.Fatalf("id %q 应当被拒绝", id)
		}
	}
}

func TestRecordingLifecycle(t *testing.T) {
	app, sink, capt, up := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{
		GatewayURL: "ws://127.0.0.1:8000/ws", KeepAudio: true,
	}); err != nil {
		t.Fatalf("写设置: %v", err)
	}

	info, err := app.StartRecording(protocol.StartRecordingRequest{Title: "季度评审"})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if info.ID == "" || info.Title != "季度评审" || info.State != string(recorder.SessionRecording) {
		t.Fatalf("开始后的状态不对：%+v", info)
	}
	if len(info.Room) == 0 {
		t.Fatal("必须有房间号，两条轨靠它归到同一条时间轴上")
	}

	// 开始时就该有一条状态事件——界面靠它把按钮切成「录制中」。
	if st, ok := payloadOf[protocol.RecorderState](t, sink, protocol.EventRecorderState); !ok {
		t.Fatal("没有 recorder:state 事件")
	} else if st.State != string(recorder.SessionRecording) {
		t.Fatalf("状态事件不对：%+v", st)
	}

	// 灌两帧真声音（麦克风轨）与一帧数字静音（回环轨）。
	mic := capt.stream(t, recorder.SourceMic)
	sys := capt.stream(t, recorder.SourceLoopback)
	mic.cfg.OnFrame(recorder.Frame{Source: recorder.SourceMic, PCM: voice(recorder.FrameSamples), Peak: 0.42})
	mic.cfg.OnFrame(recorder.Frame{Source: recorder.SourceMic, PCM: voice(recorder.FrameSamples), Peak: 0.51})
	sys.cfg.OnFrame(recorder.Frame{Source: recorder.SourceLoopback, PCM: make([]int16, recorder.FrameSamples), Silent: true})

	// 回环轨的数字静音不该上行——省下的 GPU 时间直接换算成能同时开几场会。
	if got := up.link(t, recorder.SourceLoopback).sent.Load(); got != 0 {
		t.Fatalf("回环轨静音帧被上行了 %d 帧", got)
	}
	if got := up.link(t, recorder.SourceMic).sent.Load(); got != 2 {
		t.Fatalf("麦克风轨上行了 %d 帧，应为 2", got)
	}

	// 电平：采集层直报 → 定时器合成一条事件。
	mic.cfg.OnLevel(0.7)
	sys.cfg.OnLevel(0.2)
	waitFor(t, 2*time.Second, func() bool {
		lv, ok := payloadOf[protocol.RecorderLevel](t, sink, protocol.EventRecorderLevel)
		return ok && lv.Mic > 0
	}, "没等到 recorder:level 事件")

	// 网关下行的转写原样透传。
	up.link(t, recorder.SourceMic).cfg.OnEvent(recorder.Event{
		Type: "final", Track: "mic", Seg: 3, Rev: recorder.RevFinal, Text: "这一版先上灰度",
	})
	tr, ok := payloadOf[protocol.RecorderTranscript](t, sink, protocol.EventRecorderTranscript)
	if !ok {
		t.Fatal("没有 recorder:transcript 事件")
	}
	if tr.Text != "这一版先上灰度" || tr.Seg != 3 || tr.Rev != recorder.RevFinal {
		t.Fatalf("转写事件不对：%+v", tr)
	}

	// 暂停 / 恢复：暂停期间的帧整帧丢弃，本地 WAV 与服务端时间轴一起停住。
	if err := app.PauseRecording(); err != nil {
		t.Fatalf("PauseRecording: %v", err)
	}
	mic.cfg.OnFrame(recorder.Frame{Source: recorder.SourceMic, PCM: voice(recorder.FrameSamples)})
	if got := up.link(t, recorder.SourceMic).sent.Load(); got != 2 {
		t.Fatalf("暂停期间还在上行：%d 帧", got)
	}
	if err := app.ResumeRecording(); err != nil {
		t.Fatalf("ResumeRecording: %v", err)
	}

	final, err := app.StopRecording()
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if final.State != string(recorder.SessionStopped) || final.EndedAt == "" {
		t.Fatalf("结束后的状态不对：%+v", final)
	}
	if len(final.Tracks) != 2 {
		t.Fatalf("应有两条轨，得到 %d", len(final.Tracks))
	}
	if !mic.closed.Load() || !sys.closed.Load() {
		t.Fatal("结束后采集流没关掉")
	}
	for _, tk := range final.Tracks {
		if _, err := os.Stat(filepath.Join(final.Dir, tk.WAV)); err != nil {
			t.Fatalf("音轨文件不在：%v", err)
		}
	}

	// 结束后不该还有"正在进行的录音"。
	if err := app.PauseRecording(); err == nil {
		t.Fatal("结束后还能暂停")
	}

	// 列表读得到，删得掉。
	list, err := app.ListRecordings()
	if err != nil {
		t.Fatalf("ListRecordings: %v", err)
	}
	if len(list) != 1 || list[0].ID != final.ID {
		t.Fatalf("列表不对：%+v", list)
	}
	if err := app.DeleteRecording(final.ID); err != nil {
		t.Fatalf("DeleteRecording: %v", err)
	}
	if list, err = app.ListRecordings(); err != nil || len(list) != 0 {
		t.Fatalf("删除后列表应为空：%+v %v", list, err)
	}
}

func TestStartRecordingRejectsSecond(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://x/ws"}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _, _ = app.StopRecording() }()

	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err == nil {
		t.Fatal("同一时刻只允许一场录音")
	}
}

// TestRecordingTitleAutoIncrements 盯住自增序号：取的是已有的最大序号加一，
// 不是"目录数 + 1"——删掉中间一场之后不能又发出一个已经用过的名字。
func TestRecordingTitleAutoIncrements(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://x/ws"}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		info, err := app.StartRecording(protocol.StartRecordingRequest{})
		if err != nil {
			t.Fatalf("第 %d 场 StartRecording: %v", i+1, err)
		}
		want := "新录音 " + string(rune('1'+i))
		if info.Title != want {
			t.Fatalf("第 %d 场标题是 %q，应为 %q", i+1, info.Title, want)
		}
		ids = append(ids, info.ID)
		if _, err := app.StopRecording(); err != nil {
			t.Fatalf("第 %d 场 StopRecording: %v", i+1, err)
		}
	}
	if err := app.DeleteRecording(ids[1]); err != nil {
		t.Fatalf("DeleteRecording: %v", err)
	}
	info, err := app.StartRecording(protocol.StartRecordingRequest{})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _, _ = app.StopRecording() }()
	if info.Title != "新录音 4" {
		t.Fatalf("删掉中间一场后标题是 %q，应为「新录音 4」——按数量算会撞名", info.Title)
	}
}

func TestWorstUplink(t *testing.T) {
	connected := string(recorder.UplinkConnected)
	offline := string(recorder.UplinkOffline)
	connecting := string(recorder.UplinkConnecting)

	// 一条通一条断时必须报断的那条：显示「已连接」而实际有一条轨在丢音，
	// 是最坏的呈现。
	if got := worstUplink(connected, offline); got != offline {
		t.Fatalf("worstUplink(connected, offline) = %q", got)
	}
	if got := worstUplink(offline, connected); got != offline {
		t.Fatalf("worstUplink(offline, connected) = %q", got)
	}
	if got := worstUplink("", connecting); got != connecting {
		t.Fatalf("worstUplink(空, connecting) = %q", got)
	}
}

func waitFor(t *testing.T, limit time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestUplinkChangeEmitsState 盯住链路状态变化会补一条事件。
//
// 会话状态事件只在开始/暂停/结束那几个瞬间才有，而链路自己在退避重连。不补发
// 的话，「正在连接」会一直挂在界面上直到用户按下一个按钮——配错网关地址时，
// 那恰恰是最需要立刻说清楚的一件事。
func TestUplinkChangeEmitsState(t *testing.T) {
	app, sink, _, up := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://x/ws"}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	info, err := app.StartRecording(protocol.StartRecordingRequest{})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _, _ = app.StopRecording() }()

	// 麦克风轨掉线：一条通一条断时要报断的那条。
	up.link(t, recorder.SourceMic).cfg.OnState(recorder.UplinkOffline)
	st, ok := payloadOf[protocol.RecorderState](t, sink, protocol.EventRecorderState)
	if !ok {
		t.Fatal("没有 recorder:state 事件")
	}
	if st.Uplink != string(recorder.UplinkOffline) {
		t.Fatalf("链路状态是 %q，应为 offline", st.Uplink)
	}
	if st.ID != info.ID || st.State != string(recorder.SessionRecording) {
		t.Fatalf("补发的事件丢了会话上下文：%+v", st)
	}

	// 重新连上：灯要变回去。原先的实现对时间取最差值，一旦掉过线就永远是红的。
	up.link(t, recorder.SourceMic).cfg.OnState(recorder.UplinkConnected)
	up.link(t, recorder.SourceLoopback).cfg.OnState(recorder.UplinkConnected)
	st, _ = payloadOf[protocol.RecorderState](t, sink, protocol.EventRecorderState)
	if st.Uplink != string(recorder.UplinkConnected) {
		t.Fatalf("两条轨都连上了，链路状态却是 %q", st.Uplink)
	}
}

// TestUplinkAggregateSettlesOnConnected 盯住两条轨先后连上后指示灯要回到「已连接」。
//
// 实测挂过：读旧值 / 写自己这轨 / 读新值 三步没有原子性，两条轨的 goroutine 一
// 交错就变成「麦克风算出变了并发了 connecting，回环随后算出没变而不发」——于是
// 明明都连上了，界面永远停在「正在连接」。
func TestUplinkAggregateSettlesOnConnected(t *testing.T) {
	app, sink, _, up := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://x/ws"}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _, _ = app.StopRecording() }()

	// 两条轨的回调并发打进来，正是实测里出问题的时序。
	var wg sync.WaitGroup
	for _, src := range []recorder.Source{recorder.SourceMic, recorder.SourceLoopback} {
		wg.Add(1)
		go func(s recorder.Source) {
			defer wg.Done()
			up.link(t, s).cfg.OnState(recorder.UplinkConnecting)
			up.link(t, s).cfg.OnState(recorder.UplinkConnected)
		}(src)
	}
	wg.Wait()

	if got := app.RecorderStatus().Uplink; got != string(recorder.UplinkConnected) {
		t.Fatalf("两条轨都连上了，聚合状态却是 %q", got)
	}
	st, ok := payloadOf[protocol.RecorderState](t, sink, protocol.EventRecorderState)
	if !ok || st.Uplink != string(recorder.UplinkConnected) {
		t.Fatalf("最后一条状态事件仍不是 connected：%+v", st)
	}
}

// TestUplinkInitDoesNotClobberCallbacks 盯住链路状态初值写在 Start() 之前。
//
// 实测挂过：初值写在 Start() 之后，把 openTrack 里回调刚设好的 connected 又抹回
// connecting。此后不再有状态变化，「正在连接」就永远挂在界面上——网关明明是通的。
func TestUplinkInitDoesNotClobberCallbacks(t *testing.T) {
	app, _, _, up := newRecorderApp(t)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://x/ws"}); err != nil {
		t.Fatalf("写设置: %v", err)
	}
	// 建链路时立刻回调 connected，模拟「Start() 返回前就连上了」。
	up.onOpen = func(cfg recorder.UplinkConfig) {
		if cfg.OnState != nil {
			cfg.OnState(recorder.UplinkConnected)
		}
	}

	info, err := app.StartRecording(protocol.StartRecordingRequest{})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _, _ = app.StopRecording() }()

	if info.Uplink != string(recorder.UplinkConnected) {
		t.Fatalf("StartRecording 返回的链路状态是 %q，应为 connected", info.Uplink)
	}
	if got := app.RecorderStatus().Uplink; got != string(recorder.UplinkConnected) {
		t.Fatalf("状态查询里的链路状态是 %q，应为 connected", got)
	}
}

// TestRecorderSettingsFallsBackToDefaultGateway 盯住内置默认的转写地址与令牌。
//
// 分发出去的包要装完就能录：没有配置文件、以及配置文件里这两栏是空的（内置默认
// 值出现之前的老版本写下的），都得退回内置值；反过来，已经填过的显式值不许被默认
// 值盖掉——那是用户指着别的服务在用。地址和令牌少哪个都连不上，所以一起盯。
func TestRecorderSettingsFallsBackToDefaultGateway(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatalf("没有配置文件时读设置: %v", err)
	}
	if got.GatewayURL != defaultGatewayURL || got.GatewayToken != defaultGatewayToken {
		t.Fatalf("没有配置文件时地址/令牌 = %q/%q，应为内置默认", got.GatewayURL, got.GatewayToken)
	}

	if err := app.SaveRecorderSettings(protocol.RecorderSettings{KeepAudio: true}); err != nil {
		t.Fatalf("写空设置: %v", err)
	}
	if got, err = app.RecorderSettings(); err != nil {
		t.Fatalf("读回留空的设置: %v", err)
	}
	if got.GatewayURL != defaultGatewayURL || got.GatewayToken != defaultGatewayToken {
		t.Fatalf("留空时地址/令牌 = %q/%q，应为内置默认", got.GatewayURL, got.GatewayToken)
	}

	const (
		mineURL = "ws://127.0.0.1:9000/ws"
		mineTok = "my-own-token"
	)
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{
		GatewayURL: mineURL, GatewayToken: mineTok, KeepAudio: true,
	}); err != nil {
		t.Fatalf("写自定义地址: %v", err)
	}
	if got, err = app.RecorderSettings(); err != nil {
		t.Fatalf("读回自定义设置: %v", err)
	}
	if got.GatewayURL != mineURL || got.GatewayToken != mineTok {
		t.Fatalf("地址/令牌 = %q/%q，应保持 %q/%q", got.GatewayURL, got.GatewayToken, mineURL, mineTok)
	}
}

// TestRecorderSettingsToleratesBOM 盯住带 BOM 的配置文件读得回来。
//
// 真实踩过：用 PowerShell 5.1 的 `Out-File -Encoding utf8` 改过一次
// recorder.json，之后整个录音功能就废了，界面上只有一句
// 「invalid character 'ï' looking for beginning of value」——对着一个看起来
// 完全正常的文件，没人猜得出是 BOM。记事本「另存为 UTF-8」也一样。
func TestRecorderSettingsToleratesBOM(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	want := protocol.RecorderSettings{
		GatewayURL: "ws://127.0.0.1:8000/ws", GatewayToken: "tok-bom",
		SpeakerName: "马文涛", KeepAudio: true,
	}
	if err := app.SaveRecorderSettings(want); err != nil {
		t.Fatalf("SaveRecorderSettings: %v", err)
	}
	path, err := recorderSettingsPath()
	if err != nil {
		t.Fatalf("配置路径: %v", err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // 测试自己写的临时路径
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, raw...), 0o600); err != nil {
		t.Fatalf("写带 BOM 的版本: %v", err)
	}

	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatalf("带 BOM 时读设置: %v", err)
	}
	if got != want {
		t.Fatalf("带 BOM 时读出来不一致：\n got %+v\nwant %+v", got, want)
	}
}
