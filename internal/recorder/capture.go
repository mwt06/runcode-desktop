package recorder

// 采集层的接口面。
//
// 具体实现（Windows 上是 WASAPI / malgo，带 cgo）住在桌面嵌套 module 里，由
// 外壳注入——和 desktop.Dialoger 是同一个模式，理由也相同：根模块的
// `go build ./...` 与 CI 不能被音频 cgo 依赖污染。
//
// 所有 DSP（重采样、降混、限幅、分帧）都留在本包，因为它们是纯函数、可测，
// 而且是最容易出静默错误的地方。实现层只负责把设备的原始缓冲喂进来。

import "time"

// Source 标识一路音频源。取值直接就是网关首帧里的 track 名。
//
// 双轨的价值在于这一刀是**物理上准确**的：mic 一定是本机使用者，sys 一定是
// 其他人。单麦盲分离的段级准确率实测只有 83.3%，而这一刀是 100%。
type Source string

const (
	// SourceMic 是麦克风——「我」。天然单人，网关侧 diarize 关掉。
	SourceMic Source = "mic"
	// SourceLoopback 是系统回环——「对方」。线上会议里远端的声音走系统输出，
	// WebView 的 getUserMedia 拿不到，这是采集层必须放在 Go 侧的唯一理由。
	SourceLoopback Source = "sys"
)

const (
	// TargetSampleRate 是网关要的采样率，不可配。
	TargetSampleRate = 16000
	// FrameDuration 与服务端的 chunk 尺寸对齐。实测流式单块计算中位 35.4 ms，
	// 余量 565 ms，所以 600 ms 这一档是稳的。
	FrameDuration = 600 * time.Millisecond
	// FrameSamples 是一帧的样本数（16000 × 0.6）。
	FrameSamples = TargetSampleRate * int(FrameDuration/time.Millisecond) / 1000
)

// Format 是设备实际协商出来的采集格式。
//
// 要记下来是因为它不由我们决定：WASAPI 共享模式跟随用户在声音控制面板里的设置，
// 常见 48 kHz 立体声，但 44.1 kHz 也完全可能。日志里带上它，出问题时能一眼看出
// 是不是重采样这一环。
type Format struct {
	SampleRate int
	Channels   int
}

// DeviceInfo 是一个可选的采集设备。
type DeviceInfo struct {
	ID        string
	Name      string
	IsDefault bool
}

// Frame 是交给上层的一块音频，已经归一到 16 kHz 单声道 s16le。
//
// PCM 由采集层复用，回调返回后即失效——要留着必须自己拷贝。这是为了避免每
// 600 ms 一次堆分配，一小时录音两条轨就是一万两千次。
type Frame struct {
	Source Source
	PCM    []int16
	// Peak 是这一帧归一化前的峰值（0..1），给界面电平表用。
	Peak float32
	// Silent 表示这一帧判定为静音，上层可以直接不上行。
	//
	// 回环轨在没人说话时是**严格全零**（不是低电平噪声），滤掉它能显著抬高
	// 服务端的实际并发容量——单卡 A10 下这一条直接换算成能同时开几场会。
	Silent bool
}

// Stream 是一路已经打开的采集。Close 幂等。
type Stream interface {
	Format() Format
	Close() error
}

// OpenConfig 描述要打开的一路采集。
type OpenConfig struct {
	Source Source
	// DeviceID 为空表示用系统默认设备。「麦克风被占用」时让用户改选别的。
	DeviceID string
	// OnFrame 在采集线程上被调用，必须快速返回——里面不要做 I/O 或加锁等待，
	// 阻塞它会直接导致丢音。
	OnFrame func(Frame)
	// OnError 报告采集中途的失败（设备被拔、驱动重启）。上层据此切设备重开。
	OnError func(error)
}

// Capturer 打开系统音频采集。桌面外壳注入具体实现。
type Capturer interface {
	// Devices 列出该音源下可用的设备。
	Devices(Source) ([]DeviceInfo, error)
	// Open 启动一路采集。
	Open(OpenConfig) (Stream, error)
}
