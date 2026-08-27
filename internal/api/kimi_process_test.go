package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestIsKimiCodeRunning_DetectsProcessNamedKimiCode(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("pgrep -x is used on unix")
	}
	src, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH")
	}
	dst := filepath.Join(t.TempDir(), "kimi-code")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dst, "8")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if IsKimiCodeRunning() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected IsKimiCodeRunning to detect a process named kimi-code")
}
