//go:build cgo && darwin

package audio

import (
	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// macOS：只有麦克风。
//
// CoreAudio **不提供**任何形式的系统音频回环——这不是 miniaudio 的缺失，是系统本身
// 就没这个接口。要录系统声音只有两条路：让用户装 BlackHole 这类虚拟声卡（要装内核
// 扩展、还要改声音输出设备，出问题也难排查），或者走 ScreenCaptureKit / Core Audio
// Taps 那套系统接口（需要 Objective-C 桥接与一次「屏幕录制」授权）。
//
// 在那之前，回环这一路明确报错而不是静默降级：录出一轨麦克风冒充系统声音，要等
// 用户听回放才会发现，比开不了糟得多。
func platformBackend() backend {
	return backend{
		label:                "macOS",
		contextBackends:      []malgo.Backend{malgo.BackendCoreaudio},
		enumKind:             func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		openKind:             func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		keep:                 keepAll,
		needsExplicitDefault: neverExplicit,
		loopbackSupported:    false,
	}
}
