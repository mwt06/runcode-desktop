package recorder

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ── 替身 ────────────────────────────────────────────────────────────────

type fakeStream struct {
	format Format
	mu     sync.Mutex
	closed bool
	onFr   func(Frame)
	onLvl  func(float32)
}

func (s *fakeStream) Format() Format { return s.format }
func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// push 模拟一次音频线程回调。关闭之后不再回调——真实实现里 Uninit 会先停掉回调。
func (s *fakeStream) push(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.onFr(f)
}

type fakeCapturer struct {
	mu      sync.Mutex
	streams map[Source]*fakeStream
	failOn  Source
	opened  []Source
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{streams: map[Source]*fakeStream{}}
}

func (c *fakeCapturer) Devices(Source) ([]DeviceInfo, error) {
	return []DeviceInfo{{ID: "dev", Name: "假设备", IsDefault: true}}, nil
}

func (c *fakeCapturer) Open(cfg OpenConfig) (Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failOn == cfg.Source {
		return nil, fmt.Errorf("假装打不开 %s", cfg.Source)
	}
	st := &fakeStream{format: Format{SampleRate: 48000, Channels: 2}, onFr: cfg.OnFrame, onLvl: cfg.OnLevel}
	c.streams[cfg.Source] = st
	c.opened = append(c.opened, cfg.Source)
	return st, nil
}

func (c *fakeCapturer) stream(src Source) *fakeStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[src]
}

func (c *fakeCapturer) openedSources() []Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Source(nil), c.opened...)
}

type fakeLink struct {
	cfg     UplinkConfig
	mu      sync.Mutex
	sent    [][]int16
	flushes int
	stopped bool
	aborted bool
	// state 默认 connected；测「离线时结束不干等」要能把它拨到 offline。
	state UplinkState
	// stopDelay 让 Stop 真的慢下来，好证明离线那条路根本没走 Stop。
	stopDelay time.Duration
	stats     UplinkStats
}

