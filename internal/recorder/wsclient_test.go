package recorder

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeGateway 是一个替身网关，按 gateway/app.py 的协议收发：首帧 JSON 配置、
// 之后二进制 PCM、控制指令走文本帧。
//
// 用真的 WebSocket 服务端而不是打桩 Dialer：协议这一层的坑（首帧字段名、二进制
// 字节序、stop 之后谁先关连接）只有真跑一遍才暴露得出来。
type fakeGateway struct {
	srv     *httptest.Server
	url     string
	handle  func(g *fakeGateway, c *websocket.Conn, first map[string]any, connNo int)
	mu      sync.Mutex
	firsts  []map[string]any
	binary  map[int][][]byte // 按连接序号分开记，重连相关的断言要用
	ctrls   []string
	connNum int
}

func newFakeGateway(t *testing.T, handle func(*fakeGateway, *websocket.Conn, map[string]any, int)) *fakeGateway {
	t.Helper()
	g := &fakeGateway{handle: handle, binary: map[int][][]byte{}}

	up := websocket.Upgrader{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()

		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var first map[string]any
		if err := json.Unmarshal(data, &first); err != nil {
			return
		}
		g.mu.Lock()
		g.connNum++
		no := g.connNum
		g.firsts = append(g.firsts, first)
		g.mu.Unlock()

		_ = c.WriteJSON(Event{Type: "ready", Room: str(first["room"]), Track: str(first["track"])})
		g.handle(g, c, first, no)
	}))
	g.url = "ws" + strings.TrimPrefix(g.srv.URL, "http") + "/ws"
	t.Cleanup(g.srv.Close)
	return g
}

// readLoop 是默认的连接处理：一直读到对端断开，把二进制和控制指令记下来。
// 收到 stop 就发一条 final 再关连接——服务端真实行为就是这样（flush 最后一块）。
func readLoop(g *fakeGateway, c *websocket.Conn, _ map[string]any, no int) {
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		g.mu.Lock()
		switch mt {
		case websocket.BinaryMessage:
			g.binary[no] = append(g.binary[no], append([]byte(nil), data...))
		case websocket.TextMessage:
			g.ctrls = append(g.ctrls, string(data))
		}
		g.mu.Unlock()

		if mt == websocket.TextMessage && strings.Contains(string(data), `"stop"`) {
			_ = c.WriteJSON(Event{Type: "final", Track: "mic", Seg: 0, Rev: RevFinal, Text: "收尾那一句"})
			return // 关连接，客户端据此判定收尾完成
		}
	}
}

func (g *fakeGateway) firstFrames() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.firsts...)
}

func (g *fakeGateway) binaryOf(no int) [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]byte(nil), g.binary[no]...)
}

func (g *fakeGateway) controls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.ctrls...)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// waitFor 轮询等条件成立，避免固定 sleep 带来的偶发失败。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

func fastCfg(g *fakeGateway) UplinkConfig {
	return UplinkConfig{
		URL:           g.url,
		Room:          "rec_test",
		Track:         SourceMic,
		ReconnectBase: 20 * time.Millisecond,
		ReconnectMax:  40 * time.Millisecond,
	}
}

func TestUplinkFirstFrame(t *testing.T) {
	g := newFakeGateway(t, readLoop)
	cfg := fastCfg(g)
	cfg.Track = SourceLoopback
	cfg.Name = "卢艳"
	cfg.Diarize = true
	cfg.Lang = "auto"
	cfg.OffsetMS = 1500
	cfg.Auth = "jwt-abc"

	u, err := NewUplink(cfg)
	if err != nil {
		t.Fatalf("建链路: %v", err)
	}
	defer u.Abort()

	waitFor(t, "首帧到达", func() bool { return len(g.firstFrames()) == 1 })
	f := g.firstFrames()[0]

	for k, want := range map[string]any{
		"room": "rec_test", "track": "sys", "role": "publisher",
		"name": "卢艳", "lang": "auto", "auth": "jwt-abc",
	} {
		if got := str(f[k]); got != want {
			t.Errorf("首帧 %s = %q，期望 %q", k, got, want)
		}
	}
	if f["diarize"] != true {
		t.Errorf("回环轨的 diarize 应为 true，得到 %v", f["diarize"])
	}
	if f["offset_ms"] != float64(1500) {
		t.Errorf("offset_ms = %v，期望 1500", f["offset_ms"])
	}
}

