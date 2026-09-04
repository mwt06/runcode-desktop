//go:build cgo && linux

package audio

import (
	"strings"

	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// Linux：PulseAudio 的 monitor 源。
//
// 与 Windows 最大的不同是回环**不是**一种独立的设备类型：PulseAudio 给每个输出端点
// 自动配一个同名的 .monitor 采集源，录它就等于录下那个端点正在播的东西。所以这边
// 麦克风和系统声音都是普通采集设备，混在同一张表里，只能按名字分。
//
// 于是有两个坑，都写在下面的字段里：一是必须显式挑 monitor（系统默认采集设备是
// 麦克风，不挑就会把麦克风当系统声音录下来，两轨内容一样且完全不报错）；二是必须
// 显式指定 PulseAudio 后端（miniaudio 会先试 ALSA，而 ALSA 没有 monitor 这个概念）。
func platformBackend() backend {
	return backend{
		label: "Linux",
		// PulseAudio 优先，ALSA 兜底。留着 ALSA 是为了没跑 PulseAudio 的环境下
		// 麦克风仍然能录——那种情况下回环会因为找不到 monitor 设备而明确报错，
		// 比静默录出一轨麦克风好。
		contextBackends: []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa},
		// 两路都是采集设备。
		enumKind: func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		openKind: func(recorder.Source) malgo.DeviceType { return malgo.Capture },
		keep: func(src recorder.Source, name string) bool {
			mon := isMonitorSource(name)
			if src == recorder.SourceLoopback {
				return mon
			}
			// 麦克风那一路要把 monitor 排掉，否则设备下拉框里会混进一堆
			// 「XXX 的监视器」，用户选中之后录到的是系统声音而不是自己说话。
			return !mon
		},
		needsExplicitDefault: func(src recorder.Source) bool {
			return src == recorder.SourceLoopback
		},
		loopbackSupported: true,
	}
}

// isMonitorSource 判断一个 PulseAudio 采集设备是不是某个输出端点的监视源。
//
// 按名字判是 PulseAudio 自己的约定：monitor 源的名字以 .monitor 结尾
// （alsa_output.pci-0000_00_1f.3.analog-stereo.monitor）。设备描述里也常带
// 「Monitor of ...」/「... 的监视器」，但那一条随语言变，不能拿来判。
func isMonitorSource(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".monitor")
}
