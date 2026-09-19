package main

import (
	"bufio"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestResolveAdminPassHash(t *testing.T) {
	defaultHash := sha256hex(defaultAdminPass)
	realHash := sha256hex("s3cret")
	otherHash := sha256hex("dashboard-set")

	tests := []struct {
		name     string
		dbHash   string
		envPass  string
		wantHash string
		wantSrc  passSource
	}{
		{"first run stores env password", "", "s3cret", realHash, passFromEnvInitial},
		{"first run stores default", "", defaultAdminPass, defaultHash, passFromEnvInitial},
		{"stored default and env default keeps default", defaultHash, defaultAdminPass, defaultHash, passFromDB},
		{"stored default and real env adopts env", defaultHash, "s3cret", realHash, passFromEnvReplacesDefault},
		{"stored real password wins over env", otherHash, "s3cret", otherHash, passFromDB},
		{"stored real password wins over default env", otherHash, defaultAdminPass, otherHash, passFromDB},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, src := resolveAdminPassHash(tc.dbHash, tc.envPass)
			if hash != tc.wantHash {
				t.Fatalf("hash = %q, want %q", hash, tc.wantHash)
			}
			if src != tc.wantSrc {
				t.Fatalf("source = %v, want %v", src, tc.wantSrc)
			}
		})
	}
}

func TestDashboardExposedToNetwork(t *testing.T) {
	for host, want := range map[string]bool{
		"":          true,
		"0.0.0.0":   true,
		"::":        true,
		"[::]":      true,
		"127.0.0.1": false,
		"localhost": false,
		"::1":       false,
		"10.0.0.5":  true,
	} {
		if got := dashboardExposedToNetwork(host); got != want {
			t.Errorf("dashboardExposedToNetwork(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestLoginHint(t *testing.T) {
	if got := loginHint(false, false, "admin"); got != "" {
		t.Fatalf("custom password must not print a hint, got %q", got)
	}
	if got := loginHint(true, true, "admin"); got != "" {
		t.Fatalf("existing database must not print a hint (password may have been changed), got %q", got)
	}
	got := loginHint(true, false, "admin")
	for _, want := range []string{"admin", defaultAdminPass} {
		if !contains(got, want) {
			t.Fatalf("hint %q should mention %q", got, want)
		}
	}
}

func TestNetworkHint(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "::1", "[::1]"} {
		if got := networkHint(host); got != "" {
			t.Errorf("networkHint(%q) = %q, want empty for a loopback bind", host, got)
		}
	}
	if got := networkHint(""); !contains(got, "0.0.0.0") || !contains(got, "ONWATCH_HOST=127.0.0.1") {
		t.Fatalf("empty host should name 0.0.0.0 and the fix, got %q", got)
	}
	if got := networkHint("10.0.0.5"); !contains(got, "10.0.0.5") {
		t.Fatalf("explicit host should be echoed, got %q", got)
	}
}

func TestStartupNotices_NetworkWarningIgnoresPasswordState(t *testing.T) {
	network := "reachable from your network"
	// Custom password, existing database, exposed bind: no login hint, but the
	// exposure warning must still be there (beta.3 dropped it here).
	got := startupNotices(false, true, "admin", "0.0.0.0")
	if len(got) != 1 || !contains(got[0], network) {
		t.Fatalf("custom password + exposed bind should print only the network warning, got %q", got)
	}
	// Default password on a first start, exposed bind: both lines.
	got = startupNotices(true, false, "admin", "")
	if len(got) != 2 || !contains(got[0], "Login:") || !contains(got[1], network) {
		t.Fatalf("first start on 0.0.0.0 should print login hint then network warning, got %q", got)
	}
	// Loopback bind: never a network line.
	for _, tc := range []struct{ def, db bool }{{true, false}, {false, true}, {false, false}} {
		for _, line := range startupNotices(tc.def, tc.db, "admin", "127.0.0.1") {
			if contains(line, network) {
				t.Fatalf("loopback bind must not warn, got %q", line)
			}
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestStopProcessAndProcessAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1)")
	}
	if processAlive(0) {
		t.Fatal("processAlive(0) must be false")
	}
	if stopProcess(0) {
		t.Fatal("stopProcess(0) must be false")
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	if !processAlive(pid) {
		t.Fatalf("processAlive(%d) = false for a running child", pid)
	}
	if !stopProcess(pid) {
		t.Fatalf("stopProcess(%d) = false for a running child", pid)
	}
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit after stopProcess")
	}
	if processAlive(pid) {
		t.Fatalf("processAlive(%d) = true after the child exited", pid)
	}
}

func TestCollectOllamaKey_Verification(t *testing.T) {
	orig := verifyOllamaKey
	defer func() { verifyOllamaKey = orig }()

	t.Run("rejected key is re-prompted", func(t *testing.T) {
		calls := 0
		verifyOllamaKey = func(key string) (string, error) {
			calls++
			if key == "bad" {
				return "", api.ErrOllamaUnauthorized
			}
			return "pro", nil
		}
		got := collectOllamaKey(bufio.NewReader(strings.NewReader("bad\ngood\n")))
		if got != "good" || calls != 2 {
			t.Fatalf("got %q after %d calls", got, calls)
		}
	})

	t.Run("network failure keeps the key", func(t *testing.T) {
		verifyOllamaKey = func(string) (string, error) { return "", api.ErrOllamaNetworkError }
		if got := collectOllamaKey(bufio.NewReader(strings.NewReader("offline-key\n"))); got != "offline-key" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("empty input is rejected before verification", func(t *testing.T) {
		calls := 0
		verifyOllamaKey = func(string) (string, error) { calls++; return "free", nil }
		if got := collectOllamaKey(bufio.NewReader(strings.NewReader("\nk1\n"))); got != "k1" || calls != 1 {
			t.Fatalf("got %q, calls %d", got, calls)
		}
	})
}
