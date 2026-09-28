// Package testhome points a test process at a throwaway home directory so
// tests never read or write the developer's (or CI runner's) real ~/.claude,
// ~/.codex, ~/.onwatch and so on.
//
// It is a leaf package (standard library only) so that the in-package tests
// of api, agent, web and cmd/onwatch can import it without an import cycle;
// internal/testutil itself imports those packages and cannot be used there.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// OverrideEnv lists the environment variables that redirect provider
// credential or data lookups away from the home directory. A developer's shell
// value for any of them would bypass the sandbox home, so SandboxHome clears
// them all.
var OverrideEnv = []string{
	"CODEX_HOME",
	"OPENCODE_HOME",
	"XDG_DATA_HOME",
	"XDG_CONFIG_HOME",
	"KIMI_CODE_HOME",
	"KIMI_CODE_CREDENTIALS",
	"KIMI_CREDENTIALS",
	"MUSE_AUTH_PATH",
	"COMMANDCODE_AUTH_PATH",
	"GROK_HOME",
}

// SandboxHome is meant for TestMain. It creates an empty temp home, points
// HOME, USERPROFILE and LOCALAPPDATA at it, and unsets OverrideEnv plus any
// extraUnset variables. os.UserHomeDir reads HOME on Unix but USERPROFILE on
// Windows, and onWatch's Windows state lives under LOCALAPPDATA, so all three
// move. LOCALAPPDATA is set on every OS so behavior is uniform.
//
// Callers that must record the real home first (api.SetTestMode(true)) have to
// do so before calling SandboxHome. cleanup removes the sandbox.
func SandboxHome(extraUnset ...string) (home string, cleanup func(), err error) {
	for _, env := range OverrideEnv {
		os.Unsetenv(env)
	}
	for _, env := range extraUnset {
		os.Unsetenv(env)
	}

	home, err = os.MkdirTemp("", "onwatch-test-home-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create sandbox home: %w", err)
	}
	if err := setHomeEnv(os.Setenv, home); err != nil {
		os.RemoveAll(home)
		return "", func() {}, err
	}
	return home, func() { os.RemoveAll(home) }, nil
}

// SetTestHome points the user's home directory at dir for the duration of the
// test via t.Setenv: HOME (Unix, Git Bash), USERPROFILE (os.UserHomeDir on
// Windows) and LOCALAPPDATA (<dir>/AppData/Local). An empty dir clears HOME
// and USERPROFILE, which makes os.UserHomeDir fail on every platform.
func SetTestHome(t testing.TB, dir string) {
	t.Helper()
	_ = setHomeEnv(func(k, v string) error { t.Setenv(k, v); return nil }, dir)
}

// LocalAppData returns the LOCALAPPDATA value SandboxHome and SetTestHome use
// for home ("" for an empty home).
func LocalAppData(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, "AppData", "Local")
}

func setHomeEnv(setenv func(k, v string) error, home string) error {
	for k, v := range map[string]string{
		"HOME":         home,
		"USERPROFILE":  home,
		"LOCALAPPDATA": LocalAppData(home),
	} {
		if err := setenv(k, v); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}
	return nil
}
