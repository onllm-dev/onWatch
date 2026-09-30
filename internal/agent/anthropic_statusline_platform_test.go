package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// stubBridgeShell overrides the Git Bash check so bridge tests behave the same
// on every OS, regardless of what the machine running them has installed.
func stubBridgeShell(t *testing.T, available bool) {
	t.Helper()
	prev := bridgeShellAvailable
	bridgeShellAvailable = func() bool { return available }
	t.Cleanup(func() { bridgeShellAvailable = prev })
}

// stubBridgeGOOS makes the bridge code behave as it does on goos, so the
// Windows and Unix settings.json handling can be tested on any OS.
func stubBridgeGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := bridgeGOOS
	bridgeGOOS = goos
	t.Cleanup(func() { bridgeGOOS = prev })
}

// The macOS/Linux snippet is how existing installs are recognised, so its text
// must never drift.
func TestBridgeSnippet_UnixTextUnchanged(t *testing.T) {
	const want = `bash -c 'I=$(cat);D=$HOME/.onwatch/data;mkdir -p "$D" 2>/dev/null;T="$D/.sl-$$";printf "%s" "$I">"$T"&&mv -f "$T" "$D/anthropic-statusline.json" 2>/dev/null||rm -f "$T" 2>/dev/null;printf "%s" "$I"'`
	if bridgeSnippet != want {
		t.Fatalf("bridgeSnippet changed:\n got %s\nwant %s", bridgeSnippet, want)
	}
	for _, goos := range []string{"darwin", "linux", "freebsd"} {
		if got := bridgeSnippetFor(goos, "/home/u/.onwatch/data"); got != bridgeSnippet {
			t.Errorf("bridgeSnippetFor(%s) = %s, want the $HOME snippet", goos, got)
		}
	}
}

func TestBridgeSnippetFor_WindowsEmbedsForwardSlashDataDir(t *testing.T) {
	got := bridgeSnippetFor("windows", `C:\Users\O'Brien $x\.onwatch\data`)
	want := `D="C:/Users/O'\''Brien \$x/.onwatch/data";`
	if !strings.Contains(got, want) {
		t.Fatalf("windows snippet = %s\nwant it to contain %s", got, want)
	}
	if strings.Contains(got, `\.onwatch`) || strings.Contains(got, "$HOME") {
		t.Fatalf("windows snippet must not use backslashes or $HOME: %s", got)
	}
	if !hasBridgeSnippet(got) {
		t.Fatal("windows snippet must carry the bridge marker")
	}
}

func TestStripBridgeSnippet_AllVariants(t *testing.T) {
	variants := map[string]string{
		"unix":    bridgeSnippet,
		"windows": bridgeSnippetFor("windows", `C:\Users\me\.onwatch\data`),
	}
	for name, snippet := range variants {
		if got, ok := stripBridgeSnippet(snippet + " | ~/.claude/statusline.sh"); !ok || got != "~/.claude/statusline.sh" {
			t.Errorf("%s piped: got (%q, %v)", name, got, ok)
		}
		if got, ok := stripBridgeSnippet(snippet + bridgeStandaloneSuffix); !ok || got != "" {
			t.Errorf("%s standalone: got (%q, %v)", name, got, ok)
		}
		if got, ok := stripBridgeSnippet(snippet); ok || got != snippet {
			t.Errorf("%s bare: got (%q, %v), want unchanged", name, got, ok)
		}
	}
	foreign := "cat ~/.onwatch/data/anthropic-statusline.json"
	if got, ok := stripBridgeSnippet(foreign); ok || got != foreign {
		t.Errorf("foreign command: got (%q, %v), want unchanged", got, ok)
	}
}

