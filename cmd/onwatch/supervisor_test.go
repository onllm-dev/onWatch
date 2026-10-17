package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeDaemonRuns plays back exit codes (or start errors) for superviseLoop and
// records how long the loop waited between runs.
type fakeDaemonRuns struct {
	codes    []int
	startErr []error
	runFor   time.Duration
	now      time.Time
	starts   int
	sleeps   []time.Duration
	logs     []string
}

func (f *fakeDaemonRuns) deps() superviseDeps {
	return superviseDeps{
		start: func() (func() (int, error), error) {
			i := f.starts
			f.starts++
			if i < len(f.startErr) && f.startErr[i] != nil {
				return nil, f.startErr[i]
			}
			return func() (int, error) {
				f.now = f.now.Add(f.runFor)
				return f.codes[i], nil
			}, nil
		},
		sleep: func(d time.Duration) { f.sleeps = append(f.sleeps, d); f.now = f.now.Add(d) },
		now:   func() time.Time { return f.now },
		logf:  func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) },
	}
}

func TestSuperviseLoopLeavesACleanExitStopped(t *testing.T) {
	f := &fakeDaemonRuns{codes: []int{0}, now: time.Unix(0, 0)}
	if err := superviseLoop(f.deps()); err != nil {
		t.Fatalf("superviseLoop: %v", err)
	}
	if f.starts != 1 || len(f.sleeps) != 0 {
		t.Fatalf("starts=%d sleeps=%v, want one run and no restart", f.starts, f.sleeps)
	}
}

func TestSuperviseLoopRestartsACrashWithBackoff(t *testing.T) {
	f := &fakeDaemonRuns{codes: []int{2, 1, 2, 0}, runFor: time.Second, now: time.Unix(0, 0)}
	if err := superviseLoop(f.deps()); err != nil {
		t.Fatalf("superviseLoop: %v", err)
	}
	if f.starts != 4 {
		t.Fatalf("starts=%d, want 4", f.starts)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second}
	if fmt.Sprint(f.sleeps) != fmt.Sprint(want) {
		t.Fatalf("sleeps=%v, want %v", f.sleeps, want)
	}
	if !strings.Contains(strings.Join(f.logs, "\n"), "exited with code 2") {
		t.Errorf("crash not logged: %v", f.logs)
	}
}

func TestSuperviseLoopResetsBackoffAfterAHealthyRun(t *testing.T) {
	f := &fakeDaemonRuns{codes: []int{2, 2, 0}, runFor: time.Hour, now: time.Unix(0, 0)}
	if err := superviseLoop(f.deps()); err != nil {
		t.Fatalf("superviseLoop: %v", err)
	}
	want := []time.Duration{10 * time.Second, 10 * time.Second}
	if fmt.Sprint(f.sleeps) != fmt.Sprint(want) {
		t.Fatalf("sleeps=%v, want %v: a daemon that ran for an hour crashed afresh", f.sleeps, want)
	}
}

func TestSuperviseLoopRetriesWhenTheBinaryWillNotStart(t *testing.T) {
	f := &fakeDaemonRuns{
		codes:    []int{0, 0},
		startErr: []error{errors.New("file is being replaced"), nil},
		now:      time.Unix(0, 0),
	}
	if err := superviseLoop(f.deps()); err != nil {
		t.Fatalf("superviseLoop: %v", err)
	}
	if f.starts != 2 || len(f.sleeps) != 1 {
		t.Fatalf("starts=%d sleeps=%v, want a retry after the failed start", f.starts, f.sleeps)
	}
}

func TestSupervisorBackoffIsCapped(t *testing.T) {
	cases := map[int]time.Duration{1: 10 * time.Second, 2: 20 * time.Second, 5: 160 * time.Second, 6: 5 * time.Minute, 50: 5 * time.Minute}
	for failures, want := range cases {
		if got := supervisorBackoff(failures); got != want {
			t.Errorf("supervisorBackoff(%d) = %v, want %v", failures, got, want)
		}
	}
}
