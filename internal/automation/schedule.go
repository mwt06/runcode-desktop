package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // embed IANA zones for Windows hosts
)

// ValidateSchedule rejects unknown modes, bad calendar fields, and contradictory extras.
func ValidateSchedule(s Schedule) error {
	switch s.Mode {
	case ModeWeekly, ModeWeekdays, ModeDaily, ModeEveryHours, ModeEveryMinutes, ModeMonthly, ModeOnce:
	default:
		return fmt.Errorf("automation: unknown schedule mode %q", s.Mode)
	}
	if strings.TrimSpace(s.TimeZone) == "" {
		return fmt.Errorf("automation: time zone required")
	}
	if _, err := time.LoadLocation(s.TimeZone); err != nil {
		return fmt.Errorf("automation: unknown time zone %q", s.TimeZone)
	}
	if _, err := parseDate(s.StartDate); err != nil {
		return fmt.Errorf("automation: invalid startDate: %w", err)
	}
	h, m, sec, err := parseTimeOfDay(s.TimeOfDay)
	if err != nil {
		return err
	}
	_ = h
	_ = m
	_ = sec

	switch s.Mode {
	case ModeOnce:
		if s.Interval != 0 {
			return fmt.Errorf("automation: once schedule must omit interval")
		}
		if len(s.Weekdays) > 0 || s.MonthDay != 0 {
			return fmt.Errorf("automation: once schedule has contradictory fields")
		}
		loc, _ := time.LoadLocation(s.TimeZone)
		if _, ok := localCivil(loc, s.StartDate, h, m, sec); !ok {
			return fmt.Errorf("automation: local time %s %s does not exist in %s", s.StartDate, s.TimeOfDay, s.TimeZone)
		}
	case ModeWeekly:
		if err := validateInterval(s.Interval); err != nil {
			return err
		}
		if len(normalizeWeekdays(s.Weekdays)) == 0 {
			return fmt.Errorf("automation: weekly requires weekdays")
		}
		if s.MonthDay != 0 {
			return fmt.Errorf("automation: weekly must not set monthDay")
		}
	case ModeWeekdays:
		if err := validateInterval(s.Interval); err != nil {
			return err
		}
		if len(s.Weekdays) > 0 || s.MonthDay != 0 {
			return fmt.Errorf("automation: weekdays mode has contradictory fields")
		}
	case ModeDaily, ModeEveryHours, ModeEveryMinutes:
		if err := validateInterval(s.Interval); err != nil {
			return err
		}
		if len(s.Weekdays) > 0 || s.MonthDay != 0 {
			return fmt.Errorf("automation: mode %s has contradictory fields", s.Mode)
		}
	case ModeMonthly:
		if err := validateInterval(s.Interval); err != nil {
			return err
		}
		if s.MonthDay < 1 || s.MonthDay > 31 {
			return fmt.Errorf("automation: monthDay out of range")
		}
		if len(s.Weekdays) > 0 {
			return fmt.Errorf("automation: monthly must not set weekdays")
		}
	}
	return nil
}

func validateInterval(n int) error {
	if n < 1 || n > 9999 {
		return fmt.Errorf("automation: interval must be 1..9999")
	}
	return nil
}

// NextOccurrence returns the first occurrence strictly after after (UTC).
func NextOccurrence(s Schedule, after time.Time) (time.Time, bool, error) {
	if err := ValidateSchedule(s); err != nil {
		return time.Time{}, false, err
	}
	after = after.UTC()
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		return time.Time{}, false, err
	}
	h, mi, sec, err := parseTimeOfDay(s.TimeOfDay)
	if err != nil {
		return time.Time{}, false, err
	}

	switch s.Mode {
	case ModeOnce:
		occ, ok := localCivil(loc, s.StartDate, h, mi, sec)
		if !ok || !occ.After(after) {
			return time.Time{}, false, nil
		}
		return occ, true, nil
	case ModeEveryMinutes:
		return nextFixedInterval(s, after, loc, h, mi, sec, time.Minute)
	case ModeEveryHours:
		return nextFixedInterval(s, after, loc, h, mi, sec, time.Hour)
	case ModeDaily:
		return nextDaily(s, after, loc, h, mi, sec)
	case ModeWeekly:
		return nextWeekly(s, after, loc, h, mi, sec, normalizeWeekdays(s.Weekdays))
	case ModeWeekdays:
		return nextWeekly(s, after, loc, h, mi, sec, []int{1, 2, 3, 4, 5})
	case ModeMonthly:
		return nextMonthly(s, after, loc, h, mi, sec)
	default:
		return time.Time{}, false, fmt.Errorf("automation: unsupported mode")
	}
}

