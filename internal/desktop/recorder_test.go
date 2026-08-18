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
	mu    sync.Mutex
	links map[recorder.Source]*fakeLink
}

func (u *fakeUplinks) open(cfg recorder.UplinkConfig) (recorder.Link, error) {
	l := &fakeLink{cfg: cfg}
	u.mu.Lock()
	u.links[cfg.Track] = l
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

func TestStartRecordingNeedsGateway(t *testing.T) {
	app, _, _, _ := newRecorderApp(t)

	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err == nil {
		t.Fatal("没配网关地址时必须拒绝开始——否则录一场发现全程离线才是最糟的")
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
		GatewayURL: "ws://127.0.0.1:8000/ws", SpeakerName: "马文涛",
		Lang: "zh", KeepAudio: false, SummaryModel: "glm-5.1",
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

func TestWorseUplink(t *testing.T) {
	connected := string(recorder.UplinkConnected)
	offline := string(recorder.UplinkOffline)
	connecting := string(recorder.UplinkConnecting)

	// 一条通一条断时必须报断的那条：显示「已连接」而实际有一条轨在丢音，
	// 是最坏的呈现。
	if got := worseUplink(connected, offline); got != offline {
		t.Fatalf("worseUplink(connected, offline) = %q", got)
	}
	if got := worseUplink(offline, connected); got != offline {
		t.Fatalf("worseUplink(offline, connected) = %q", got)
	}
	if got := worseUplink("", connecting); got != connecting {
		t.Fatalf("worseUplink(空, connecting) = %q", got)
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
