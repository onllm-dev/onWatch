package menubar

import (
	"testing"
	"time"
)

func utc(y int, m time.Month, d, h, min, s int) time.Time {
	return time.Date(y, m, d, h, min, s, 0, time.UTC)
}

// The DeepSeek peak schedule is peak 01:00-04:00 and 06:00-10:00 UTC Monday
// through Friday. Everything else, including all of Saturday and Sunday UTC,
// is off-peak. Windows are half-open: [01:00, 04:00) and [06:00, 10:00).
func TestDeepSeekPeakScheduleBoundaries(t *testing.T) {
	t.Parallel()

	schedule := DeepSeekPeakSchedule
	// 2026-10-22 is a Thursday.
	cases := []struct {
		name      string
		now       time.Time
		wantPeak  bool
		wantUntil time.Time
	}{
		{"exactly 01:00 starts peak", utc(2026, 10, 22, 1, 0, 0), true, utc(2026, 10, 22, 4, 0, 0)},
		{"03:59:59 is still peak", utc(2026, 10, 22, 3, 59, 59), true, utc(2026, 10, 22, 4, 0, 0)},
		{"04:00 is off-peak in the gap", utc(2026, 10, 22, 4, 0, 0), false, utc(2026, 10, 22, 6, 0, 0)},
		{"05:59 is off-peak in the gap", utc(2026, 10, 22, 5, 59, 0), false, utc(2026, 10, 22, 6, 0, 0)},
		{"exactly 06:00 starts peak", utc(2026, 10, 22, 6, 0, 0), true, utc(2026, 10, 22, 10, 0, 0)},
		{"exactly 10:00 ends peak", utc(2026, 10, 22, 10, 0, 0), false, utc(2026, 10, 23, 1, 0, 0)},
		{"midnight is off-peak", utc(2026, 10, 22, 0, 0, 0), false, utc(2026, 10, 22, 1, 0, 0)},
		{"saturday is off-peak all day", utc(2026, 10, 24, 7, 0, 0), false, utc(2026, 10, 26, 1, 0, 0)},
		{"sunday 23:59 is off-peak", utc(2026, 10, 25, 23, 59, 0), false, utc(2026, 10, 26, 1, 0, 0)},
		{"monday 01:00 starts peak", utc(2026, 10, 26, 1, 0, 0), true, utc(2026, 10, 26, 4, 0, 0)},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := schedule.StateAt(tc.now)
			if state.Peak != tc.wantPeak {
				t.Fatalf("StateAt(%s).Peak = %v, want %v", tc.now.Format(time.RFC3339), state.Peak, tc.wantPeak)
			}
			if !state.Until.Equal(tc.wantUntil) {
				t.Fatalf("StateAt(%s).Until = %s, want %s", tc.now.Format(time.RFC3339), state.Until.Format(time.RFC3339), tc.wantUntil.Format(time.RFC3339))
			}
			if state.Remaining != tc.wantUntil.Sub(tc.now) {
				t.Fatalf("StateAt(%s).Remaining = %s, want %s", tc.now.Format(time.RFC3339), state.Remaining, tc.wantUntil.Sub(tc.now))
			}
		})
	}
}

func TestDeepSeekPeakScheduleWeekendWrap(t *testing.T) {
	t.Parallel()

	// Friday 10:00 is the start of the weekend off-peak block; the next peak
	// is Monday 01:00, 63 hours later.
	friday := utc(2026, 10, 23, 10, 0, 0)
	state := DeepSeekPeakSchedule.StateAt(friday)
	if state.Peak {
		t.Fatal("Friday 10:00 must be off-peak")
	}
	if want := 63 * time.Hour; state.Remaining != want {
		t.Fatalf("Friday 10:00 remaining = %s, want %s", state.Remaining, want)
	}
	if want := utc(2026, 10, 26, 1, 0, 0); !state.Until.Equal(want) {
		t.Fatalf("Friday 10:00 next transition = %s, want %s", state.Until, want)
	}

	// Saturday noon: 37 hours to Monday 01:00.
	saturday := DeepSeekPeakSchedule.StateAt(utc(2026, 10, 24, 12, 0, 0))
	if want := 37 * time.Hour; saturday.Remaining != want {
		t.Fatalf("Saturday noon remaining = %s, want %s", saturday.Remaining, want)
	}

	// Sunday 23:59: 1h 1m to Monday 01:00, with second precision.
	sunday := DeepSeekPeakSchedule.StateAt(utc(2026, 10, 25, 23, 59, 0))
	if want := time.Hour + time.Minute; sunday.Remaining != want {
		t.Fatalf("Sunday 23:59 remaining = %s, want %s", sunday.Remaining, want)
	}
}

func TestDeepSeekPeakScheduleCountdownValues(t *testing.T) {
	t.Parallel()

	// Thursday 07:50 is inside the second window: 2h 10m to 10:00.
	state := DeepSeekPeakSchedule.StateAt(utc(2026, 10, 22, 7, 50, 0))
	if !state.Peak {
		t.Fatal("Thursday 07:50 must be peak")
	}
	if want := 2*time.Hour + 10*time.Minute; state.Remaining != want {
		t.Fatalf("Thursday 07:50 remaining = %s, want %s", state.Remaining, want)
	}

	// Thursday 10:00 to Friday 01:00 is 15 hours.
	afterPeak := DeepSeekPeakSchedule.StateAt(utc(2026, 10, 22, 10, 0, 0))
	if want := 15 * time.Hour; afterPeak.Remaining != want {
		t.Fatalf("Thursday 10:00 remaining = %s, want %s", afterPeak.Remaining, want)
	}

	// Fractional seconds survive: 03:59:59.5 leaves 500ms.
	fractional := DeepSeekPeakSchedule.StateAt(time.Date(2026, 10, 22, 3, 59, 59, 500_000_000, time.UTC))
	if want := 500 * time.Millisecond; fractional.Remaining != want {
		t.Fatalf("03:59:59.5 remaining = %s, want %s", fractional.Remaining, want)
	}
}

func TestDeepSeekPeakScheduleEmpty(t *testing.T) {
	t.Parallel()

	empty := PeakSchedule{Timezone: "UTC"}
	state := empty.StateAt(utc(2026, 10, 22, 2, 0, 0))
	if state.Peak || !state.Until.IsZero() || state.Remaining != 0 {
		t.Fatalf("empty schedule state = %+v, want zero", state)
	}
}
