package marketing

import (
	"errors"
	"slices"
	"time"
)

// ShanghaiCalendar is deliberately independent from the gateway host timezone.
var ShanghaiCalendar = time.FixedZone("Asia/Shanghai", 8*60*60)

func ParseCheckinDay(day string) (time.Time, error) {
	parsed, err := time.ParseInLocation("2006-01-02", day, ShanghaiCalendar)
	if err != nil || parsed.Format("2006-01-02") != day {
		return time.Time{}, errors.New("Invalid check-in date")
	}
	return parsed, nil
}

// CheckinMilestoneCount counts all qualifying cycles across uninterrupted
// segments. A caller must retain previously claimed ordinals: merging segments
// can reduce this mathematical count but must never revoke or replay a grant.
func CheckinMilestoneCount(days []string, milestone, cycle int) (int64, error) {
	if milestone <= 0 || cycle < 0 || (cycle > 0 && milestone > cycle) {
		return 0, errors.New("Invalid check-in milestone")
	}
	ordered := slices.Clone(days)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	var total int64
	streak := 0
	var previous time.Time
	for _, day := range ordered {
		date, err := ParseCheckinDay(day)
		if err != nil {
			return 0, err
		}
		if !previous.IsZero() && date.Equal(previous.AddDate(0, 0, 1)) {
			streak++
		} else {
			streak = 1
		}
		if streak >= milestone && (cycle == 0 || (streak-milestone)%cycle == 0) {
			if cycle == 0 {
				return 1, nil
			}
			total++
		}
		previous = date
	}
	return total, nil
}

// CheckinSegment returns the uninterrupted segment containing the changed day.
// Previously earned responsibilities stay attached to days in this segment;
// excess responsibilities after a merge must not offset a later disjoint run.
func CheckinSegment(days []string, target string) ([]string, error) {
	ordered := slices.Clone(days)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	index := slices.Index(ordered, target)
	if index < 0 {
		return nil, errors.New("Invalid check-in date")
	}
	parsed := make([]time.Time, len(ordered))
	for i, day := range ordered {
		var err error
		parsed[i], err = ParseCheckinDay(day)
		if err != nil {
			return nil, err
		}
	}
	start, end := index, index+1
	for start > 0 && parsed[start].Equal(parsed[start-1].AddDate(0, 0, 1)) {
		start--
	}
	for end < len(parsed) && parsed[end].Equal(parsed[end-1].AddDate(0, 0, 1)) {
		end++
	}
	return ordered[start:end], nil
}

func CheckinCurrentStreak(days []string, now time.Time) int {
	present := make(map[string]bool, len(days))
	for _, day := range days {
		present[day] = true
	}
	date := now.In(ShanghaiCalendar)
	if !present[date.Format("2006-01-02")] {
		date = date.AddDate(0, 0, -1)
	}
	count := 0
	for present[date.Format("2006-01-02")] {
		count++
		date = date.AddDate(0, 0, -1)
	}
	return count
}