func (l *fakeLink) Send(pcm []int16) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, append([]int16(nil), pcm...))
}
func (l *fakeLink) Flush() { l.mu.Lock(); l.flushes++; l.mu.Unlock() }
func (l *fakeLink) Stop(ctx context.Context) error {
	if l.stopDelay > 0 {
		select {
		case <-time.After(l.stopDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	return nil
}
func (l *fakeLink) Abort()             { l.mu.Lock(); l.aborted = true; l.mu.Unlock() }
func (l *fakeLink) Stats() UplinkStats { l.mu.Lock(); defer l.mu.Unlock(); return l.stats }
func (l *fakeLink) State() UplinkState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == "" {
		return UplinkConnected
	}
	return l.state
}
func (l *fakeLink) setState(s UplinkState) { l.mu.Lock(); l.state = s; l.mu.Unlock() }
func (l *fakeLink) wasAborted() bool       { l.mu.Lock(); defer l.mu.Unlock(); return l.aborted }
func (l *fakeLink) sentCount() int         { l.mu.Lock(); defer l.mu.Unlock(); return len(l.sent) }

type fakeLinks struct {
	mu    sync.Mutex
	links map[Source]*fakeLink
}

func newFakeLinks() *fakeLinks { return &fakeLinks{links: map[Source]*fakeLink{}} }

func (f *fakeLinks) uplinker(cfg UplinkConfig) (Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := &fakeLink{cfg: cfg}
	f.links[cfg.Track] = l
	return l, nil
}

func (f *fakeLinks) get(src Source) *fakeLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[src]
}

// ── 助手 ────────────────────────────────────────────────────────────────

func newTestSession(t *testing.T, tweak func(*SessionConfig)) (*Session, *fakeCapturer, *fakeLinks) {
	t.Helper()
	capt := newFakeCapturer()
	links := newFakeLinks()
	cfg := SessionConfig{
		Root:       t.TempDir(),
		GatewayURL: "ws://127.0.0.1:0/ws",
		Capturer:   capt,
		Uplinker:   links.uplinker,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	return s, capt, links
}

// voice 造一帧有声音频（幅度够高，IsSilent 判为非静音）。
func voice() []int16 {
	out := make([]int16, FrameSamples)
	for i := range out {
		out[i] = int16(1000 + i%500)
	}
	return out
}

func frame(src Source, pcm []int16) Frame {
	return Frame{Source: src, PCM: pcm, Peak: 0.3, Silent: IsSilent(pcm)}
}

func wavDataBytes(t *testing.T, path string) int64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	if len(b) < wavHeaderSize {
		t.Fatalf("%s 只有 %d 字节", path, len(b))
	}
	return int64(binary.LittleEndian.Uint32(b[40:]))
}

// ── 测试 ────────────────────────────────────────────────────────────────

func TestSessionRecordsBothTracks(t *testing.T) {
	s, capt, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	if got := len(capt.openedSources()); got != 2 {
		t.Fatalf("应打开 2 条轨，实际 %d", got)
	}

	for i := 0; i < 5; i++ {
		capt.stream(SourceMic).push(frame(SourceMic, voice()))
		capt.stream(SourceLoopback).push(frame(SourceLoopback, voice()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("结束: %v", err)
	}

	want := int64(5 * FrameSamples * 2)
	for _, src := range []Source{SourceMic, SourceLoopback} {
		p := filepath.Join(s.Dir(), "track_"+string(src)+".wav")
		if got := wavDataBytes(t, p); got != want {
			t.Errorf("%s 轨落盘 %d 字节，期望 %d", src, got, want)
		}
		if got := links.get(src).sentCount(); got != 5 {
			t.Errorf("%s 轨上行 %d 帧，期望 5", src, got)
		}
	}

	m := s.Meta()
	if m.State != SessionStopped || m.EndedAt == nil {
		t.Errorf("结束后 meta 状态不对：%+v", m.State)
	}
	if len(m.Tracks) != 2 {
		t.Errorf("meta 里应有 2 条轨，实际 %d", len(m.Tracks))
	}
}

// TestSessionMicSkipsDiarize 守一条产品判断：麦克风轨天然单人，跑说话人聚类
// 是白费 GPU。单卡 A10 下这直接换算成能同时开几场会。
func TestSessionMicSkipsDiarize(t *testing.T) {
	s, _, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	defer func() { _ = s.Discard() }()

	if links.get(SourceMic).cfg.Diarize {
		t.Error("麦克风轨不该开 diarize")
	}
	if !links.get(SourceLoopback).cfg.Diarize {
		t.Error("回环轨必须开 diarize——要区分对方那几个人")
	}
	if got := links.get(SourceMic).cfg.Room; got != s.Meta().ID {
		t.Errorf("两条轨要在同一个 room 里，得到 %q", got)
	}
}

// TestSessionOnlyLoopbackSilenceIsSkipped 守 IsSilent 那条实测结论：数字静音
// 只在回环轨上成立，麦克风永远有底噪，客户端不能替它做门控。
func TestSessionOnlyLoopbackSilenceIsSkipped(t *testing.T) {
	s, capt, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}

	silent := make([]int16, FrameSamples) // 全零 = 数字静音
	capt.stream(SourceLoopback).push(frame(SourceLoopback, silent))
	capt.stream(SourceMic).push(frame(SourceMic, silent))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("结束: %v", err)
	}

	if got := links.get(SourceLoopback).sentCount(); got != 0 {
		t.Errorf("回环轨的静音帧不该上行，实际发了 %d 帧", got)
	}
	if got := links.get(SourceMic).sentCount(); got != 1 {
		t.Errorf("麦克风轨一律上行（交给服务端 VAD），期望 1 帧，实际 %d", got)
	}
	// 但两条轨都必须完整落盘——本地那份是补转写的唯一依据。
	for _, src := range []Source{SourceMic, SourceLoopback} {
		p := filepath.Join(s.Dir(), "track_"+string(src)+".wav")
		if got := wavDataBytes(t, p); got != int64(FrameSamples*2) {
			t.Errorf("%s 轨静音帧也必须落盘，实际 %d 字节", src, got)
		}
	}
}

// TestSessionPauseDropsAudio 守暂停的语义：本地 WAV 与服务端时间轴同步不前进。
func TestSessionPauseDropsAudio(t *testing.T) {
	s, capt, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}

	capt.stream(SourceMic).push(frame(SourceMic, voice()))
	if err := s.Pause(); err != nil {
		t.Fatalf("暂停: %v", err)
	}
	if s.State() != SessionPaused {
		t.Errorf("状态应为 paused，实际 %s", s.State())
	}
	for i := 0; i < 10; i++ { // 暂停期间的音频应当全部丢弃
		capt.stream(SourceMic).push(frame(SourceMic, voice()))
	}
	if err := s.Resume(); err != nil {
		t.Fatalf("继续: %v", err)
	}
	capt.stream(SourceMic).push(frame(SourceMic, voice()))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("结束: %v", err)
	}

	want := int64(2 * FrameSamples * 2) // 暂停前 1 帧 + 继续后 1 帧
	if got := wavDataBytes(t, filepath.Join(s.Dir(), "track_mic.wav")); got != want {
		t.Errorf("落盘 %d 字节，期望 %d（暂停期间的 10 帧应被丢弃）", got, want)
	}
	if got := links.get(SourceMic).sentCount(); got != 2 {
		t.Errorf("上行 %d 帧，期望 2", got)
	}

	// 暂停/继续的状态转换要挡住非法调用。
	if err := s.Resume(); err == nil {
		t.Error("已结束的会话不该能继续")
	}
}