func TestBridgedCommand(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			stubBridgeGOOS(t, goos)
			current := addBridgeSnippet("~/sl.sh")
			if got, changed := bridgedCommand(current); changed || got != current {
				t.Errorf("current bridge: got (%q, %v), want unchanged", got, changed)
			}

			foreign := "cat ~/.onwatch/data/anthropic-statusline.json"
			if got, changed := bridgedCommand(foreign); changed || got != foreign {
				t.Errorf("foreign command: got (%q, %v), want unchanged", got, changed)
			}

			if got, changed := bridgedCommand(""); !changed || got != addBridgeSnippet("") {
				t.Errorf("empty: got (%q, %v)", got, changed)
			}
			if got, changed := bridgedCommand("~/sl.sh"); !changed || got != current {
				t.Errorf("no bridge: got (%q, %v), want %q", got, changed, current)
			}
		})
	}
}

// On Windows, a bridge written by an older Windows build (the $HOME form) or
// for another data directory is outdated for this platform and replaced in
// place.
func TestBridgedCommand_WindowsReplacesStaleWindowsVariants(t *testing.T) {
	stubBridgeGOOS(t, "windows")
	current := addBridgeSnippet("~/sl.sh")
	stale := map[string]string{
		"$HOME form":     bridgeSnippet + " | ~/sl.sh",
		"other data dir": bridgeSnippetFor("windows", filepath.Join(t.TempDir(), "elsewhere")) + " | ~/sl.sh",
	}
	for name, cmd := range stale {
		if got, changed := bridgedCommand(cmd); !changed || got != current {
			t.Errorf("%s: got (%q, %v), want %q", name, got, changed, current)
		}
	}
}

// A settings.json synced between a Windows machine and a macOS/Linux one
// carries the Windows bridge. macOS/Linux must leave that recognised bridge
// alone rather than rewrite it, or each machine would rewrite the other's
// bridge forever.
func TestBridgedCommand_UnixLeavesWindowsVariant(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			stubBridgeGOOS(t, goos)
			for _, cmd := range []string{
				bridgeSnippetFor("windows", `C:\Users\me\.onwatch\data`) + " | ~/sl.sh",
				bridgeSnippetFor("windows", `C:\Users\me\.onwatch\data`) + bridgeStandaloneSuffix,
			} {
				if got, changed := bridgedCommand(cmd); changed || got != cmd {
					t.Errorf("windows bridge on %s: got (%q, %v), want unchanged", goos, got, changed)
				}
			}
		})
	}
}

// Once Windows has written its bridge, neither side rewrites it again.
func TestBridgedCommand_SyncedSettingsSettle(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	cmd := bridgeSnippet + " | ~/sl.sh" // written on macOS

	stubBridgeGOOS(t, "windows")
	cmd, _ = bridgedCommand(cmd)
	for i := 0; i < 3; i++ {
		for _, goos := range []string{"darwin", "windows"} {
			bridgeGOOS = goos
			if got, changed := bridgedCommand(cmd); changed {
				t.Fatalf("round %d on %s rewrote the bridge:\n from %s\n   to %s", i, goos, cmd, got)
			}
		}
	}
}

// A bridge written for another data directory (for example the $HOME form an
// older Windows build wrote) is replaced, not stacked.
func TestSetupStatuslineBridge_ReplacesStaleSnippet(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	stubBridgeGOOS(t, "windows")
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := bridgeSnippetFor("windows", filepath.Join(t.TempDir(), "elsewhere")) + " | ~/sl.sh"
	writeStatusLineSettings(t, claudeDir, stale)

	if err := SetupStatuslineBridge(slog.Default()); err != nil {
		t.Fatalf("SetupStatuslineBridge: %v", err)
	}

	cmd := readStatusLineCommand(t, claudeDir)
	if cmd != addBridgeSnippet("~/sl.sh") {
		t.Fatalf("command = %s\nwant %s", cmd, addBridgeSnippet("~/sl.sh"))
	}
	if n := strings.Count(cmd, bridgeMarker); n != 1 {
		t.Fatalf("bridge marker appears %d times, want 1", n)
	}
}

