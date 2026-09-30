package menubar

import (
	"errors"
	"testing"
)

func TestBrowserGrantFlow(t *testing.T) {
	for _, outcome := range []string{"granted", "cancelled", "panel was not answered"} {
		t.Run(outcome, func(t *testing.T) {
			calls := 0
			g := browserGrantFlow{}
			result := g.run(func() string { return "/browser" }, func(string) (bool, string) { return outcome == "granted", outcome }, func(string) error { return nil }, func() error { calls++; return nil })
			if outcome == "granted" && (calls != 1 || result != "retrying") {
				t.Fatalf("%s calls=%d", result, calls)
			}
			if outcome != "granted" && calls != 0 {
				t.Fatal("retried without grant")
			}
		})
	}
	g := browserGrantFlow{}
	calls := 0
	result := g.run(func() string { return "/browser" }, func(string) (bool, string) { return true, "granted" }, func(string) error { return errors.New("denied") }, func() error { calls++; return nil })
	if result != "unavailable" || calls != 0 {
		t.Fatalf("%s calls=%d", result, calls)
	}
}

func TestBrowserGrantCoalesces(t *testing.T) {
	g := browserGrantFlow{}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		g.run(func() string { return "/browser" }, func(string) (bool, string) { close(started); <-release; return false, "cancelled" }, nil, nil)
	}()
	<-started
	result := g.run(func() string { t.Error("duplicate reached picker"); return "" }, nil, nil, nil)
	close(release)
	<-done
	if result != "busy" {
		t.Fatal(result)
	}
}

func TestBrowserGrantAlreadyReadable(t *testing.T) {
	g := browserGrantFlow{}
	calls := 0
	result := g.run(func() string { return "" }, nil, nil, func() error { calls++; return errors.New("cooldown") })
	if calls != 1 || result != "retry_failed" {
		t.Fatalf("%s calls=%d", result, calls)
	}
}
