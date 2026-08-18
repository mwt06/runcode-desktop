// Command audioprobe 是双轨采集的现场验证工具，对应方案 M0 的两个闸门项：
// WASAPI loopback 在真实会议软件下能不能拿到对方的声音，以及两条轨的时间轴对不对得上。
//
// 它不进发布包，只在开发机上跑。
//
//	go run ./cmd/audioprobe -list                    # 看有哪些设备
//	go run ./cmd/audioprobe -seconds 20 -out ./probe # 双轨录 20 秒
//
// 验证方法：开着腾讯会议/Zoom 跑一遍，自己说几句、让对方说几句，然后听两个 WAV。
// mic 轨应该只有自己，sys 轨应该只有对方。**耳机和扬声器两种输出都要试**——
// 有些声卡在耳机插入时会切换端点，loopback 跟不跟得上是必须实测的。
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wt68/runcode/cmd/runcode-desktop/internal/audio"
	"github.com/wt68/runcode/internal/recorder"
)

func main() {
	var (
		list    = flag.Bool("list", false, "只列出设备然后退出")
		seconds = flag.Int("seconds", 15, "录制秒数")
		outDir  = flag.String("out", ".", "WAV 输出目录")
		micID   = flag.String("mic", "", "麦克风设备 ID（空 = 系统默认）")
		sysID   = flag.String("sys", "", "回环用的播放设备 ID（空 = 系统默认）")
	)
	flag.Parse()

	if err := run(*list, *seconds, *outDir, *micID, *sysID); err != nil {
		fmt.Fprintf(os.Stderr, "\n失败：%v\n", err)
		os.Exit(1)
	}
}

func run(list bool, seconds int, outDir, micID, sysID string) error {
	capt, err := audio.New()
	if err != nil {
		return err
	}
	defer func() { _ = capt.Close() }()

	if err := printDevices(capt); err != nil {
		return err
	}
	if list {
		return nil
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("建输出目录: %w", err)
	}

	tracks := []*track{
		{src: recorder.SourceMic, deviceID: micID},
		{src: recorder.SourceLoopback, deviceID: sysID},
	}

	fmt.Printf("\n开始录制 %d 秒。现在说几句话，并让对方（或播放器）出声。\n\n", seconds)
	for _, t := range tracks {
		if err := t.start(capt, outDir); err != nil {
			return err
		}
		defer t.stop()
	}

	// 电平表。10 Hz 刷新足够看出「有没有声音进来」，又不会把终端刷爆。
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("\r  剩余 %2.0fs   麦克风 %s   系统声音 %s",
			time.Until(deadline).Seconds(),
			meter(tracks[0].peak()), meter(tracks[1].peak()))
	}
	fmt.Print("\r\033[K")

	for _, t := range tracks {
		t.stop()
	}
	return report(tracks)
}

func printDevices(c *audio.Capturer) error {
	for _, src := range []recorder.Source{recorder.SourceMic, recorder.SourceLoopback} {
		label := "麦克风（采集设备）"
		if src == recorder.SourceLoopback {
			// 反直觉但确实如此：loopback 录的是某个**输出**端点正在播的东西。
			label = "系统声音（回环用的播放设备）"
		}
		devs, err := c.Devices(src)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s：\n", label)
		if len(devs) == 0 {
			fmt.Println("  (无)")
			continue
		}
		for _, d := range devs {
			star := " "
			if d.IsDefault {
				star = "*"
			}
			fmt.Printf("  %s %-42s %s\n", star, truncate(d.Name, 42), d.ID)
		}
	}
	return nil
}

// track 是探针里的一路采集：落一个 WAV，顺带统计。
//
// WAV 写入放在独立 goroutine 上，不在音频回调里做 I/O——回调阻塞会直接丢音。
// 这个形状就是后面真正的会话流水线要用的形状。
type track struct {
	src      recorder.Source
	deviceID string

	stream recorder.Stream
	sink   *recorder.WAVSink
	ch     chan []int16
	wg     sync.WaitGroup

	frames   atomic.Int64
	silent   atomic.Int64
	dropped  atomic.Int64
	peakBits atomic.Uint32 // float32 的位模式，回调与主线程之间无锁传递
	maxPeak  atomic.Uint32

	stopOnce sync.Once
	writeErr atomic.Pointer[error]
}

