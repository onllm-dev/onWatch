package testhome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxHome(t *testing.T) {
	for _, env := range append([]string{"HOME", "USERPROFILE", "LOCALAPPDATA", "ONWATCH_EXTRA_TEST_VAR"}, OverrideEnv...) {
		t.Setenv(env, "/developer/value")
	}

	home, cleanup, err := SandboxHome("ONWATCH_EXTRA_TEST_VAR")
	if err != nil {
		t.Fatalf("SandboxHome: %v", err)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatalf("sandbox home %q not created: %v", home, err)
	}
	for _, env := range []string{"HOME", "USERPROFILE"} {
		if got := os.Getenv(env); got != home {
			t.Errorf("%s = %q, want %q", env, got, home)
		}
	}
	if got, want := os.Getenv("LOCALAPPDATA"), filepath.Join(home, "AppData", "Local"); got != want {
		t.Errorf("LOCALAPPDATA = %q, want %q", got, want)
	}
	if got, err := os.UserHomeDir(); err != nil || got != home {
		t.Errorf("os.UserHomeDir() = %q, %v; want %q", got, err, home)
	}
	for _, env := range append([]string{"ONWATCH_EXTRA_TEST_VAR"}, OverrideEnv...) {
		if v, ok := os.LookupEnv(env); ok {
			t.Errorf("%s still set to %q", env, v)
		}
	}

	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("cleanup left %q behind (stat err %v)", home, err)
	}
}

func TestSetTestHome(t *testing.T) {
	dir := t.TempDir()
	SetTestHome(t, dir)
	for _, env := range []string{"HOME", "USERPROFILE"} {
		if got := os.Getenv(env); got != dir {
			t.Errorf("%s = %q, want %q", env, got, dir)
		}
	}
	if got, want := os.Getenv("LOCALAPPDATA"), filepath.Join(dir, "AppData", "Local"); got != want {
		t.Errorf("LOCALAPPDATA = %q, want %q", got, want)
	}
	if got, err := os.UserHomeDir(); err != nil || got != dir {
		t.Errorf("os.UserHomeDir() = %q, %v; want %q", got, err, dir)
	}
}

func TestSetTestHome_EmptyMakesUserHomeDirFail(t *testing.T) {
	SetTestHome(t, "")
	if got, err := os.UserHomeDir(); err == nil {
		t.Errorf("os.UserHomeDir() = %q, want an error for an empty home", got)
	}
}
