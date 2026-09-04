//go:build cgo && (windows || linux || darwin)

package audio

import (
	"github.com/gen2brain/malgo"
	"github.com/wt68/runcode/internal/recorder"
)

// backend 把三个平台不同的那点东西收在一起。
//
// 录麦克风三家一样，录**系统声音**则是三套完全不同的机制：Windows 的 WASAPI
// loopback 把输出端点当成一个可采集设备；Linux 的 PulseAudio 给每个输出端点配一个
// .monitor 采集源；macOS 的 CoreAudio 干脆不提供，要另走系统的屏幕/音频捕获接口。
//
// 抽成数据而不是接口，是因为差异恰好只有这五项，且都是「查表」性质的：谁也不需要
// 覆写 DSP 或设备生命周期，那些在 capture.go 里三家共用。
type backend struct {
	// label 是出现在错误信息里的平台名。
	label string

	// contextBackends 是初始化 miniaudio 时**显式**指定的后端列表。
	//
	// 不能留空让它自己挑：miniaudio 按内置顺序逐个尝试，而回环能不能录取决于挑中了
	// 哪个。Windows 上只有 WASAPI 支持 loopback，Linux 上只有 PulseAudio 提供
	// monitor 源——挑错的表现不是报错，是回环轨悄悄录不到东西。
	contextBackends []malgo.Backend

	// enumKind 是枚举该音源时要列的设备种类。Windows 的回环列的是**播放**端点，
	// 其余情况都是采集端点。
	enumKind func(recorder.Source) malgo.DeviceType

	// openKind 是打开该音源时要用的设备类型。只有 Windows 的回环用
	// malgo.Loopback，Linux 的 monitor 源本身就是普通采集设备。
	openKind func(recorder.Source) malgo.DeviceType

	// keep 判断一个枚举出来的设备属不属于该音源。Windows 靠 enumKind 就分开了，
	// 这里恒真；Linux 上麦克风与 monitor 混在同一张表里，只能按名字分。
	keep func(recorder.Source, string) bool

	// needsExplicitDefault 说明该音源在未指定设备时必须由我们挑一个，而不能交给
	// miniaudio 选"系统默认"。Linux 的回环必须如此：系统默认采集设备是麦克风。
	needsExplicitDefault func(recorder.Source) bool

	// loopbackSupported 说明本平台能不能录系统声音。false 时相关调用直接报错，
	// 而不是打开一个只有麦克风的"系统声音"轨——那种错法录出来的两轨内容一样，
	// 等用户听回放才会发现。
	loopbackSupported bool
}

// keepAll 是「枚举出来的都算」，给靠设备种类就能分开音源的平台用。
func keepAll(recorder.Source, string) bool { return true }

// neverExplicit 是「默认设备交给 miniaudio 挑」。
func neverExplicit(recorder.Source) bool { return false }
