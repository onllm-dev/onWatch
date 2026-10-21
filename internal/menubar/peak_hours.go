package menubar

import (
	"strings"
	"time"
)

// PeakWindow is a half-open window [StartMinute, EndMinute) counted from local
// midnight. Minutes may exceed 1440 to cross midnight.
type PeakWindow struct {
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// PeakSchedule describes recurring peak windows on weekdays in one timezone.
// StateAt uses exact UTC math for the schedule in use (UTC); the timezone
// field keeps the shape generic for future schedules.
type PeakSchedule struct {
	Timezone string         `json:"timezone"`
	Weekdays []time.Weekday `json:"weekdays"`
	Windows  []PeakWindow   `json:"windows"`
}

// PeakState is the schedule evaluated at one instant. Until is the next
// transition (the end of the current peak window, or the start of the next
// one) and Remaining is the exact duration until it.
type PeakState struct {
	Peak      bool
	Until     time.Time
	Remaining time.Duration
}

// DeepSeekPeakSchedule is DeepSeek's published peak schedule: 01:00-04:00 and
// 06:00-10:00 UTC, Monday through Friday. Everything else, including all of
// Saturday and Sunday UTC, is off-peak.
var DeepSeekPeakSchedule = PeakSchedule{
	Timezone: "UTC",
	Weekdays: []time.Weekday{
		time.Monday,
		time.Tuesday,
		time.Wednesday,
		time.Thursday,
		time.Friday,
	},
	Windows: []PeakWindow{
		{StartMinute: 1 * 60, EndMinute: 4 * 60},
		{StartMinute: 6 * 60, EndMinute: 10 * 60},
	},
}

// DeepSeekPeakScheduleText is the full human-readable schedule shown in the
// chip tooltip.
const DeepSeekPeakScheduleText = "DeepSeek peak 01:00-04:00 and 06:00-10:00 UTC, Mon-Fri; off-peak all other hours"

// DeepSeekPeakModelNote names the models the schedule applies to when the
// provider's card covers more than those models.
const DeepSeekPeakModelNote = "DeepSeek V4.1 Flash / V4 Pro"

// DeepSeekPeakPricing is the peak price the user supplied. The source does not
// name the units, so the UI shows the figure as given.
const DeepSeekPeakPricing = "peak $0.30 / $1.20"

// ProviderPeak carries the peak schedule and the extra context a provider card
// shows in its chip tooltip.
type ProviderPeak struct {
	PeakSchedule
	Provider     string `json:"provider"`
	ScheduleText string `json:"schedule_text"`
	ModelNote    string `json:"model_note,omitempty"`
	Pricing      string `json:"pricing,omitempty"`
}

// NewDeepSeekPeak builds the peak metadata a Command Code or OpenCode card
// shows for the DeepSeek models it serves.
func NewDeepSeekPeak(provider, modelNote, pricing string) *ProviderPeak {
	return &ProviderPeak{
		PeakSchedule: DeepSeekPeakSchedule,
		Provider:     provider,
		ScheduleText: DeepSeekPeakScheduleText,
		ModelNote:    modelNote,
		Pricing:      pricing,
	}
}

func (s PeakSchedule) includesWeekday(day time.Weekday) bool {
	for _, weekday := range s.Weekdays {
		if weekday == day {
			return true
		}
	}
	return false
}

// StateAt evaluates the schedule at now. Windows are half-open, so exactly
// 01:00 is peak and exactly 04:00 is off-peak. The search spans nine days so a
// Friday-evening or weekend instant always finds the next Monday window.
func (s PeakSchedule) StateAt(now time.Time) PeakState {
	if len(s.Windows) == 0 || len(s.Weekdays) == 0 {
		return PeakState{}
	}
	loc := time.UTC
	if s.Timezone != "" && !strings.EqualFold(s.Timezone, "UTC") {
		loaded, err := time.LoadLocation(s.Timezone)
		if err != nil {
			return PeakState{}
		}
		loc = loaded
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	var nextStart time.Time
	for offset := 0; offset < 9; offset++ {
		day := today.AddDate(0, 0, offset)
		if !s.includesWeekday(day.Weekday()) {
			continue
		}
		for _, window := range s.Windows {
			if window.EndMinute <= window.StartMinute {
				continue
			}
			start := time.Date(day.Year(), day.Month(), day.Day(), window.StartMinute/60, window.StartMinute%60, 0, 0, loc)
			end := time.Date(day.Year(), day.Month(), day.Day(), window.EndMinute/60, window.EndMinute%60, 0, 0, loc)
			if !now.Before(start) && now.Before(end) {
				return PeakState{Peak: true, Until: end, Remaining: end.Sub(now)}
			}
			if start.After(now) && nextStart.IsZero() {
				nextStart = start
			}
		}
	}
	if nextStart.IsZero() {
		return PeakState{Peak: false}
	}
	return PeakState{Peak: false, Until: nextStart, Remaining: nextStart.Sub(now)}
}
