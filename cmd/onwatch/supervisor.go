package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/service"
)

// The Windows auto-start task runs `onwatch service run`: this supervisor. It
// starts the daemon in the foreground and restarts it when it crashes, which
// Task Scheduler alone cannot do. A clean exit (code 0) means the daemon was
// stopped on purpose - `onwatch stop`, a manual takeover, logoff - and ends
// supervision, mirroring launchd's KeepAlive {SuccessfulExit: false}.

const (
	supervisorFirstDelay = 10 * time.Second // launchd's ThrottleInterval
	supervisorMaxDelay   = 5 * time.Minute
	// supervisorHealthyRun is how long a daemon must stay up for its next
	// crash to be treated as new rather than part of a crash loop.
	supervisorHealthyRun = 10 * time.Minute
)

type superviseDeps struct {
	// start launches the daemon and returns a function that waits for it and
	// reports its exit code.
	start func() (wait func() (int, error), err error)
	sleep func(time.Duration)
	now   func() time.Time
	logf  func(format string, args ...any)
}

// superviseLoop runs the daemon until it exits cleanly, restarting it after
// every crash with a backoff that resets once a run stays up.
func superviseLoop(d superviseDeps) error {
	failures := 0
	for {
		started := d.now()
		code := -1
		wait, err := d.start()
		if err == nil {
			code, err = wait()
		}
		if err == nil && code == 0 {
			d.logf("supervisor: onWatch exited normally - not restarting")
			return nil
		}
		if d.now().Sub(started) >= supervisorHealthyRun {
			failures = 0
		}
		failures++
		delay := supervisorBackoff(failures)
		if err != nil {
			d.logf("supervisor: could not run onWatch: %v - retrying in %s", err, delay)
		} else {
			d.logf("supervisor: onWatch exited with code %d - restarting in %s", code, delay)
		}
		d.sleep(delay)
	}
}

// supervisorBackoff doubles from 10s per consecutive failure, capped at 5m.
func supervisorBackoff(failures int) time.Duration {
	delay := supervisorFirstDelay
	for i := 1; i < failures && delay < supervisorMaxDelay; i++ {
		delay *= 2
	}
	if delay > supervisorMaxDelay {
		delay = supervisorMaxDelay
	}
	return delay
}

// supervisorDeps wires superviseLoop to the real binary: the daemon runs in
// the foreground (EnvSupervised, so it neither forks nor backgrounds), takes
// over from any instance already running, and writes its output to the
// regular onWatch log.
func supervisorDeps(o service.Options) superviseDeps {
	return superviseDeps{
		start: func() (func() (int, error), error) {
			logFile, err := config.OpenRotatingLogFile(o.LogPath)
			if err != nil {
				return nil, err
			}
			cmd := exec.Command(o.BinPath)
			cmd.Dir = o.WorkDir
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			cmd.Env = append(daemonEnv(os.Environ()), service.EnvSupervised+"=1")
			// A hidden console of its own: ending the task, which closes the
			// supervisor's console, must not take the daemon down mid-write.
			cmd.SysProcAttr = companionSysProcAttr()
			if err := cmd.Start(); err != nil {
				logFile.Close()
				return nil, err
			}
			return func() (int, error) {
				defer logFile.Close()
				err := cmd.Wait()
				var exitErr *exec.ExitError
				switch {
				case err == nil:
					return 0, nil
				case errors.As(err, &exitErr):
					return exitErr.ExitCode(), nil
				default:
					return -1, err
				}
			}, nil
		},
		sleep: time.Sleep,
		now:   time.Now,
		logf: func(format string, args ...any) {
			f, err := os.OpenFile(o.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return
			}
			defer f.Close()
			fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
		},
	}
}