// TestSessionStartRollsBackOnFailure 守：只录到自己声音的「会议纪要」没有意义，
// 一条轨打不开就整体失败，不能把麦克风占着不放。
func TestSessionStartRollsBackOnFailure(t *testing.T) {
	s, capt, _ := newTestSession(t, nil)
	capt.failOn = SourceLoopback

	if err := s.Start(); err == nil {
		t.Fatal("回环轨打不开时 Start 应当失败")
	}
	if s.State() != SessionIdle {
		t.Errorf("失败后状态应留在 idle，实际 %s", s.State())
	}
	mic := capt.stream(SourceMic)
	if mic == nil {
		t.Fatal("麦克风轨应当被打开过")
	}
	mic.mu.Lock()
	closed := mic.closed
	mic.mu.Unlock()
	if !closed {
		t.Error("已打开的麦克风轨必须被收回，否则设备一直被占着")
	}
}

func TestSessionStopIsIdempotent(t *testing.T) {
	s, _, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.Stop(ctx); err != nil {
		t.Fatalf("首次结束: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Errorf("重复结束应当无害，得到 %v", err)
	}
	if !links.get(SourceMic).stopped {
		t.Error("正常结束必须给服务端发 stop，否则最后一块不会被 flush")
	}
}

// TestSessionDiscardAborts 守：放弃的录音不该在服务端留下半句话，
// 所以走 Abort 而不是 Stop（服务端把它当意外断开，不 flush）。
func TestSessionDiscardAborts(t *testing.T) {
	s, _, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	dir := s.Dir()
	if err := s.Discard(); err != nil {
		t.Fatalf("放弃: %v", err)
	}
	if links.get(SourceMic).stopped {
		t.Error("放弃录音不该给服务端发 stop")
	}
	if !links.get(SourceMic).aborted {
		t.Error("放弃录音应当 Abort")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("放弃后会话目录应被删除")
	}
}

// TestSessionDiskErrorStopsRecording 守方案 §08 那条：磁盘写不动必须立即停录、
// 保住已录部分、明确告警——绝不能静默，静默意味着用户以为在录、实际有洞。
func TestSessionDiskErrorStopsRecording(t *testing.T) {
	var mu sync.Mutex
	var reported []error
	s, capt, _ := newTestSession(t, func(c *SessionConfig) {
		c.OnError = func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() }
	})
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}

	// 把底层文件抽走，模拟磁盘写失败（写满、被安全软件锁住都是这个表现）。
	s.mu.Lock()
	_ = s.tracks[0].sink.f.Close()
	s.mu.Unlock()

	capt.stream(SourceMic).push(frame(SourceMic, voice()))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.State() != SessionStopped {
		time.Sleep(2 * time.Millisecond)
	}
	if s.State() != SessionStopped {
		t.Fatalf("写盘失败后会话应自行停止，实际 %s", s.State())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) == 0 {
		t.Error("写盘失败必须报给用户，不能静默")
	}
}