// CountOccurrences counts occurrences with fromExclusive < t <= through.
// Interval modes use arithmetic; others iterate with a hard safety bound.
func CountOccurrences(s Schedule, fromExclusive, through time.Time) (int64, error) {
	if err := ValidateSchedule(s); err != nil {
		return 0, err
	}
	fromExclusive = fromExclusive.UTC()
	through = through.UTC()
	if !through.After(fromExclusive) {
		return 0, nil
	}
	loc, err := time.LoadLocation(s.TimeZone)
	if err != nil {
		return 0, err
	}
	h, mi, sec, err := parseTimeOfDay(s.TimeOfDay)
	if err != nil {
		return 0, err
	}

	switch s.Mode {
	case ModeEveryMinutes, ModeEveryHours:
		step := time.Minute
		if s.Mode == ModeEveryHours {
			step = time.Hour
		}
		anchor, ok := localCivil(loc, s.StartDate, h, mi, sec)
		if !ok {
			return 0, fmt.Errorf("automation: invalid anchor")
		}
		n := s.Interval
		delta := step * time.Duration(n)
		// First k with anchor+k*delta > fromExclusive and <= through.
		var firstK int64
		if !anchor.After(fromExclusive) {
			elapsed := fromExclusive.Sub(anchor)
			firstK = int64(elapsed/delta) + 1
		}
		lastOcc := through
		if lastOcc.Before(anchor) {
			return 0, nil
		}
		lastK := int64(lastOcc.Sub(anchor) / delta)
		if lastK < firstK {
			return 0, nil
		}
		return lastK - firstK + 1, nil
	default:
		var count int64
		cur := fromExclusive
		for i := 0; i < 2_000_000; i++ {
			next, ok, err := NextOccurrence(s, cur)
			if err != nil {
				return 0, err
			}
			if !ok || next.After(through) {
				break
			}
			count++
			cur = next
		}
		return count, nil
	}
}

func nextFixedInterval(s Schedule, after time.Time, loc *time.Location, h, mi, sec int, unit time.Duration) (time.Time, bool, error) {
	anchor, ok := localCivil(loc, s.StartDate, h, mi, sec)
	if !ok {
		return time.Time{}, false, fmt.Errorf("automation: invalid interval anchor")
	}
	delta := unit * time.Duration(s.Interval)
	if after.Before(anchor) {
		return anchor, true, nil
	}
	elapsed := after.Sub(anchor)
	k := elapsed/delta + 1
	return anchor.Add(k * delta), true, nil
}

func nextDaily(s Schedule, after time.Time, loc *time.Location, h, mi, sec int) (time.Time, bool, error) {
	start, err := parseDate(s.StartDate)
	if err != nil {
		return time.Time{}, false, err
	}
	// Iterate calendar days from start, stepping Interval.
	for i := 0; i < 40000; i++ {
		d := start.AddDate(0, 0, i*s.Interval)
		date := fmt.Sprintf("%04d-%02d-%02d", d.Year(), d.Month(), d.Day())
		occ, ok := localCivil(loc, date, h, mi, sec)
		if !ok {
			continue // DST gap
		}
		if occ.After(after) {
			return occ, true, nil
		}
	}
	return time.Time{}, false, nil
}

