package mail

import (
	"fmt"
	"strings"
	"time"
)

// ParseLocalSchedule 将任务时区下的本地墙钟解析为唯一 UTC。
// 夏令时不存在的本地时刻拒绝（要求用户确认）；回拨重叠时取较早 UTC。
func ParseLocalSchedule(local, timeZone string) (time.Time, error) {
	local = strings.TrimSpace(local)
	timeZone = strings.TrimSpace(timeZone)
	if local == "" || timeZone == "" {
		return time.Time{}, fmt.Errorf("%w: empty schedule", ErrInvalid)
	}
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: unknown timezone", ErrInvalid)
	}
	norm := strings.ReplaceAll(local, " ", "T")
	layouts := []string{"2006-01-02T15:04:05", "2006-01-02T15:04"}
	var parsed time.Time
	var parseErr error
	used := ""
	for _, layout := range layouts {
		parsed, parseErr = time.ParseInLocation(layout, norm, loc)
		if parseErr == nil {
			used = layout
			break
		}
	}
	if parseErr != nil {
		return time.Time{}, fmt.Errorf("%w: bad local time", ErrInvalid)
	}
	// 构造「字面」本地时刻；若与解析结果墙钟不一致，说明落入 DST 空洞被归一化。
	var y, day, hour, minute, sec int
	var mon time.Month
	switch used {
	case "2006-01-02T15:04:05":
		var err2 error
		var t struct {
			Y, D, H, M, S int
			Mon           time.Month
		}
		_, err2 = fmt.Sscanf(norm, "%d-%d-%dT%d:%d:%d", &t.Y, &t.Mon, &t.D, &t.H, &t.M, &t.S)
		if err2 != nil {
			return time.Time{}, fmt.Errorf("%w: bad local time", ErrInvalid)
		}
		y, mon, day, hour, minute, sec = t.Y, t.Mon, t.D, t.H, t.M, t.S
	default:
		var err2 error
		var t struct {
			Y, D, H, M int
			Mon        time.Month
		}
		_, err2 = fmt.Sscanf(norm, "%d-%d-%dT%d:%d", &t.Y, &t.Mon, &t.D, &t.H, &t.M)
		if err2 != nil {
			return time.Time{}, fmt.Errorf("%w: bad local time", ErrInvalid)
		}
		y, mon, day, hour, minute, sec = t.Y, t.Mon, t.D, t.H, t.M, 0
	}
	literal := time.Date(y, mon, day, hour, minute, sec, 0, loc)
	if literal.In(loc).Format("2006-01-02T15:04:05") != fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", y, mon, day, hour, minute, sec) {
		return time.Time{}, fmt.Errorf("%w: local time does not exist in timezone", ErrInvalid)
	}
	// 重叠小时：Date 与 ParseInLocation 可能不同；取较早 UTC。
	a, b := parsed.UTC(), literal.UTC()
	if a.After(b) {
		return b, nil
	}
	return a, nil
}

// WithinLateWindow 判断 now 是否仍在 scheduledAt 起 LateWindow 闭区间内（含边界）。
// 即 now ∈ [scheduledAt, scheduledAt+10m]；早于计划时刻返回 false（尚未到期）。
func WithinLateWindow(scheduledAt, now time.Time) bool {
	s := scheduledAt.UTC()
	n := now.UTC()
	if n.Before(s) {
		return false
	}
	deadline := s.Add(LateWindow)
	return !n.After(deadline)
}

// DeliveryDisposition 根据计划时刻与当前时间决定投递处置。
type DeliveryDisposition string

// 投递处置常量。
const (
	DispositionWait    DeliveryDisposition = "wait"
	DispositionDeliver DeliveryDisposition = "deliver"
	DispositionMissed  DeliveryDisposition = "missed"
)

// DecideDelivery 对一次性计划给出 wait / deliver / missed。
func DecideDelivery(scheduledAt, now time.Time) DeliveryDisposition {
	s := scheduledAt.UTC()
	n := now.UTC()
	if n.Before(s) {
		return DispositionWait
	}
	if WithinLateWindow(s, n) {
		return DispositionDeliver
	}
	return DispositionMissed
}