func TestSessionFlushReachesBothTracks(t *testing.T) {
	s, _, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	defer func() { _ = s.Discard() }()

	s.Flush()
	for _, src := range []Source{SourceMic, SourceLoopback} {
		links.get(src).mu.Lock()
		n := links.get(src).flushes
		links.get(src).mu.Unlock()
		if n != 1 {
			t.Errorf("%s 轨收到 %d 次 flush，期望 1", src, n)
		}
	}
}

// TestRecoverSessions 守 §08 的必做项：v3 单进程下录音窗崩了等于整个 app 崩，
// 没有第二个进程兜底，所以录音状态必须能从磁盘恢复。
func TestRecoverSessions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "rec_crashed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 造一个「录到一半被强杀」的现场：WAV 有数据但头还停在上次 fsync。
	wav := filepath.Join(dir, "track_mic.wav")
	sink, err := NewWAVSink(wav, TargetSampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	sink.SyncEvery = time.Hour // 保证不会自动 Flush
	if err := sink.Write(make([]int16, 3*TargetSampleRate)); err != nil {
		t.Fatal(err)
	}
	_ = sink.f.Close() // 强杀：不 Close，直接丢句柄

	if err := WriteMeta(dir, SessionMeta{
		ID: "rec_crashed", Title: "新录音 1", Room: "rec_crashed",
		State: SessionRecording, StartedAt: time.Now(),
		Tracks: []TrackMeta{{Source: SourceMic, WAV: "track_mic.wav"}},
	}); err != nil {
		t.Fatal(err)
	}

	// 再放一场正常结束的，它不该被恢复流程碰。
	okDir := filepath.Join(root, "rec_ok")
	if err := os.MkdirAll(okDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMeta(okDir, SessionMeta{
		ID: "rec_ok", Title: "新录音 2", State: SessionStopped, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := RecoverSessions(root)
	if err != nil {
		t.Fatalf("恢复: %v", err)
	}
	if len(got) != 1 || got[0].ID != "rec_crashed" {
		t.Fatalf("应只恢复被中断的那场，得到 %+v", got)
	}
	m := got[0]
	if !m.Interrupted {
		t.Error("恢复出来的会话要标记 Interrupted，界面据此提示用户")
	}
	if m.State != SessionStopped {
		t.Errorf("恢复后状态应为 stopped，实际 %s", m.State)
	}
	if m.Tracks[0].DurationMS != 3000 {
		t.Errorf("修复后时长 %d ms，期望 3000（头被 RepairWAV 补上）", m.Tracks[0].DurationMS)
	}
	if !m.NeedsBackfill() {
		t.Error("被中断的录音必然要走补转写")
	}

	// 幂等：再跑一次不该重复恢复。
	again, err := RecoverSessions(root)
	if err != nil {
		t.Fatalf("二次恢复: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("已恢复的会话不该被再次恢复，得到 %d 个", len(again))
	}
}

// TestNextTitleUsesMaxNotCount 守：删掉中间某场之后不应出现两场同名录音。
func TestNextTitleUsesMaxNotCount(t *testing.T) {
	root := t.TempDir()
	if got := NextTitle(root); got != "新录音 1" {
		t.Errorf("空目录应给「新录音 1」，得到 %q", got)
	}

	for _, tc := range []struct{ id, title string }{
		{"a", "新录音 1"}, {"b", "新录音 7"}, {"c", "项目相关事项讨论"},
	} {
		d := filepath.Join(root, tc.id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteMeta(d, SessionMeta{ID: tc.id, Title: tc.title}); err != nil {
			t.Fatal(err)
		}
	}
	// 按最大值 +1，不是按数量（数量会给出「新录音 4」，与已有的 7 之后冲突）。
	if got := NextTitle(root); got != "新录音 8" {
		t.Errorf("应取最大序号加一得「新录音 8」，得到 %q", got)
	}
}

func TestListSessionsSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"old", "mid", "new"} {
		d := filepath.Join(root, id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteMeta(d, SessionMeta{ID: id, StartedAt: base.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	// 混进一个不是会话的目录，不能让它把整个列举搞失败。
	if err := os.MkdirAll(filepath.Join(root, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListSessions(root)
	if err != nil {
		t.Fatalf("列举: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("应列出 3 场，得到 %d", len(got))
	}
	for i, want := range []string{"new", "mid", "old"} {
		if got[i].ID != want {
			t.Errorf("第 %d 个是 %q，期望 %q", i, got[i].ID, want)
		}
	}

	if _, err := ListSessions(filepath.Join(root, "不存在")); err != nil {
		t.Errorf("根目录不存在应返回空而不是报错，得到 %v", err)
	}
}

func TestMetaRoundTripAndBackfill(t *testing.T) {
	dir := t.TempDir()
	end := time.Date(2026, 8, 18, 12, 30, 0, 0, time.UTC)
	in := SessionMeta{
		ID: "rec_1", Title: "新录音 3", Room: "rec_1", State: SessionStopped,
		Lang: "auto", KeepAudio: true,
		StartedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC), EndedAt: &end,
		AudioMS: 1_800_000,
		Tracks: []TrackMeta{
			{Source: SourceMic, WAV: "track_mic.wav", Format: Format{SampleRate: 48000, Channels: 2}, DurationMS: 1_800_000},
			{Source: SourceLoopback, WAV: "track_sys.wav", DurationMS: 1_800_000, GapMS: 4200, Reconnects: 2},
		},
	}
	if err := WriteMeta(dir, in); err != nil {
		t.Fatalf("写: %v", err)
	}
	out, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("读: %v", err)
	}
	if out.ID != in.ID || out.Title != in.Title || out.AudioMS != in.AudioMS ||
		len(out.Tracks) != 2 || out.Tracks[1].GapMS != 4200 {
		t.Errorf("往返不一致：%+v", out)
	}
	if !out.NeedsBackfill() {
		t.Error("有 GapMS 就必须走补转写——服务端那份转写是有洞的")
	}

	in.Tracks[1].GapMS = 0
	if err := WriteMeta(dir, in); err != nil {
		t.Fatal(err)
	}
	clean, _ := ReadMeta(dir)
	if clean.NeedsBackfill() {
		t.Error("没有缺口也没被中断，不该要求补转写")
	}

	// 临时文件不能留在目录里——它会被 ListSessions/清理逻辑当成噪音。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != metaFileName {
			t.Errorf("目录里残留了 %q", e.Name())
		}
	}
}

func TestNewSessionValidates(t *testing.T) {
	if _, err := NewSession(SessionConfig{Capturer: newFakeCapturer()}); err == nil {
		t.Error("根目录为空应当报错")
	}
	if _, err := NewSession(SessionConfig{Root: t.TempDir()}); err == nil {
		t.Error("未注入 Capturer 应当报错")
	}
}

// TestSessionSameSecondGetsOwnDir 盯住会话目录的独占认领。
//
// 场景是真实的：会开完点「结束」，想起还有一段要单独录，马上又点「开始」。
// id 只精确到秒，两场撞进同一个目录的话，第二个 WAVSink 会把第一场的录音从头
// 覆写掉——一小时的会就这么没了，而且两场共用一个 room 号，服务端会把它们并
// 进同一条时间轴。
func TestSessionSameSecondGetsOwnDir(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 8, 18, 15, 30, 45, 0, time.UTC)

	newAt := func() *Session {
		s, err := NewSession(SessionConfig{
			Root:     root,
			Capturer: newFakeCapturer(),
			Uplinker: newFakeLinks().uplinker,
			Now:      func() time.Time { return fixed },
		})
		if err != nil {
			t.Fatalf("建会话: %v", err)
		}
		return s
	}

	first, second := newAt(), newAt()
	if first.Dir() == second.Dir() {
		t.Fatalf("同一秒的两场录音共用了目录 %s", first.Dir())
	}
	if first.Meta().ID == second.Meta().ID {
		t.Fatalf("同一秒的两场录音共用了 id %s —— room 号也会撞", first.Meta().ID)
	}

	// 两份 meta 都要在，各自读得回来。
	for _, s := range []*Session{first, second} {
		m, err := ReadMeta(s.Dir())
		if err != nil {
			t.Fatalf("读 %s 的 meta: %v", s.Dir(), err)
		}
		if m.ID != s.Meta().ID {
			t.Fatalf("meta 被另一场覆盖了：%s != %s", m.ID, s.Meta().ID)
		}
	}
}

// TestSessionLevelComesFromCapture 盯住电平走的是采集层那条密集回调，而不是
// 600 ms 一个的帧——靠帧驱动的话电平条每秒只动 1.67 次，看着像卡死了。
func TestSessionLevelComesFromCapture(t *testing.T) {
	var mu sync.Mutex
	got := map[Source][]float32{}
	s, capt, _ := newTestSession(t, func(c *SessionConfig) {
		c.OnLevel = func(src Source, peak float32) {
			mu.Lock()
			got[src] = append(got[src], peak)
			mu.Unlock()
		}
	})
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	// 一帧都不推，只报电平：这正是「说话了但还没攒够 600 ms」的那段时间，
	// 界面上的条必须已经在动了。
	capt.stream(SourceMic).level(0.8)
	capt.stream(SourceMic).level(0.3)
	capt.stream(SourceLoopback).level(0.5)

	mu.Lock()
	defer mu.Unlock()
	if len(got[SourceMic]) != 2 || got[SourceMic][0] != 0.8 {
		t.Fatalf("麦克风电平不对：%v", got[SourceMic])
	}
	if len(got[SourceLoopback]) != 1 || got[SourceLoopback][0] != 0.5 {
		t.Fatalf("回环电平不对：%v", got[SourceLoopback])
	}
}

// level 模拟一次采集层的电平回调（比 push 密得多，见 OpenConfig.OnLevel）。
func (s *fakeStream) level(peak float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.onLvl == nil {
		return
	}
	s.onLvl(peak)
}

// TestSessionStopSkipsWaitWhenOffline 盯住「离线时结束不干等」。
//
// 网关地址填错、或转写服务没起，是这个功能最常见的第一次失败。那时点「结束」
// 如果还要等满 stopGrace 再抛一句「等待服务端收尾超时」，用户会以为录音也出了
// 问题——其实本地那份完好。链路离线时直接 Abort，立刻返回。
func TestSessionStopSkipsWaitWhenOffline(t *testing.T) {
	s, capt, links := newTestSession(t, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("开始: %v", err)
	}
	capt.stream(SourceMic).push(frame(SourceMic, voice()))

	for _, src := range []Source{SourceMic, SourceLoopback} {
		links.get(src).setState(UplinkOffline)
		links.get(src).stopDelay = time.Hour // 真去等就会超时，测试也就挂了
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("离线时结束不该报错: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("离线时结束等了 %v，应当立刻返回", d)
	}
	for _, src := range []Source{SourceMic, SourceLoopback} {
		if !links.get(src).wasAborted() {
			t.Fatalf("%s 轨的链路应当被 Abort 而不是 Stop", src)
		}
	}
}
