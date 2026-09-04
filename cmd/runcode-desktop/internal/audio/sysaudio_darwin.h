// macOS 系统音频捕获的 C 接口——Go 与 Objective-C 的边界。
//
// 为什么需要它：CoreAudio 不提供任何形式的回环，miniaudio 因此在 macOS 上录不到
// 系统声音。ScreenCaptureKit（macOS 13+）是苹果给的那条路：它本是屏幕录制接口，
// 但同时能捕获系统音频，且不需要用户安装虚拟声卡。
//
// 音频以**交错的 float32** 交回 Go（回调里已经把 ScreenCaptureKit 给的分平面数据
// 交错好），与 malgo 那条路的格式一致，好让两边共用同一条 DSP 链。

#ifndef RC_SYSAUDIO_DARWIN_H
#define RC_SYSAUDIO_DARWIN_H

#include <stdint.h>

// rc_sysaudio_cb 在音频线程上被调用，必须快速返回。
// frames 是每声道的采样点数，channels 是声道数，samples 长度 = frames * channels。
//
// samples 故意**不加 const**：这个类型要接住 cgo 为 Go 导出函数生成的那个声明，
// 而 cgo 对 *C.float 生成的就是 float*。加了 const 的后果是编译期
// "conflicting types for rcSysAudioTrampoline"——两份签名差一个限定符。
typedef void (*rc_sysaudio_cb)(uintptr_t handle, float *samples, int frames, int channels);

// rc_sysaudio_start 启动一路系统音频捕获。
//
// handle 是 Go 侧的 cgo.Handle，原样回传给回调，用来找到对应的 stream。
// 成功返回 0 并把捕获句柄写进 *out；失败返回非 0，错误信息写进 errbuf。
//
// 同步返回：内部等待 ScreenCaptureKit 的异步启动完成（含用户授权那一步），
// 上层因此不必处理"还没开始就先返回了"的中间态。
int rc_sysaudio_start(uintptr_t handle, rc_sysaudio_cb cb, int sample_rate, int channels,
                      void **out, char *errbuf, int errlen);

// rc_sysaudio_stop 停止并释放。传 NULL 是安全的。
void rc_sysaudio_stop(void *capture);

// rc_sysaudio_available 报告本机能否走这条路（macOS 13 以上）。
int rc_sysaudio_available(void);

#endif
