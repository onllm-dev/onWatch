package main

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
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