func (t *track) start(c *audio.Capturer, outDir string) error {
	path := filepath.Join(outDir, "probe_"+string(t.src)+".wav")
	sink, err := recorder.NewWAVSink(path, recorder.TargetSampleRate, 1)
	if err != nil {
		return err
	}
	t.sink = sink

	// 有界队列：满了就丢并计数。丢帧本身就是一个要看到的信号——说明下游跟不上。
	t.ch = make(chan []int16, 64)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for pcm := range t.ch {
			if err := t.sink.Write(pcm); err != nil {
				t.writeErr.Store(&err)
				return
			}
		}
	}()

	st, err := c.Open(recorder.OpenConfig{
		Source:   t.src,
		DeviceID: t.deviceID,
		OnFrame:  t.onFrame,
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "\n[%s] 采集出错：%v\n", t.src, err)
		},
	})
	if err != nil {
		_ = t.sink.Close()
		close(t.ch)
		t.wg.Wait()
		return err
	}
	t.stream = st

	f := st.Format()
	fmt.Printf("  %-4s 已打开：%d Hz / %d 声道 → 16000 Hz / 1 声道 → %s\n",
		t.src, f.SampleRate, f.Channels, path)
	return nil
}

func (t *track) onFrame(fr recorder.Frame) {
	t.frames.Add(1)
	if fr.Silent {
		t.silent.Add(1)
	}
	bits := math.Float32bits(fr.Peak)
	t.peakBits.Store(bits)
	for {
		old := t.maxPeak.Load()
		if bits <= old || t.maxPeak.CompareAndSwap(old, bits) {
			break
		}
	}

	// Frame.PCM 由采集层复用，回调返回即失效——必须拷贝再交给写盘 goroutine。
	buf := make([]int16, len(fr.PCM))
	copy(buf, fr.PCM)
	select {
	case t.ch <- buf:
	default:
		t.dropped.Add(1)
	}
}

func (t *track) stop() {
	t.stopOnce.Do(func() {
		if t.stream != nil {
			_ = t.stream.Close() // 会 Drain 出最后不足一帧的尾巴
		}
		close(t.ch)
		t.wg.Wait()
		if t.sink != nil {
			_ = t.sink.Close()
		}
	})
}

func (t *track) peak() float32    { return math.Float32frombits(t.peakBits.Load()) }
func (t *track) maxSeen() float32 { return math.Float32frombits(t.maxPeak.Load()) }

func report(tracks []*track) error {
	fmt.Println("结果：")
	fmt.Printf("  %-6s %8s %8s %8s %9s %9s\n", "轨", "帧数", "静音帧", "丢帧", "峰值", "时长")
	for _, t := range tracks {
		fmt.Printf("  %-6s %8d %8d %8d %9.3f %8.1fs\n",
			t.src, t.frames.Load(), t.silent.Load(), t.dropped.Load(),
			t.maxSeen(), t.sink.Duration().Seconds())
		if err := t.writeErr.Load(); err != nil {
			return fmt.Errorf("[%s] 写盘失败: %w", t.src, *err)
		}
	}

	// M0 的 go/no-go 就是这一条。
	sys := tracks[1]
	fmt.Println()
	switch {
	case sys.frames.Load() == 0:
		fmt.Println("  ✗ 回环轨一帧都没拿到 —— loopback 没打开。")
		return fmt.Errorf("回环采集未产出任何数据")
	case sys.silent.Load() == sys.frames.Load():
		fmt.Println("  ✗ 回环轨全程静音 —— 录制期间系统确实没出声，或 loopback 挂错了端点。")
		fmt.Println("    重跑一次，期间放一段音乐或让会议里的对方说话。")
		return fmt.Errorf("回环采集全程静音")
	default:
		ratio := float64(sys.frames.Load()-sys.silent.Load()) / float64(sys.frames.Load())
		fmt.Printf("  ✓ 回环轨拿到了系统声音（%.0f%% 的帧有声，峰值 %.3f）。\n",
			ratio*100, sys.maxSeen())
	}
	if tracks[0].frames.Load() > 0 && tracks[0].silent.Load() == tracks[0].frames.Load() {
		fmt.Println("  ! 麦克风轨全程静音 —— 检查麦克风权限与是否被其他程序独占。")
	}
	fmt.Println("\n  现在听一下两个 WAV：mic 应该只有你自己，sys 应该只有对方。")
	return nil
}

func meter(peak float32) string {
	const width = 20
	n := int(peak * 2.5 * width) // 放大一点，正常说话不会顶到满格
	if n > width {
		n = width
	}
	bar := make([]byte, width)
	for i := range bar {
		if i < n {
			bar[i] = '#'
		} else {
			bar[i] = '.'
		}
	}
	return string(bar)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
