package mail

import "time"

// Clock 抽象墙上时钟，供 dispatcher 单测推进；不依赖 automation.Clock。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// SystemClock 是生产时钟。
type SystemClock struct{}

// Now 返回 UTC 当前时间。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// After 等待指定时长。
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