// TestUplinkMicTrackDisablesDiarize 守一条产品判断：麦克风轨天然单人，
// 跑说话人聚类是白费 GPU——单卡 A10 下这不是小事。
func TestUplinkMicTrackDisablesDiarize(t *testing.T) {
	g := newFakeGateway(t, readLoop)
	cfg := fastCfg(g)
	cfg.Track = SourceMic
	cfg.Diarize = false

	u, _ := NewUplink(cfg)
	defer u.Abort()

	waitFor(t, "首帧到达", func() bool { return len(g.firstFrames()) == 1 })
	if f := g.firstFrames()[0]; f["diarize"] != false {
		t.Errorf("麦克风轨的 diarize 应为 false，得到 %v", f["diarize"])
	}
}

func TestUplinkSendsPCMLittleEndian(t *testing.T) {
	// 服务端是 np.frombuffer(data, dtype=np.int16)，numpy 用本机字节序。
	// 字节序搞反的表现是音频变成噪声，但**链路一切正常**——最难查的那类故障。
	g := newFakeGateway(t, readLoop)
	u, _ := NewUplink(fastCfg(g))
	defer u.Abort()

	waitFor(t, "连接建立", func() bool { return u.State() == UplinkConnected })
	u.Send([]int16{1, -1, 256, -32768, 32767})

	waitFor(t, "收到 PCM", func() bool { return len(g.binaryOf(1)) == 1 })
	got := g.binaryOf(1)[0]

	want := []int16{1, -1, 256, -32768, 32767}
	if len(got) != len(want)*2 {
		t.Fatalf("收到 %d 字节，期望 %d", len(got), len(want)*2)
	}
	for i, w := range want {
		if v := int16(binary.LittleEndian.Uint16(got[i*2:])); v != w {
			t.Errorf("第 %d 个样本 %d != %d", i, v, w)
		}
	}
}

func TestUplinkDecodesEvents(t *testing.T) {
	g := newFakeGateway(t, func(g *fakeGateway, c *websocket.Conn, _ map[string]any, _ int) {
		_ = c.WriteJSON(Event{Type: "partial", Track: "mic", Seg: LiveSeg, Rev: RevPartial, Text: "之后我们就", BT: 3030})
		_ = c.WriteJSON(Event{
			Type: "final", Track: "mic", Seg: 7, Rev: RevFinal,
			Text: "庙里有个和尚。", BT: 1050, ET: 4200, Spk: "spk1", Name: "张三", Conf: 0.86,
		})
		_ = c.WriteJSON(Event{Type: "drop", Track: "mic", Segs: []int{5, 6}})
		_ = c.WriteJSON(Event{Type: "live_status", Track: "mic", Silence: 2, Need: 3})
		_ = c.WriteJSON(Event{Type: "stage", Stage: "refined", QueuePos: 4})
		readLoop(g, c, nil, 0)
	})

	var mu sync.Mutex
	var evs []Event
	cfg := fastCfg(g)
	cfg.OnEvent = func(e Event) { mu.Lock(); evs = append(evs, e); mu.Unlock() }

	u, _ := NewUplink(cfg)
	defer u.Abort()

	waitFor(t, "收齐事件", func() bool { mu.Lock(); defer mu.Unlock(); return len(evs) >= 6 })
	mu.Lock()
	defer mu.Unlock()

	if evs[0].Type != "ready" {
		t.Errorf("第一条应是 ready，得到 %q", evs[0].Type)
	}
	if p := evs[1]; p.Type != "partial" || p.Seg != LiveSeg || p.Rev != RevPartial || p.Text != "之后我们就" {
		t.Errorf("partial 解码错：%+v", p)
	}
	f := evs[2]
	if f.Type != "final" || f.Seg != 7 || f.Rev != RevFinal || f.Text != "庙里有个和尚。" ||
		f.BT != 1050 || f.ET != 4200 || f.Spk != "spk1" || f.Name != "张三" || f.Conf != 0.86 {
		t.Errorf("final 解码错：%+v", f)
	}
	if d := evs[3]; d.Type != "drop" || len(d.Segs) != 2 || d.Segs[0] != 5 || d.Segs[1] != 6 {
		t.Errorf("drop 解码错：%+v", d)
	}
	if s := evs[4]; s.Type != "live_status" || s.Silence != 2 || s.Need != 3 {
		t.Errorf("live_status 解码错：%+v", s)
	}
	if s := evs[5]; s.Type != "stage" || s.Stage != "refined" || s.QueuePos != 4 {
		t.Errorf("stage 解码错：%+v", s)
	}
}