// Setup and the health check on macOS/Linux leave a synced Windows bridge in
// settings.json untouched.
func TestStatuslineBridge_UnixLeavesSyncedWindowsBridge(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	stubBridgeGOOS(t, "darwin")
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	windowsCmd := bridgeSnippetFor("windows", `C:\Users\me\.onwatch\data`) + " | ~/sl.sh"
	writeStatusLineSettings(t, claudeDir, windowsCmd)

	if err := SetupStatuslineBridge(slog.Default()); err != nil {
		t.Fatalf("SetupStatuslineBridge: %v", err)
	}
	resetBridgeCheck()
	EnsureStatuslineBridge(slog.Default())

	if cmd := readStatusLineCommand(t, claudeDir); cmd != windowsCmd {
		t.Fatalf("command = %s\nwant untouched %s", cmd, windowsCmd)
	}
}

// Without Git Bash, Claude Code on Windows runs the statusline in PowerShell,
// where the bash snippet would break the user's statusline. Setup and the
// health check must leave a bridge-free settings.json alone.
func TestStatuslineBridge_NoBashShellLeavesSettingsAlone(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, false)
	stubBridgeGOOS(t, "windows")
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	userCmd := "powershell -NoProfile -File C:/Users/me/.claude/statusline.ps1"
	writeStatusLineSettings(t, claudeDir, userCmd)

	if err := SetupStatuslineBridge(slog.Default()); err != nil {
		t.Fatalf("SetupStatuslineBridge: %v", err)
	}
	resetBridgeCheck()
	EnsureStatuslineBridge(slog.Default())

	if cmd := readStatusLineCommand(t, claudeDir); cmd != userCmd {
		t.Fatalf("command = %s, want untouched %s", cmd, userCmd)
	}
}

// A bridge that an older onWatch wrote before it checked for Git Bash keeps
// breaking the statusline under PowerShell. Without Git Bash, Setup and the
// health check remove it and restore the user's own command.
func TestStatuslineBridge_NoBashShellRemovesExistingBridge(t *testing.T) {
	userCmd := "powershell -NoProfile -File C:/Users/me/.claude/statusline.ps1"
	variants := map[string]string{
		"older $HOME form piped":      bridgeSnippet + " | " + userCmd,
		"windows form piped":          bridgeSnippetFor("windows", `C:\Users\me\.onwatch\data`) + " | " + userCmd,
		"older $HOME form standalone": bridgeSnippet + bridgeStandaloneSuffix,
	}
	for name, bridged := range variants {
		want := userCmd
		if strings.HasSuffix(bridged, bridgeStandaloneSuffix) {
			want = ""
		}
		for _, entry := range []string{"setup", "ensure"} {
			t.Run(name+"/"+entry, func(t *testing.T) {
				home := t.TempDir()
				testhome.SetTestHome(t, home)
				stubBridgeShell(t, false)
				stubBridgeGOOS(t, "windows")
				claudeDir := filepath.Join(home, ".claude")
				if err := os.MkdirAll(claudeDir, 0o700); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				writeStatusLineSettings(t, claudeDir, bridged)

				if entry == "setup" {
					if err := SetupStatuslineBridge(slog.Default()); err != nil {
						t.Fatalf("SetupStatuslineBridge: %v", err)
					}
				} else {
					resetBridgeCheck()
					EnsureStatuslineBridge(slog.Default())
				}

				if cmd := readStatusLineCommand(t, claudeDir); cmd != want {
					t.Fatalf("command = %q, want %q", cmd, want)
				}
			})
		}
	}
}

// resetBridgeCheck lets the next EnsureStatuslineBridge call run its check.
func resetBridgeCheck() {
	bridgeSetup.mu.Lock()
	bridgeSetup.lastCheck = time.Time{}
	bridgeSetup.mu.Unlock()
}

