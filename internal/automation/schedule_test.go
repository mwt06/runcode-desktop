package automation

import (
	"testing"
	"time"
)

func mustParseUTC(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return tt.UTC()
}

func daily(tz, start, tod string, n int) Schedule {
	return Schedule{Mode: ModeDaily, TimeZone: tz, StartDate: start, TimeOfDay: tod, Interval: n}
}

func TestValidateSchedule(t *testing.T) {
	ok := daily("Asia/Shanghai", "2026-11-18", "09:00:00", 1)
	if err := ValidateSchedule(ok); err != nil {
		t.Fatalf("valid daily: %v", err)
	}

	cases := []struct {
		name string
		s    Schedule
	}{
		{"unknown mode", Schedule{Mode: "custom", TimeZone: "UTC", StartDate: "2026-01-01", TimeOfDay: "00:00:00", Interval: 1}},
		{"empty tz", daily("", "2026-01-01", "00:00:00", 1)},
		{"unknown tz", daily("Not/AZone", "2026-01-01", "00:00:00", 1)},
		{"bad date", daily("UTC", "2026-13-01", "00:00:00", 1)},
		{"24:00:00", daily("UTC", "2026-01-01", "24:00:00", 1)},
		{"N=0", daily("UTC", "2026-01-01", "00:00:00", 0)},
		{"N negative", daily("UTC", "2026-01-01", "00:00:00", -1)},
		{"N=10000", daily("UTC", "2026-01-01", "00:00:00", 10000)},
		{"weekly empty weekdays", Schedule{Mode: ModeWeekly, TimeZone: "UTC", StartDate: "2026-01-05", TimeOfDay: "09:00:00", Interval: 1, Weekdays: nil}},
		{"monthly with weekdays", Schedule{Mode: ModeMonthly, TimeZone: "UTC", StartDate: "2026-01-31", TimeOfDay: "09:00:00", Interval: 1, MonthDay: 31, Weekdays: []int{1}}},
		{"once with interval", Schedule{Mode: ModeOnce, TimeZone: "UTC", StartDate: "2026-12-01", TimeOfDay: "09:00:00", Interval: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSchedule(tc.s); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestNextOccurrence(t *testing.T) {
	t.Run("daily strictly after", func(t *testing.T) {
		s := daily("Asia/Shanghai", "2026-11-18", "09:00:00", 1)
		after := mustParseUTC(t, "2026-11-18T01:00:00Z") // 09:00 CST
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-19T01:00:00Z")
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("every N calendar days", func(t *testing.T) {
		s := daily("UTC", "2026-11-01", "12:00:00", 3)
		after := mustParseUTC(t, "2026-11-01T12:00:00Z")
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-04T12:00:00Z")
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("weekly monday anchor multi weekday", func(t *testing.T) {
		// startDate 2026-11-18 is Wednesday; week Monday is 2026-11-16.
		s := Schedule{
			Mode: ModeWeekly, TimeZone: "UTC", StartDate: "2026-11-18", TimeOfDay: "10:00:00",
			Interval: 1, Weekdays: []int{3, 5}, // Wed, Fri
		}
		after := mustParseUTC(t, "2026-11-18T10:00:00Z")
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-20T10:00:00Z") // Fri
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("weekdays every N weeks skips weekend", func(t *testing.T) {
		s := Schedule{
			Mode: ModeWeekdays, TimeZone: "UTC", StartDate: "2026-11-16", // Mon
			TimeOfDay: "09:00:00", Interval: 1,
		}
		after := mustParseUTC(t, "2026-11-20T09:00:00Z") // Fri
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-23T09:00:00Z") // next Mon
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("monthly keeps month anchor skip short months", func(t *testing.T) {
		s := Schedule{
			Mode: ModeMonthly, TimeZone: "UTC", StartDate: "2026-01-31",
			TimeOfDay: "12:00:00", MonthDay: 31, Interval: 1,
		}
		after := mustParseUTC(t, "2026-01-31T12:00:00Z")
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		// Feb 2026 has no 31 → March 31
		want := mustParseUTC(t, "2026-03-31T12:00:00Z")
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("every N minutes fixed UTC anchor", func(t *testing.T) {
		s := Schedule{
			Mode: ModeEveryMinutes, TimeZone: "UTC", StartDate: "2026-11-18",
			TimeOfDay: "10:00:00", Interval: 15,
		}
		after := mustParseUTC(t, "2026-11-18T10:00:00Z")
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-18T10:15:00Z")
		if !next.Equal(want) {
			t.Fatalf("got %s want %s", next, want)
		}
	})

	t.Run("once expired", func(t *testing.T) {
		s := Schedule{
			Mode: ModeOnce, TimeZone: "UTC", StartDate: "2026-01-01", TimeOfDay: "00:00:00",
		}
		after := mustParseUTC(t, "2026-06-01T00:00:00Z")
		_, ok, err := NextOccurrence(s, after)
		if err != nil {
			t.Fatalf("err %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false")
		}
	})
}

func TestScheduleDST(t *testing.T) {
	t.Run("spring forward skip missing local", func(t *testing.T) {
		s := Schedule{
			Mode: ModeOnce, TimeZone: "America/New_York",
			StartDate: "2026-03-08", TimeOfDay: "02:30:00",
		}
		// 02:30 does not exist; once with invalid local should fail validation or NextOccurrence.
		err := ValidateSchedule(s)
		if err == nil {
			after := mustParseUTC(t, "2026-03-01T00:00:00Z")
			_, ok, nerr := NextOccurrence(s, after)
			if nerr == nil && ok {
				t.Fatalf("missing local time must not yield an occurrence")
			}
		}
	})

	t.Run("fall back earlier UTC only", func(t *testing.T) {
		s := Schedule{
			Mode: ModeDaily, TimeZone: "America/New_York",
			StartDate: "2026-11-01", TimeOfDay: "01:30:00", Interval: 1,
		}
		// Before the day: first occurrence of 2026-11-01 01:30 EST = 05:30Z (earlier), not 06:30Z.
		after := mustParseUTC(t, "2026-10-31T00:00:00Z")
		next, ok, err := NextOccurrence(s, after)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		want := mustParseUTC(t, "2026-11-01T05:30:00Z")
		if !next.Equal(want) {
			t.Fatalf("got %s want %s (must pick earlier UTC)", next, want)
		}
	})
}

func TestCountOccurrencesBounded(t *testing.T) {
	s := Schedule{
		Mode: ModeEveryMinutes, TimeZone: "UTC", StartDate: "2026-01-01",
		TimeOfDay: "00:00:00", Interval: 1,
	}
	from := mustParseUTC(t, "2026-01-01T00:00:00Z")
	through := from.Add(time.Duration(1_000_000) * time.Minute)
	n, err := CountOccurrences(s, from, through)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1_000_000 {
		t.Fatalf("count=%d want 1000000", n)
	}
}