func TestUplinkCleanStop(t *testing.T) {
	g := newFakeGateway(t, readLoop)

	var mu sync.Mutex
	var texts []string
	cfg := fastCfg(g)
	cfg.OnEvent = func(e Event) {
		if e.Type == "final" {
			mu.Lock()
			texts = append(texts, e.Text)
			mu.Unlock()
		}
	}

	u, _ := NewUplink(cfg)
	waitFor(t, "连接建立", func() bool { return u.State() == UplinkConnected })
	u.Send(make([]int16, FrameSamples))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := u.Stop(ctx); err != nil {
		t.Fatalf("正常收尾失败: %v", err)
	}

	var sawStop bool
	for _, c := range g.controls() {
		if strings.Contains(c, `"stop"`) {
			sawStop = true
		}
	}
	if !sawStop {
		t.Errorf("服务端没收到 stop 指令，收到的是 %v", g.controls())
	}
	// 收尾时服务端 flush 出来的那句必须收得到——不然「最后一句没了」。
	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 1 || texts[0] != "收尾那一句" {
		t.Errorf("没收到收尾 final，得到 %v", texts)
	}
}

func TestUplinkFlushKeepsSessionAlive(t *testing.T) {
	g := newFakeGateway(t, readLoop)
	u, _ := NewUplink(fastCfg(g))
	defer u.Abort()

	waitFor(t, "连接建立", func() bool { return u.State() == UplinkConnected })
	u.Flush()
	waitFor(t, "收到 flush", func() bool {
		for _, c := range g.controls() {
			if strings.Contains(c, `"flush"`) {
				return true
			}
		}
		return false
	})

	// flush 之后会话要继续，还能接着送音频。
	u.Send(make([]int16, 160))
	waitFor(t, "flush 后仍能上行", func() bool { return len(g.binaryOf(1)) >= 1 })
}