func nextWeekly(s Schedule, after time.Time, loc *time.Location, h, mi, sec int, days []int) (time.Time, bool, error) {
	start, err := parseDate(s.StartDate)
	if err != nil {
		return time.Time{}, false, err
	}
	week0Mon := mondayOf(start)
	daySet := map[int]struct{}{}
	for _, d := range days {
		daySet[d] = struct{}{}
	}
	// Scan forward day by day from startDate (inclusive candidates).
	for i := 0; i < 40000; i++ {
		d := start.AddDate(0, 0, i)
		if d.Before(start) {
			continue
		}
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 7
		}
		if _, ok := daySet[wd]; !ok {
			continue
		}
		mon := mondayOf(d)
		weekDiff := int(mon.Sub(week0Mon).Hours() / 24 / 7)
		if weekDiff < 0 || weekDiff%s.Interval != 0 {
			continue
		}
		date := fmt.Sprintf("%04d-%02d-%02d", d.Year(), d.Month(), d.Day())
		occ, ok := localCivil(loc, date, h, mi, sec)
		if !ok {
			continue
		}
		if occ.After(after) {
			return occ, true, nil
		}
	}
	return time.Time{}, false, nil
}

func nextMonthly(s Schedule, after time.Time, loc *time.Location, h, mi, sec int) (time.Time, bool, error) {
	start, err := parseDate(s.StartDate)
	if err != nil {
		return time.Time{}, false, err
	}
	y, m := start.Year(), int(start.Month())
	for i := 0; i < 12000; i++ {
		yy := y
		mm := m + i*s.Interval
		for mm > 12 {
			mm -= 12
			yy++
		}
		// Skip months without MonthDay; keep month anchor (do not shift to month end).
		if s.MonthDay > daysInMonth(yy, time.Month(mm)) {
			continue
		}
		date := fmt.Sprintf("%04d-%02d-%02d", yy, mm, s.MonthDay)
		if date < s.StartDate {
			continue
		}
		occ, ok := localCivil(loc, date, h, mi, sec)
		if !ok {
			continue
		}
		if occ.After(after) {
			return occ, true, nil
		}
	}
	return time.Time{}, false, nil
}

func parseDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad date %q", s)
	}
	return t, nil
}

func parseTimeOfDay(s string) (h, m, sec int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("automation: timeOfDay must be HH:mm:ss")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("automation: invalid timeOfDay")
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return 0, 0, 0, fmt.Errorf("automation: timeOfDay out of range")
	}
	return h, m, sec, nil
}

// localCivil builds the UTC instant for a civil local wall time.
// On DST fall-back ambiguity, picks the earlier UTC. On spring gap, ok=false.
func localCivil(loc *time.Location, date string, h, mi, sec int) (time.Time, bool) {
	y, mo, d, err := splitDate(date)
	if err != nil {
		return time.Time{}, false
	}
	// First try: construct with timezone; detect gap by round-trip.
	t := time.Date(y, time.Month(mo), d, h, mi, sec, 0, loc)
	cy, cmo, cd := t.In(loc).Date()
	ch, cmi, csec := t.In(loc).Clock()
	if cy != y || int(cmo) != mo || cd != d || ch != h || cmi != mi || csec != sec {
		// Likely spring-forward gap (Go rolled forward).
		return time.Time{}, false
	}
	// Fall-back: there may be two UTCs. Prefer earlier by checking −1h.
	earlier := t.Add(-time.Hour)
	ey, emo, ed := earlier.In(loc).Date()
	eh, emi, esec := earlier.In(loc).Clock()
	if ey == y && int(emo) == mo && ed == d && eh == h && emi == mi && esec == sec {
		return earlier.UTC(), true
	}
	return t.UTC(), true
}

func splitDate(date string) (y, m, d int, err error) {
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("bad date")
	}
	y, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	d, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, fmt.Errorf("bad date")
	}
	return y, m, d, nil
}

func mondayOf(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(wd-1), 0, 0, 0, 0, time.UTC)
}

func daysInMonth(year int, m time.Month) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func normalizeWeekdays(in []int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, d := range in {
		if d < 1 || d > 7 {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}
