//go:build !windows

package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// isolatedStarter is NewAnthropicStarter with Claude Code's settings pointed
// at an empty folder, so the developer's own Claude Code setup never decides
// whether a test pings.
func isolatedStarter(t *testing.T) *AnthropicStarter {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	return NewAnthropicStarter()
}

// fakeClaudeScript writes a stand-in `claude` that records its arguments,
// working directory and API-key variables, then prints body and exits code.
func fakeClaudeScript(t *testing.T, body string, code int) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record.txt")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"{ echo \"args=$*\"; echo \"pwd=$(pwd)\"; echo \"key=${ANTHROPIC_API_KEY:-unset}\"; echo \"traffic=${CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC:-unset}\"; echo \"settings=$(cat onwatch-starter-settings.json)\"; echo \"mcp=$(cat onwatch-starter-mcp.json)\"; echo \"files=$(ls -A | tr '\\n' ' ')\"; } > '" + record + "'\n" +
		"cat <<'JSON'\n" + body + "\nJSON\n" +
		"exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

// Runs the real exec path against a fake CLI.
func TestAnthropicStarterPing_RealProcess(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-leak")
	t.Setenv("ANTHROPIC_STARTER_MODEL", "")
	bin, record := fakeClaudeScript(t, `{"type":"result","subtype":"success","is_error":false,"result":"Session started"}`, 0)
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := isolatedStarter(t).Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"args=-p --mcp-config onwatch-starter-mcp.json --strict-mcp-config --settings onwatch-starter-settings.json --model haiku --output-format json --max-turns 1 Reply with only", "key=unset", "traffic=1",
		`settings={"disableAllHooks":true,"permissions":{"deny":["Bash",`, `mcp={"mcpServers":{}}`, "files=onwatch-starter-mcp.json onwatch-starter-settings.json \n", filepath.Join("onwatch", "claude-starter")} {
		if !strings.Contains(got, want) {
			t.Errorf("record missing %q:\n%s", want, got)
		}
	}
}

func TestAnthropicStarterPing_RealProcessLoggedOut(t *testing.T) {
	bin, _ := fakeClaudeScript(t, `{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login"}`, 1)
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := isolatedStarter(t).Ping(ctx)
	if !errors.Is(err, ErrAnthropicStarterAuth) {
		t.Fatalf("err = %v, want ErrAnthropicStarterAuth", err)
	}
}

// A CLI that hangs, and leaves a child holding its output pipes, is killed at
// the deadline and Ping returns soon after instead of waiting on the child.
func TestAnthropicStarterPing_HangingCLIWithChildIsBounded(t *testing.T) {
	orig := anthropicStarterWaitDelay
	anthropicStarterWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { anthropicStarterWaitDelay = orig })

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	// The background sleep inherits stdout and outlives the killed shell.
	script := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := isolatedStarter(t).Ping(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Ping took %v after the deadline, want it bounded by the wait delay", elapsed)
	}
}

// A timed out turn leaves nothing running: the node process behind an npm
// claude script is killed with it, not left to finish the turn later.
func TestAnthropicStarterPing_TimeoutKillsChildren(t *testing.T) {
	orig := anthropicStarterWaitDelay
	anthropicStarterWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { anthropicStarterWaitDelay = orig })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"$ONWATCH_TEST_PIDFILE\"\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)
	t.Setenv("ONWATCH_TEST_PIDFILE", pidFile)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := isolatedStarter(t).Ping(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid not written: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for processRunning(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d still running after the timed out turn", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// processRunning reports whether pid is a live process. A killed child whose
// new parent has not reaped it yet is a zombie, which counts as gone.
func processRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] != 'Z'
	}
	return true
}

// A CLI that printed a success result but is still held open when the time
// runs out started the session all the same: Ping reports success.
func TestAnthropicStarterPing_SuccessPrintedBeforeDeadline(t *testing.T) {
	orig := anthropicStarterWaitDelay
	anthropicStarterWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { anthropicStarterWaitDelay = orig })

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false}'\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := isolatedStarter(t).Ping(ctx); err != nil {
		t.Fatalf("err = %v, want success: the turn finished before the deadline", err)
	}

	// An error result printed before the deadline is still the deadline.
	script = "#!/bin/sh\necho '{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true}'\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()
	if err := isolatedStarter(t).Ping(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
}

// The environment the CLI actually sees has none of the variables that would
// bill an API key or send the turn to another provider.
func TestAnthropicStarterPing_ChildEnvironment(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(name, "set-by-test")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "env.txt")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nenv > '" + record + "'\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Session started\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)

	if err := isolatedStarter(t).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "set-by-test") {
		t.Fatalf("child environment kept a stripped variable:\n%s", raw)
	}
}

// An npm-installed claude is a `#!/usr/bin/env node` script. Under a daemon's
// minimal PATH, node is found because the CLI's own folder is put on PATH.
func TestAnthropicStarterPing_NpmShimFindsInterpreterBesideIt(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "fakenode")
	if err := os.WriteFile(node, []byte("#!/bin/sh\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Session started\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env fakenode\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_STARTER_CLI_PATH", bin)
	t.Setenv("PATH", "/usr/bin:/bin")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := isolatedStarter(t).Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
