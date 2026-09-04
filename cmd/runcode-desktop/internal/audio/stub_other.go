//go:build !cgo || (!windows && !linux && !darwin)

// 占位实现：三大桌面平台之外，**以及关掉 cgo 时**。
//
// 采集走 malgo（miniaudio 的 cgo 绑定），CGO_ENABLED=0 下它一个符号都没有。没有这条
// !cgo 分支的话，关掉 cgo 的构建不是退化成"没有录音"，而是整包编译失败——而
// CGO_ENABLED=0 正是各处做快速语法检查时的常用配置。
//
// 这里不假装支持，直接明确报错——录音入口会因此置灰，其余功能照常。
package audio

import (
	"fmt"
	"runtime"

	"github.com/wt68/runcode/internal/recorder"
)

// Capturer 在这些平台上不可用。
type Capturer struct{}

// New 直接报错。
func New() (*Capturer, error) {
	return nil, fmt.Errorf("录音纪要不支持当前系统（%s）", runtime.GOOS)
}

// Close 无事可做。
func (c *Capturer) Close() error { return nil }

// Devices 不可用。
func (c *Capturer) Devices(recorder.Source) ([]recorder.DeviceInfo, error) {
	return nil, fmt.Errorf("录音纪要不支持当前系统（%s）", runtime.GOOS)
}

// Open 不可用。
func (c *Capturer) Open(recorder.OpenConfig) (recorder.Stream, error) {
	return nil, fmt.Errorf("录音纪要不支持当前系统（%s）", runtime.GOOS)
}
