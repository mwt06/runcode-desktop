// Package recorder 是录音纪要的采集核：把系统音频归一成网关要的 16 kHz 单声道
// PCM，同时落一份完整的本地 WAV。
//
// 分工：本包只放**接口与纯逻辑**，不含 cgo。具体的设备采集（Windows 上是
// WASAPI/malgo）由桌面外壳实现 Capturer 接口后注入——和 desktop.Dialoger 一个
// 模式，为的是根模块的 `go build ./...` 与 CI 不被音频 cgo 依赖污染。
//
// 数据流：
//
//	设备原生格式（常见 48 kHz 立体声 f32）
//	  → DownmixToMono   合成单声道（取平均，不是取左声道）
//	  → Resampler       带限多相重采样到 16 kHz（实测混叠抑制 108.9 dB）
//	  → FloatToInt16    转 s16le 并硬限幅
//	  → Framer          攒成与服务端 chunk 对齐的 600 ms 定长帧
//	  → WAVSink 落盘（先）＋ WebSocket 上行（后）
//
// 「先落盘，再上行」这个顺序是整个功能的可靠性地基：网络断了、服务端实例挂了、
// GPU 排队排到明天，本地那份录音都必须是完整的，而且必须随时是一个能直接播放的
// 合法 WAV——所以 WAVSink 每次 Flush 都回写 RIFF 头，进程被强杀后还能用
// RepairWAV 把最后一段补回来。
package recorder
