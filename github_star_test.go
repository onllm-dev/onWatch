package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH scripts the gh CLI: which sub-commands succeed, and records calls.
type fakeGH struct {
	loggedIn bool
	starred  bool
	starErr  error
	calls    []string
}

func (f *fakeGH) run(args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		if f.loggedIn {
			return nil
		}
		return errors.New("not logged in")
	case len(args) >= 2 && args[0] == "api":
		if f.starred {
			return nil
		}
		return errors.New("404")
	case len(args) >= 2 && args[0] == "repo" && args[1] == "star":
		return f.starErr
	}
	return errors.New("unexpected: " + strings.Join(args, " "))
}

func (f *fakeGH) starCalls() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "repo star ") {
			n++
		}
	}
	return n
}

func TestOfferGitHubStar(t *testing.T) {
	t.Setenv("ONWATCH_STAR", "")

	t.Run("no gh login: silent, nothing written", func(t *testing.T) {
		gh := &fakeGH{loggedIn: false}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		offerGitHubStar(bufio.NewReader(strings.NewReader("\n")), gh.run, marker)
		if gh.starCalls() != 0 {
			t.Fatal("must not star without a logged-in gh")
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("marker must not be written when no prompt was shown")
		}
	})

	t.Run("already starred: no prompt", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true, starred: true}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		offerGitHubStar(bufio.NewReader(strings.NewReader("\n")), gh.run, marker)
		if gh.starCalls() != 0 {
			t.Fatal("must not star again")
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("marker must not be written when no prompt was shown")
		}
	})

	t.Run("Enter accepts the default yes and stars", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		offerGitHubStar(bufio.NewReader(strings.NewReader("\n")), gh.run, marker)
		if gh.starCalls() != 1 {
			t.Fatalf("expected one star call, calls: %v", gh.calls)
		}
		if !strings.HasSuffix(gh.calls[len(gh.calls)-1], "repo star "+githubRepoSlug) {
			t.Fatalf("star must target %s, got %v", githubRepoSlug, gh.calls)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("marker must be written after the prompt was answered")
		}
	})

	t.Run("n declines", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		offerGitHubStar(bufio.NewReader(strings.NewReader("n\n")), gh.run, marker)
		if gh.starCalls() != 0 {
			t.Fatal("n must not star")
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("declining must still be remembered")
		}
	})

	t.Run("unattended (EOF) takes the default yes", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		offerGitHubStar(bufio.NewReader(strings.NewReader("")), gh.run, marker)
		if gh.starCalls() != 1 {
			t.Fatalf("unattended run must star by default, calls: %v", gh.calls)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("marker must be written so it is a one-time action")
		}
	})

	t.Run("marker present: never asked twice", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true}
		marker := filepath.Join(t.TempDir(), ".star-prompted")
		if err := os.WriteFile(marker, []byte("asked\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		offerGitHubStar(bufio.NewReader(strings.NewReader("\n")), gh.run, marker)
		if len(gh.calls) != 0 {
			t.Fatalf("must not even probe gh when the marker exists, calls: %v", gh.calls)
		}
	})

	t.Run("ONWATCH_STAR=no opts out", func(t *testing.T) {
		t.Setenv("ONWATCH_STAR", "no")
		gh := &fakeGH{loggedIn: true}
		offerGitHubStar(bufio.NewReader(strings.NewReader("\n")), gh.run, filepath.Join(t.TempDir(), "m"))
		if len(gh.calls) != 0 {
			t.Fatalf("opt-out must skip gh entirely, calls: %v", gh.calls)
		}
	})

	t.Run("star failure is reported, not fatal", func(t *testing.T) {
		gh := &fakeGH{loggedIn: true, starErr: errors.New("network")}
		offerGitHubStar(bufio.NewReader(strings.NewReader("y\n")), gh.run, filepath.Join(t.TempDir(), "m"))
		if gh.starCalls() != 1 {
			t.Fatal("should have attempted the star")
		}
	})
}
