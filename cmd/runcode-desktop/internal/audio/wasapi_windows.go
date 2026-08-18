//go:build windows

// Package audio 是 recorder.Capturer 的 Windows 实现，基于 malgo（miniaudio 的
// cgo 绑定）走 WASAPI。
//
// 它住在桌面嵌套 module 里而不是核心的 internal/recorder，理由与 Wails 完全相同：
// 根模块的 `go build ./...` 与 CI 不能被音频 cgo 依赖污染。核心模块只定义
// recorder.Capturer 接口，由外壳注入本实现——和 desktop.Dialoger 一个模式。
//
// 所有 DSP 都调核心模块的函数，本包不自己算：重采样、降混、限幅、分帧都在
// internal/recorder 里有测试守着，这里只负责把设备缓冲喂进去。
package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// Capturer 持有一个 malgo 上下文，供进程内所有采集共用。
//
// 上下文初始化要枚举设备、起 COM，开销不小，不能每开一路采集建一个。
type Capturer struct {
	mu  sync.Mutex
	ctx *malgo.AllocatedContext
}

// New 初始化 WASAPI 上下文。
//
// 显式只挂 WASAPI 后端：miniaudio 默认会依次尝试 WASAPI → DirectSound → WinMM，
// 而**只有 WASAPI 支持 loopback**。让它悄悄回落到 DirectSound 的后果是回环轨
// 打不开，而报错信息只会说「设备不可用」，非常难查。
func New() (*Capturer, error) {
	ctx, err := malgo.InitContext([]malgo.Backend{malgo.BackendWasapi}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("初始化 WASAPI: %w", err)
	}
	return &Capturer{ctx: ctx}, nil
}

// Close 释放上下文。所有 Stream 都关掉之后再调。
func (c *Capturer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		return nil
	}
	err := c.ctx.Uninit()
	c.ctx.Free()
	c.ctx = nil
	return err
}

// Devices 列出该音源可用的设备。
//
// 注意回环轨枚举的是**播放**设备：WASAPI loopback 的语义是「录下某个输出端点
// 正在播的东西」，所以要选的是扬声器/耳机，不是麦克风。这一点每次读代码都会
// 想反一次，所以写在这里。
func (c *Capturer) Devices(src recorder.Source) ([]recorder.DeviceInfo, error) {
	kind := malgo.Capture
	if src == recorder.SourceLoopback {
		kind = malgo.Playback
	}

	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx == nil {
		return nil, fmt.Errorf("采集上下文已关闭")
	}

	infos, err := ctx.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("枚举设备: %w", err)
	}
	out := make([]recorder.DeviceInfo, 0, len(infos))
	for i := range infos {
		out = append(out, recorder.DeviceInfo{
			ID:        infos[i].ID.String(),
			Name:      infos[i].Name(),
			IsDefault: infos[i].IsDefault != 0,
		})
	}
	return out, nil
}

// Open 启动一路采集。
func (c *Capturer) Open(cfg recorder.OpenConfig) (recorder.Stream, error) {
	if cfg.OnFrame == nil {
		return nil, fmt.Errorf("OnFrame 不能为空")
	}

	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	if ctx == nil {
		return nil, fmt.Errorf("采集上下文已关闭")
	}

	devType := malgo.Capture
	if cfg.Source == recorder.SourceLoopback {
		devType = malgo.Loopback
	}
	dc := malgo.DefaultDeviceConfig(devType)

	// 要设备的**原生**格式：SampleRate/Channels 留 0，让 miniaudio 用端点本身的
	// 混音格式，重采样和降混我们自己做。
	//
	// 不让 miniaudio 代劳，是因为它默认的线性重采样器只有一阶低通，8 kHz 以上的
	// 成分会折叠回语音频段——正是 recorder.TestNoAliasing 守的那个坑。我们自己
	// 那条路是 264 抽头、实测 108.9 dB 抑制。
	dc.SampleRate = 0
	dc.Capture.Format = malgo.FormatF32
	dc.Capture.Channels = 0
	dc.Capture.ShareMode = malgo.Shared // 绝不用 Exclusive：会抢走设备，把正在开的会议软件搞哑

	if cfg.DeviceID != "" {
		id, err := findDeviceID(ctx, cfg.Source, cfg.DeviceID)
		if err != nil {
			return nil, err
		}
		dc.Capture.DeviceID = id.Pointer()
	}

	s := &stream{onFrame: cfg.OnFrame, onError: cfg.OnError, source: cfg.Source}

	dev, err := malgo.InitDevice(ctx.Context, dc, malgo.DeviceCallbacks{Data: s.onData})
	if err != nil {
		return nil, fmt.Errorf("打开%s采集: %w", sourceLabel(cfg.Source), err)
	}
	s.dev = dev

	// 协商结果只有 InitDevice 之后才知道，DSP 链要按它来搭。
	s.format = recorder.Format{
		SampleRate: int(dev.CaptureInternalSampleRate()),
		Channels:   int(dev.CaptureInternalChannels()),
	}
	if s.format.SampleRate <= 0 || s.format.Channels <= 0 {
		dev.Uninit()
		return nil, fmt.Errorf("%s设备返回了非法格式 %d Hz / %d 声道",
			sourceLabel(cfg.Source), s.format.SampleRate, s.format.Channels)
	}
	s.res = recorder.NewResampler(s.format.SampleRate, recorder.TargetSampleRate)
	s.framer = recorder.NewFramer(recorder.FrameSamples)

	if err := dev.Start(); err != nil {
		dev.Uninit()
		return nil, fmt.Errorf("启动%s采集: %w", sourceLabel(cfg.Source), err)
	}
	return s, nil
}

