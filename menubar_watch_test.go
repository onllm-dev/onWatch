package main

import (
	"net"
	"os"
	"testing"
	"time"
)

// stubWatcher builds a watcher whose world is fully faked: the clock only
// moves when a test says so.
func stubWatcher(pid int, alive map[int]bool, daemonPID func() int, reachable bool, now *time.Time) *daemonWatcher {
	return &daemonWatcher{
		pid:       pid,
		alive:     func(p int) bool { return alive[p] },
		daemonPID: daemonPID,
		reachable: func() bool { return reachable },
		grace:     30 * time.Second,
		now:       func() time.Time { return *now },
	}
}

func TestDaemonWatcherKeepsRunningWhileDaemonIsAlive(t *testing.T) {
	now := time.Unix(1000, 0)
	w := stubWatcher(42, map[int]bool{42: true}, func() int { return 42 }, true, &now)
	for i := 0; i < 5; i++ {
		if w.check() {
			t.Fatalf("check %d wanted the companion to stay", i)
		}
		now = now.Add(time.Minute)
	}
}

func TestDaemonWatcherQuitsAfterTheGracePeriod(t *testing.T) {
	now := time.Unix(1000, 0)
	w := stubWatcher(42, map[int]bool{}, func() int { return 0 }, false, &now)

	if w.check() {
		t.Fatal("the first poll only starts the grace period")
	}
	now = now.Add(29 * time.Second)
	if w.check() {
		t.Fatal("still inside the grace period")
	}
	now = now.Add(2 * time.Second)
	if !w.check() {
		t.Fatal("wanted the companion to quit once the grace period passed")
	}
}

func TestDaemonWatcherAdoptsARestartedDaemon(t *testing.T) {
	now := time.Unix(1000, 0)
	alive := map[int]bool{42: true}
	daemonPID := 42
	w := stubWatcher(42, alive, func() int { return daemonPID }, false, &now)

	// The daemon stops: gone from the process table and from the PID file.
	delete(alive, 42)
	daemonPID = 0
	if w.check() {
		t.Fatal("a restart must not quit the companion inside the grace period")
	}

	// It comes back under a new PID well inside the grace period.
	now = now.Add(3 * time.Second)
	daemonPID = 77
	alive[77] = true
	if w.check() {
		t.Fatal("the companion should have adopted the new daemon")
	}
	if w.pid != 77 {
		t.Fatalf("watching PID %d, want 77", w.pid)
	}
	if !w.goneSince.IsZero() {
		t.Fatal("adopting a daemon must reset the grace period")
	}

	// And a later disappearance starts a fresh grace period rather than
	// inheriting the old one.
	delete(alive, 77)
	daemonPID = 0
	now = now.Add(time.Hour)
	if w.check() {
		t.Fatal("the grace period must restart from the new disappearance")
	}
}

func TestDaemonWatcherTrustsAReachableDashboard(t *testing.T) {
	// smoochy's case: the PID file lives under a different LOCALAPPDATA than
	// the one this process sees, so only the port answers for the daemon.
	now := time.Unix(1000, 0)
	w := stubWatcher(0, map[int]bool{}, func() int { return 0 }, true, &now)
	now = now.Add(time.Hour)
	if w.check() {
		t.Fatal("a daemon answering on the port keeps the companion alive")
	}
}

func TestDaemonWatcherIgnoresAStalePIDFileEntry(t *testing.T) {
	now := time.Unix(1000, 0)
	// The PID file still names the dead daemon.
	w := stubWatcher(42, map[int]bool{}, func() int { return 42 }, false, &now)
	if w.check() {
		t.Fatal("the first poll only starts the grace period")
	}
	now = now.Add(31 * time.Second)
	if !w.check() {
		t.Fatal("a stale PID file must not keep the companion alive")
	}
}

func TestWatchDaemonQuitsOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	w := stubWatcher(42, map[int]bool{}, func() int { return 0 }, false, &now)
	w.grace = 0
	w.now = time.Now

	quit := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	go watchDaemon(w, time.Millisecond, stop, func() { close(quit) })

	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("watchDaemon never quit after the daemon disappeared")
	}
}

func TestWatchDaemonStops(t *testing.T) {
	now := time.Unix(1000, 0)
	w := stubWatcher(42, map[int]bool{42: true}, func() int { return 42 }, true, &now)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		watchDaemon(w, time.Millisecond, stop, func() { t.Error("quit called while the daemon was alive") })
		close(done)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchDaemon ignored its stop channel")
	}
}

func TestDaemonPIDFromEnv(t *testing.T) {
	t.Setenv(daemonPIDEnv, " 4321\n")
	if got := daemonPIDFromEnv(); got != 4321 {
		t.Fatalf("got %d, want 4321", got)
	}
	for _, value := range []string{"", "0", "-1", "abc"} {
		t.Setenv(daemonPIDEnv, value)
		if got := daemonPIDFromEnv(); got != 0 {
			t.Fatalf("env %q gave PID %d, want 0", value, got)
		}
	}
	os.Unsetenv(daemonPIDEnv)
	if got := daemonPIDFromEnv(); got != 0 {
		t.Fatalf("unset env gave PID %d, want 0", got)
	}
}

func TestDaemonPortOpen(t *testing.T) {
	if daemonPortOpen(0) {
		t.Error("port 0 is never a daemon")
	}
	ln, err := listenLoopback()
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	defer ln.Close()
	if !daemonPortOpen(loopbackPort(ln)) {
		t.Error("wanted the listening port to count as a live daemon")
	}
	port := loopbackPort(ln)
	ln.Close()
	if daemonPortOpen(port) {
		t.Error("a closed port must not count as a live daemon")
	}
}

func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

func loopbackPort(ln net.Listener) int {
	return ln.Addr().(*net.TCPAddr).Port
}