func TestFindGitBash(t *testing.T) {
	root := filepath.Join("X", "Git")
	bash := filepath.Join(root, "bin", "bash.exe")

	tests := []struct {
		name  string
		env   map[string]string
		git   string
		files []string
		want  string
	}{
		{
			name:  "explicit env var",
			env:   map[string]string{"CLAUDE_CODE_GIT_BASH_PATH": filepath.Join("Y", "bash.exe")},
			files: []string{filepath.Join("Y", "bash.exe")},
			want:  filepath.Join("Y", "bash.exe"),
		},
		{
			name:  "env var pointing nowhere falls through",
			env:   map[string]string{"CLAUDE_CODE_GIT_BASH_PATH": filepath.Join("Y", "bash.exe"), "ProgramFiles": "X"},
			files: []string{bash},
			want:  bash,
		},
		{
			name:  "git in cmd dir",
			git:   filepath.Join(root, "cmd", "git.exe"),
			files: []string{bash},
			want:  bash,
		},
		{
			name:  "git in mingw64 bin",
			git:   filepath.Join(root, "mingw64", "bin", "git.exe"),
			files: []string{bash},
			want:  bash,
		},
		{
			name:  "program files",
			env:   map[string]string{"ProgramFiles": "X"},
			files: []string{bash},
			want:  bash,
		},
		{
			name:  "per-user install",
			env:   map[string]string{"LOCALAPPDATA": "L"},
			files: []string{filepath.Join("L", "Programs", "Git", "bin", "bash.exe")},
			want:  filepath.Join("L", "Programs", "Git", "bin", "bash.exe"),
		},
		{
			name: "not installed",
			env:  map[string]string{"ProgramFiles": "X"},
			git:  filepath.Join("Z", "shims", "git.exe"),
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			lookPath := func(string) (string, error) {
				if tt.git == "" {
					return "", errors.New("not found")
				}
				return tt.git, nil
			}
			exists := func(p string) bool {
				for _, f := range tt.files {
					if filepath.Clean(p) == filepath.Clean(f) {
						return true
					}
				}
				return false
			}
			if got := findGitBash(getenv, lookPath, exists); filepath.Clean(got) != filepath.Clean(tt.want) {
				t.Fatalf("findGitBash() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBridgeSnippet_RunsUnderBash executes the generated statusline command in
// a real bash, the way Claude Code does, and checks that stdin is passed
// through untouched and saved exactly where onWatch reads it. The data
// directory contains a space, a single quote and a dollar sign to exercise the
// quoting of the Windows variant.
func TestBridgeSnippet_RunsUnderBash(t *testing.T) {
	bash := testBashPath(t)
	payload := `{"rate_limits":{"five_hour":{"used_percentage":12.5,"resets_at":1790000000}}}`

	run := func(t *testing.T, command string, env []string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bash, "-c", command)
		cmd.Stdin = strings.NewReader(payload)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run statusline command: %v", err)
		}
		return string(out)
	}

	t.Run("windows variant", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "it's $odd", "data")
		command := bridgeSnippetFor("windows", dataDir) + " | cat"
		if out := run(t, command, nil); out != payload {
			t.Fatalf("stdout = %q, want payload passed through", out)
		}
		assertStatuslineFile(t, filepath.Join(dataDir, statuslineFileName), payload)

		standalone := bridgeSnippetFor("windows", dataDir) + bridgeStandaloneSuffix
		if out := run(t, standalone, nil); out != "" {
			t.Fatalf("standalone stdout = %q, want empty", out)
		}
	})

	t.Run("unix variant", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the $HOME snippet is only written on macOS and Linux")
		}
		home := t.TempDir()
		if out := run(t, bridgeSnippet+" | cat", []string{"HOME=" + home}); out != payload {
			t.Fatalf("stdout = %q, want payload passed through", out)
		}
		assertStatuslineFile(t, filepath.Join(home, ".onwatch", "data", statuslineFileName), payload)
	})
}

// testBashPath returns the bash Claude Code would use: Git Bash on Windows,
// bash from PATH elsewhere.
func testBashPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		if p := findGitBash(os.Getenv, exec.LookPath, isRegularFile); p != "" {
			return p
		}
		t.Skip("Git Bash not installed; the bridge is not configured without it")
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	return p
}

func assertStatuslineFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("statusline file not written where onWatch reads it: %v", err)
	}
	if string(data) != want {
		t.Fatalf("statusline file = %q, want %q", data, want)
	}
}

func writeStatusLineSettings(t *testing.T, claudeDir, command string) {
	t.Helper()
	data, err := json.MarshalIndent(map[string]interface{}{
		"statusLine": map[string]interface{}{"type": "command", "command": command},
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func readStatusLineCommand(t *testing.T, claudeDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return getCurrentStatusLineCommand(settings)
}