func findDeviceID(ctx *malgo.AllocatedContext, src recorder.Source, want string) (malgo.DeviceID, error) {
	kind := malgo.Capture
	if src == recorder.SourceLoopback {
		kind = malgo.Playback
	}
	infos, err := ctx.Devices(kind)
	if err != nil {
		return malgo.DeviceID{}, fmt.Errorf("枚举设备: %w", err)
	}
	for i := range infos {
		if infos[i].ID.String() == want {
			return infos[i].ID, nil
		}
	}
	// 设备被拔掉是常态（会议中拔耳机），错误信息要能直接告诉用户发生了什么。
	return malgo.DeviceID{}, fmt.Errorf("找不到%s设备 %q（可能已被拔出或禁用）",
		sourceLabel(src), want)
}

func sourceLabel(s recorder.Source) string {
	if s == recorder.SourceLoopback {
		return "系统声音"
	}
	return "麦克风"
}

// stream 是一路正在跑的采集。
//
// DSP 的各级缓冲都挂在这里跨回调复用：onData 由音频线程以约 100 Hz 调用，
// 在里面分配是实时路径抖动的经典来源。这些字段只被 onData 一个 goroutine 碰，
// 不需要加锁。
type stream struct {
	dev     *malgo.Device
	source  recorder.Source
	format  recorder.Format
	res     *recorder.Resampler
	framer  *recorder.Framer
	onFrame func(recorder.Frame)
	onError func(error)

	fbuf []float32 // 设备字节解出来的交错浮点
	mbuf []float32 // 降混后的单声道
	ibuf []int16   // 转定点后的 PCM

	// lastPeak 是最近一次回调的峰值。一帧（600 ms）会跨多次回调，取最后一次
	// 对电平表足够——它只用来画一根跳动的条，不参与任何判决。
	lastPeak float32

	closeOnce sync.Once
}

func (s *stream) Format() recorder.Format { return s.format }

func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		// 先 Uninit 停掉回调再收尾，否则 Drain 可能与 onData 并发跑。
		s.dev.Uninit()
		// 把不足一帧的尾巴发出去——否则最后不到 600 ms 的话会被丢掉，
		// 而那往往正是「最后一句没了」的原因。
		s.framer.Drain(s.emit)
	})
	return nil
}

// onData 是音频线程的回调，必须快速返回。
//
// pOutput 在纯采集下用不到。pInput 是交错的 f32，长度 = framecount × 声道数。
func (s *stream) onData(_, pInput []byte, framecount uint32) {
	if framecount == 0 || len(pInput) == 0 {
		return
	}
	n := int(framecount) * s.format.Channels
	if need := n * 4; len(pInput) < need {
		// 驱动给的字节数与声明的帧数对不上，宁可丢这一块也不要越界读。
		if s.onError != nil {
			s.onError(fmt.Errorf("%s采集缓冲长度异常：%d 字节 < %d",
				sourceLabel(s.source), len(pInput), need))
		}
		return
	}

	// 字节 → float32。不用 unsafe 转切片：这里每秒约 10 万次转换，安全解码的
	// 开销可以忽略，换来不必给 gosec 开豁免。
	if cap(s.fbuf) < n {
		s.fbuf = make([]float32, n)
	}
	s.fbuf = s.fbuf[:n]
	for i := 0; i < n; i++ {
		s.fbuf[i] = math.Float32frombits(binary.LittleEndian.Uint32(pInput[i*4:]))
	}

	s.mbuf = recorder.DownmixToMono(s.fbuf, s.format.Channels, s.mbuf)
	mono := s.res.Process(s.mbuf)
	if len(mono) == 0 {
		return // 重采样器还在攒够一个输出点所需的输入
	}
	peak := recorder.PeakLevel(mono)
	s.ibuf = recorder.FloatToInt16(mono, s.ibuf)

	s.lastPeak = peak
	s.framer.Push(s.ibuf, s.emit)
}

func (s *stream) emit(pcm []int16) {
	s.onFrame(recorder.Frame{
		Source: s.source,
		PCM:    pcm,
		Peak:   s.lastPeak,
		Silent: recorder.IsSilent(pcm),
	})
}
