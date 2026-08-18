//go:build !windows

// 非 Windows 平台的占位实现。
//
// 一期只发 Windows（见方案 §12）。macOS 的系统回环要走 ScreenCaptureKit 或引导
// 用户装 BlackHole 这类虚拟声卡，Linux 走 PulseAudio monitor 源——都不是把
// malgo 换个后端就能了事的，所以这里不假装支持，直接明确报错。
//
// 麦克风一路其实 malgo 在三个平台都能开，但只有麦克风的「会议纪要」录不到对方，
// 产品上没有意义，因此也不在这里悄悄提供半个功能。
package audio

import (
	"fmt"
	"runtime"

	"github.com/wt68/runcode/internal/recorder"
)

// Capturer 在非 Windows 平台上不可用。
type Capturer struct{}

// New 在非 Windows 平台上直接报错。
func New() (*Capturer, error) {
	return nil, fmt.Errorf("录音纪要目前只支持 Windows（当前 %s）", runtime.GOOS)
}

// Close 无事可做。
func (c *Capturer) Close() error { return nil }

// Devices 在非 Windows 平台上不可用。
func (c *Capturer) Devices(recorder.Source) ([]recorder.DeviceInfo, error) {
	return nil, fmt.Errorf("录音纪要目前只支持 Windows（当前 %s）", runtime.GOOS)
}

// Open 在非 Windows 平台上不可用。
func (c *Capturer) Open(recorder.OpenConfig) (recorder.Stream, error) {
	return nil, fmt.Errorf("录音纪要目前只支持 Windows（当前 %s）", runtime.GOOS)
}
