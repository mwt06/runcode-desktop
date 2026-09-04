//go:build cgo && darwin

package audio

// macOS 录系统声音走 ScreenCaptureKit，不走 malgo——CoreAudio 根本不提供回环。
// C 与 Objective-C 那一侧在 sysaudio_darwin.h / .m，这里只做桥接与格式对接。
//
// 从 ScreenCaptureKit 出来的样本会喂进与 malgo 那条路**同一条** DSP 链
// （降混 → 重采样 → 分帧 → 电平），所以两轨在下游完全等价，录音会话那边看不出
// 它们来自不同的系统接口。

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework ScreenCaptureKit -framework CoreMedia -framework AudioToolbox -framework CoreGraphics
#include <stdlib.h>
#include "sysaudio_darwin.h"

// rc_start_bridge 的**定义**在 sysaudio_darwin.m 里，不在这里。
//
// 它要把下面那个 //export 函数当函数指针传进去，而导出函数的签名由 cgo 生成、
// 只在 _cgo_export.h 里可见。在这段前言里手写一份声明去对它，等于猜 cgo 的生成
// 结果——猜错一个 const 就是编译期 "conflicting types"，而且报在一个看不出所以然
// 的位置。让 .m 那边 include 生成的头文件，签名由 cgo 自己保证一致。
int rc_start_bridge(uintptr_t handle, int rate, int channels, void **out, char *err, int errlen);
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"

	"github.com/wt68/runcode/internal/recorder"
)

// sysAudioRate / sysAudioChannels 是向 ScreenCaptureKit 要的格式。
//
// 直接要 48 kHz 立体声而不是设备原生格式：这条路没有"设备"可言，格式由我们指定，
// 那就指定一个与 malgo 那条路对齐的。重采样到目标采样率仍走同一个重采样器——
// 48k → 16k 是整数倍，代价可以忽略，换来两条路的下游完全一致。
const (
	sysAudioRate     = 48000
	sysAudioChannels = 2
)

//export rcSysAudioTrampoline
func rcSysAudioTrampoline(handle C.uintptr_t, samples *C.float, frames, channels C.int) {
	h := cgo.Handle(handle)
	s, ok := h.Value().(*sysAudioStream)
	if !ok || s == nil {
		return
	}
	n := int(frames) * int(channels)
	if n <= 0 || samples == nil {
		return
	}
	// 不复制：这段内存在回调返回前有效，而下面 onSamples 会把它转成自己的缓冲。
	buf := unsafe.Slice((*float32)(unsafe.Pointer(samples)), n)
	s.onSamples(buf)
}

// sysAudioStream 是一路 ScreenCaptureKit 采集，实现 recorder.Stream。
//
// DSP 各级缓冲挂在这里跨回调复用，理由与 malgo 那条路的 stream 相同：回调以约
// 100 Hz 触发，在里面分配是实时路径抖动的经典来源。这些字段只被音频回调一个
// goroutine 碰（ScreenCaptureKit 用的是单串行队列），不加锁。
type sysAudioStream struct {
	capture unsafe.Pointer
	handle  cgo.Handle
	format  recorder.Format
	res     *recorder.Resampler
	framer  *recorder.Framer

	onFrame func(recorder.Frame)
	onLevel func(float32)
	onError func(error)

	mbuf []float32
	ibuf []int16

	framePeak  float32
	levelPeak  float32
	levelCount int

	closeOnce sync.Once
}

func (s *sysAudioStream) Format() recorder.Format { return s.format }

func (s *sysAudioStream) Close() error {
	s.closeOnce.Do(func() {
		// 先停捕获再收尾：stop 内部会先摘掉回调，所以之后不会再有样本进来。
		C.rc_sysaudio_stop(s.capture)
		s.capture = nil
		// 把不足一帧的尾巴发出去，否则最后不到一帧的话会被丢掉。
		s.framer.Drain(s.emit)
		s.handle.Delete()
	})
	return nil
}

// onSamples 是交错 float32 进来之后的处理，与 malgo 那条路的 onData 后半段等价。
func (s *sysAudioStream) onSamples(interleaved []float32) {
	s.mbuf = recorder.DownmixToMono(interleaved, s.format.Channels, s.mbuf)
	mono := s.res.Process(s.mbuf)
	if len(mono) == 0 {
		return // 重采样器还在攒够一个输出点所需的输入
	}
	peak := recorder.PeakLevel(mono)
	if peak > s.framePeak {
		s.framePeak = peak
	}
	if peak > s.levelPeak {
		s.levelPeak = peak
	}
	s.levelCount += len(mono)
	if s.levelCount >= recorder.LevelSamples {
		if s.onLevel != nil {
			s.onLevel(s.levelPeak)
		}
		s.levelPeak, s.levelCount = 0, 0
	}
	s.ibuf = recorder.FloatToInt16(mono, s.ibuf)
	s.framer.Push(s.ibuf, s.emit)
}

func (s *sysAudioStream) emit(pcm []int16) {
	s.onFrame(recorder.Frame{
		Source: recorder.SourceLoopback,
		PCM:    pcm,
		Peak:   s.framePeak,
		Silent: recorder.IsSilent(pcm),
	})
	s.framePeak = 0
}

// openSystemAudio 启动一路系统音频捕获。backend_darwin.go 把它挂在 openLoopback 上。
func openSystemAudio(cfg recorder.OpenConfig) (recorder.Stream, error) {
	if cfg.OnFrame == nil {
		return nil, fmt.Errorf("OnFrame 不能为空")
	}
	if C.rc_sysaudio_available() == 0 {
		return nil, fmt.Errorf("录系统声音需要 macOS 13 或更高版本")
	}

	s := &sysAudioStream{
		format:  recorder.Format{SampleRate: sysAudioRate, Channels: sysAudioChannels},
		onFrame: cfg.OnFrame,
		onLevel: cfg.OnLevel,
		onError: cfg.OnError,
	}
	s.res = recorder.NewResampler(sysAudioRate, recorder.TargetSampleRate)
	s.framer = recorder.NewFramer(recorder.FrameSamples)
	// handle 要在 start 之前建好：回调可能在 start 返回之前就来了。
	s.handle = cgo.NewHandle(s)

	errbuf := (*C.char)(C.calloc(512, 1))
	defer C.free(unsafe.Pointer(errbuf))

	var capture unsafe.Pointer
	rc := C.rc_start_bridge(C.uintptr_t(s.handle), C.int(sysAudioRate), C.int(sysAudioChannels),
		&capture, errbuf, 512)
	if rc != 0 {
		s.handle.Delete()
		return nil, fmt.Errorf("%s", C.GoString(errbuf))
	}
	s.capture = capture
	return s, nil
}
