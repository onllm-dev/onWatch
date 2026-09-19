package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
)

// daemonPIDEnv carries the spawning daemon's PID to the tray companion so the
// companion can tell whether the process that started it is still around.
const daemonPIDEnv = "ONWATCH_DAEMON_PID"

const (
	// daemonWatchInterval is how often the companion looks for its daemon.
	daemonWatchInterval = 5 * time.Second
	// daemonWatchGrace is how long the daemon may stay missing before the
	// companion quits. A restart (stop plus start, or an update) reclaims the
	// icon inside this window, so it never flickers.
	daemonWatchGrace = 30 * time.Second
)

// daemonWatcher decides when a tray companion has outlived its daemon.
// Without it the icon survives a daemon that died outside `onwatch stop`, or a
// stop that ran with a different HOME or LOCALAPPDATA than the start did, and
// the user is left with a gray dash they cannot get rid of (issue #124).
type daemonWatcher struct {
	pid       int
	alive     func(int) bool
	daemonPID func() int
	reachable func() bool
	grace     time.Duration
	now       func() time.Time

	goneSince time.Time
}

// newDaemonWatcher watches the daemon named by ONWATCH_DAEMON_PID, falling
// back to whatever the daemon PID file holds when the companion was started by
// hand.
func newDaemonWatcher(cfg *config.Config, envPID int) *daemonWatcher {
	pid := envPID
	if pid <= 0 {
		pid = readRuntimePID(pidFile)
	}
	port := 0
	if cfg != nil {
		port = cfg.Port
	}
	return &daemonWatcher{
		pid:       pid,
		alive:     processAlive,
		daemonPID: func() int { return readRuntimePID(pidFile) },
		reachable: func() bool { return daemonPortOpen(port) },
		grace:     daemonWatchGrace,
		now:       time.Now,
	}
}

// daemonPIDFromEnv reads the PID the daemon handed over at spawn time.
func daemonPIDFromEnv() int {
	pid, err := strconv.Atoi(strings.TrimSpace(os.Getenv(daemonPIDEnv)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// check runs one poll and reports whether the companion should quit.
func (w *daemonWatcher) check() bool {
	if w.pid > 0 && w.alive(w.pid) {
		w.goneSince = time.Time{}
		return false
	}
	// The daemon may have restarted under a new PID; adopt it rather than
	// quitting on a process the user never meant to end.
	if pid := w.daemonPID(); pid > 0 && pid != w.pid && w.alive(pid) {
		w.pid = pid
		w.goneSince = time.Time{}
		return false
	}
	// The PID file can be missing or stale (a different HOME or LOCALAPPDATA);
	// a dashboard answering on our port means the daemon is still there.
	if w.reachable() {
		w.goneSince = time.Time{}
		return false
	}
	if w.goneSince.IsZero() {
		w.goneSince = w.now()
		return false
	}
	return w.now().Sub(w.goneSince) >= w.grace
}

// watchDaemon polls until the daemon is gone for good, then calls quit once.
func watchDaemon(w *daemonWatcher, interval time.Duration, stop <-chan struct{}, quit func()) {
	if w == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if w.check() {
				quit()
				return
			}
		}
	}
}

// daemonPortOpen reports whether something is listening on the dashboard port.
func daemonPortOpen(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
