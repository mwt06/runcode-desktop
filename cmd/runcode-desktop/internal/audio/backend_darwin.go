//go:build cgo && darwin

package audio

import (
	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// macOS：麦克风走 CoreAudio，系统声音走 ScreenCaptureKit。
//
// CoreAudio **不提供**任何形式的回环——这不是 miniaudio 的缺失，是系统本身就没这个
// 接口。所以这边的回环整个绕开 malgo，改用 ScreenCaptureKit（见 sysaudio_darwin.m）：
// 它本是屏幕录制接口，但同时能捕获系统音频，且不需要用户装虚拟声卡，代价是需要
// macOS 13 以上和一次「屏幕录制」授权。
//
// 另一条路是引导用户装 BlackHole 这类虚拟声卡。没选它：要装内核扩展级别的东西、
// 还要去声音设置里改输出设备，装完出问题也无从排查。
func platformBackend() backend {
	return backend{
		label:                "macOS",
		contextBackends:      []malgo.Backend{malgo.BackendCoreaudio},
		enumKind:             func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		openKind:             func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		keep:                 keepAll,
		needsExplicitDefault: neverExplicit,
		loopbackSupported:    true,
		openLoopback:         openSystemAudio,
		loopbackDevices: func() []recorder.DeviceInfo {
			// ScreenCaptureKit 捕获的是整机输出，没有"选哪个设备"这回事。给一条
			// 固定项，好让界面上的下拉框不是空的——空列表在那边会被当成"没有可用
			// 设备"而把录音入口置灰。
			return []recorder.DeviceInfo{{
				ID:        "system",
				Name:      "系统声音",
				IsDefault: true,
			}}
		},
	}
}