// TestUplinkReconnectsWithSameTrack 守服务端 120 秒会话保留那条：同 track id
// 重连才能续上分段编号和时间轴。换了 id 的表现是一场会里出现两段互不相干的转写。
func TestUplinkReconnectsWithSameTrack(t *testing.T) {
	g := newFakeGateway(t, func(g *fakeGateway, c *websocket.Conn, first map[string]any, no int) {
		if no == 1 {
			return // 第一条连接直接掐掉，模拟网断
		}
		readLoop(g, c, first, no)
	})

	var mu sync.Mutex
	var states []UplinkState
	cfg := fastCfg(g)
	cfg.OnState = func(s UplinkState) { mu.Lock(); states = append(states, s); mu.Unlock() }

	u, _ := NewUplink(cfg)
	defer u.Abort()

	waitFor(t, "重连后再次建立", func() bool { return len(g.firstFrames()) >= 2 })

	fs := g.firstFrames()
	if fs[0]["track"] != fs[1]["track"] || fs[0]["room"] != fs[1]["room"] {
		t.Errorf("重连换了 room/track：%v → %v", fs[0], fs[1])
	}
	if got := u.Stats().Reconnects; got < 1 {
		t.Errorf("Reconnects = %d，期望 ≥1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawOffline bool
	for _, s := range states {
		if s == UplinkOffline {
			sawOffline = true
		}
	}
	if !sawOffline {
		t.Errorf("断线期间应报 offline（界面上的「离线录制中」），实际状态序列 %v", states)
	}
}

// TestUplinkDropsStaleAudioOnReconnect 守「断线期间的音频直接丢，不缓存补发」。
//
// 发送队列有 32 帧缓冲，如果重连时不清空，一次断线会把近 20 秒的陈音频一次性
// 灌给服务端——服务端会把它们当成刚说的话去识别，时间轴直接错位。
func TestUplinkDropsStaleAudioOnReconnect(t *testing.T) {
	g := newFakeGateway(t, func(g *fakeGateway, c *websocket.Conn, first map[string]any, no int) {
		if no == 1 {
			return // 掐掉，进入重连
		}
		readLoop(g, c, first, no)
	})

	offline := make(chan struct{})
	connected := make(chan struct{})
	var once1, once2 sync.Once
	cfg := fastCfg(g)
	cfg.ReconnectBase = 250 * time.Millisecond // 留出确定的窗口来灌陈音频
	cfg.ReconnectMax = 250 * time.Millisecond
	cfg.OnState = func(s UplinkState) {
		switch s {
		case UplinkOffline:
			once1.Do(func() { close(offline) })
		case UplinkConnected:
			// 第一次 connected 是初始连接，这里只关心重连后那次
			select {
			case <-offline:
				once2.Do(func() { close(connected) })
			default:
			}
		}
	}

	u, _ := NewUplink(cfg)
	defer u.Abort()

	<-offline
	// 断线期间灌满队列并溢出：40 帧 > 32 的队列深度。
	stale := make([]int16, FrameSamples)
	for i := 0; i < 40; i++ {
		u.Send(stale)
	}

	<-connected
	// 重连之后发一帧可识别的标记帧。
	marker := []int16{111, 222, 333}
	u.Send(marker)

	waitFor(t, "标记帧到达", func() bool { return len(g.binaryOf(2)) >= 1 })
	time.Sleep(50 * time.Millisecond) // 若有陈音频漏出，这段时间足够它到达

	got := g.binaryOf(2)
	if len(got) != 1 {
		t.Fatalf("重连后服务端收到 %d 帧，期望只有 1 帧标记帧（陈音频应被丢弃）", len(got))
	}
	if len(got[0]) != len(marker)*2 {
		t.Fatalf("收到的不是标记帧：%d 字节", len(got[0]))
	}

	st := u.Stats()
	if st.Dropped < 40 {
		t.Errorf("Dropped = %d，40 帧陈音频应全部计入丢弃", st.Dropped)
	}
	// GapMS 是会后决定要不要走补转写的依据，必须真实反映丢了多少秒。
	if wantMS := int64(40) * int64(FrameSamples) * 1000 / TargetSampleRate; st.GapMS < wantMS {
		t.Errorf("GapMS = %d，期望 ≥ %d", st.GapMS, wantMS)
	}
	if st.Sent != 1 {
		t.Errorf("Sent = %d，期望 1（只有标记帧真正发出去了）", st.Sent)
	}
}

func TestUplinkRejectsBadConfig(t *testing.T) {
	if _, err := NewUplink(UplinkConfig{Room: "r", Track: SourceMic}); err == nil {
		t.Error("地址为空应当报错")
	}
	if _, err := NewUplink(UplinkConfig{URL: "ws://x/ws", Track: SourceMic}); err == nil {
		t.Error("room 为空应当报错")
	}
	if _, err := NewUplink(UplinkConfig{URL: "ws://x/ws", Room: "r"}); err == nil {
		t.Error("track 为空应当报错")
	}
}

// TestUplinkSendNeverBlocks 守一条硬约束：Send 跑在音频回调链上，阻塞它会直接
// 导致**本地录音**丢音，那比丢上行严重得多。
func TestUplinkSendNeverBlocks(t *testing.T) {
	// 指向一个不存在的地址，永远连不上，队列会被灌满。
	cfg := UplinkConfig{
		URL: "ws://127.0.0.1:1/ws", Room: "r", Track: SourceMic,
		ReconnectBase: time.Hour, ReconnectMax: time.Hour,
	}
	u, err := NewUplink(cfg)
	if err != nil {
		t.Fatalf("建链路: %v", err)
	}
	defer u.Abort()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < sendQueueDepth*4; i++ {
			u.Send(make([]int16, FrameSamples))
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Send 阻塞了——音频回调会被拖死")
	}
	if u.Stats().Dropped == 0 {
		t.Error("队列满时应当计入丢弃")
	}
}
