//go:build cgo && windows

package audio

import (
	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// Windows：WASAPI loopback。
//
// 回环在这里是一等公民——WASAPI 把「录下某个输出端点正在播的东西」做成了一种设备
// 类型（malgo.Loopback），所以枚举的是**播放**端点（扬声器/耳机），不是麦克风。
// 这一点每次读代码都会想反一次。
func platformBackend() backend {
	return backend{
		label: "Windows",
		// 只挂 WASAPI：miniaudio 默认会依次尝试 WASAPI → DirectSound → WinMM，
		// 而只有 WASAPI 支持 loopback。悄悄回落的后果是回环轨打不开，报错却只说
		// 「设备不可用」，非常难查。
		contextBackends: []malgo.Backend{malgo.BackendWasapi},
		enumKind: func(src recorder.Source) malgo.DeviceType {
			if src == recorder.SourceLoopback {
				return malgo.Playback
			}
			return malgo.Capture
		},
		openKind: func(src recorder.Source) malgo.DeviceType {
			if src == recorder.SourceLoopback {
				return malgo.Loopback
			}
			return malgo.Capture
		},
		keep:                 keepAll,
		needsExplicitDefault: neverExplicit,
		loopbackSupported:    true,
	}
}
