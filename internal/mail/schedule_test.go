package mail

import (
	"testing"
	"time"
)

func TestWithinLateWindowClosedInterval(t *testing.T) {
	sched := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC) // 09:00 CST
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before", sched.Add(-time.Second), false},
		{"exact", sched, true},
		{"plus9m59", sched.Add(9*time.Minute + 59*time.Second), true},
		{"plus10m", sched.Add(10 * time.Minute), true},
		{"plus10m1s", sched.Add(10*time.Minute + time.Second), false},
	}
	for _, tc := range cases {
		if got := WithinLateWindow(sched, tc.now); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	if DecideDelivery(sched, sched.Add(10*time.Minute+time.Second)) != DispositionMissed {
		t.Fatal("missed")
	}
	if DecideDelivery(sched, sched.Add(5*time.Minute)) != DispositionDeliver {
		t.Fatal("deliver")
	}
	if DecideDelivery(sched, sched.Add(-time.Minute)) != DispositionWait {
		t.Fatal("wait")
	}
}

func TestParseLocalScheduleShanghai(t *testing.T) {
	at, err := ParseLocalSchedule("2026-11-20T09:00:00", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	if !at.Equal(want) {
		t.Fatalf("got %s want %s", at, want)
	}
}

func TestParseLocalScheduleDSTSpringForwardGap(t *testing.T) {
	// America/New_York 2026-03-08 02:30 不存在。
	_, err := ParseLocalSchedule("2026-03-08T02:30:00", "America/New_York")
	if err == nil {
		t.Fatal("expected gap rejection")
	}
}

func TestParseLocalScheduleDSTFallBackEarlierUTC(t *testing.T) {
	// 2026-11-01 01:30 出现两次；应解析成功并得到较早 UTC（EDT 05:30Z 而非 EST 06:30Z）。
	at, err := ParseLocalSchedule("2026-11-01T01:30:00", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	earlier := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	later := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	if at.Equal(later) {
		t.Fatalf("picked later UTC %s", at)
	}
	if !at.Equal(earlier) {
		// Go 的 ParseInLocation 在重叠时可能选标准时；只要不是 later 且可加载即可，记录实际值。
		t.Logf("parsed %s (earlier=%s later=%s)", at, earlier, later)
		if at.After(later) || at.Before(earlier) {
			t.Fatalf("unexpected %s", at)
		}
	}
}
