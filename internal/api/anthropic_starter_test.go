package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

func TestAnthropicStarterArgs(t *testing.T) {
	args := anthropicStarterArgs("haiku")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--model haiku", "--output-format json", "--max-turns 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if args[len(args)-1] != anthropicStarterPrompt {
		t.Errorf("last arg = %q, want the prompt", args[len(args)-1])
	}
}

func TestAnthropicStarterModel(t *testing.T) {
	t.Setenv("ANTHROPIC_STARTER_MODEL", "")
	if got := AnthropicStarterModel(); got != "haiku" {
		t.Fatalf("default model = %q, want haiku", got)
	}
	t.Setenv("ANTHROPIC_STARTER_MODEL", "  sonnet  ")
	if got := AnthropicStarterModel(); got != "sonnet" {
		t.Fatalf("override = %q, want sonnet", got)
	}
}

// The child must never be able to bill an API key instead of the
// subscription.
func TestAnthropicStarterEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-ant-api03-secret",
		"anthropic_auth_token=lower",
		"ANTHROPIC_AUTH_TOKEN=bearer",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=0",
		"HOME=/home/u",
		"ANTHROPIC_MODEL=keep",
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=cli",
	}
	out := anthropicStarterEnv(in)
	joined := strings.Join(out, "\n")
	for _, gone := range []string{"sk-ant-api03-secret", "lower", "bearer", "TRAFFIC=0", "CLAUDECODE", "ENTRYPOINT"} {
		if strings.Contains(joined, gone) {
			t.Errorf("env still contains %q:\n%s", gone, joined)
		}
	}
	for _, kept := range []string{"PATH=/usr/bin", "HOME=/home/u", "ANTHROPIC_MODEL=keep", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("env missing %q:\n%s", kept, joined)
		}
	}
	if strings.Count(joined, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") != 1 {
		t.Errorf("non-essential traffic switch should appear once:\n%s", joined)
	}
}

func writeFakeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveClaudeCLIPath(t *testing.T) {
	binName := "claude"
	if runtime.GOOS == "windows" {
		binName = "claude.exe"
	}

	t.Run("override", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), binName)
		writeFakeExecutable(t, p)
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", p)
		got, err := ResolveClaudeCLIPath()
		if err != nil || got != p {
			t.Fatalf("got (%q, %v), want %q", got, err, p)
		}
	})

	t.Run("relative override is made absolute", func(t *testing.T) {
		dir := t.TempDir()
		writeFakeExecutable(t, filepath.Join(dir, "bin", binName))
		t.Chdir(dir)
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", filepath.Join(".", "bin", binName))
		got, err := ResolveClaudeCLIPath()
		if err != nil || !filepath.IsAbs(got) || filepath.Base(got) != binName {
			t.Fatalf("got (%q, %v), want an absolute path to %s", got, err, binName)
		}
		if _, err := os.Stat(got); err != nil {
			t.Fatalf("resolved path does not exist: %v", err)
		}
	})

	t.Run("missing override", func(t *testing.T) {
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", filepath.Join(t.TempDir(), "nope"))
		if _, err := ResolveClaudeCLIPath(); !errors.Is(err, ErrClaudeCLINotFound) || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("err = %v, want ErrClaudeCLINotFound saying it does not exist", err)
		}
	})

	t.Run("override is a folder", func(t *testing.T) {
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", t.TempDir())
		_, err := ResolveClaudeCLIPath()
		if !errors.Is(err, ErrClaudeCLINotFound) || !strings.Contains(err.Error(), "is a folder") {
			t.Fatalf("err = %v, want ErrClaudeCLINotFound saying it is a folder", err)
		}
	})

	t.Run("PATH", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, binName)
		writeFakeExecutable(t, p)
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", "")
		t.Setenv("PATH", dir)
		got, err := ResolveClaudeCLIPath()
		if err != nil || filepath.Clean(got) != filepath.Clean(p) {
			t.Fatalf("got (%q, %v), want %q", got, err, p)
		}
	})

	t.Run("home install location", func(t *testing.T) {
		home := t.TempDir()
		testhome.SetTestHome(t, home)
		t.Setenv("ANTHROPIC_STARTER_CLI_PATH", "")
		t.Setenv("PATH", t.TempDir())
		p := filepath.Join(home, ".claude", "local", binName)
		writeFakeExecutable(t, p)
		got, err := ResolveClaudeCLIPath()
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		// /opt/homebrew or /usr/local may hold a real claude on a developer
		// machine; those are checked after the home locations.
		if got != p {
			t.Fatalf("got %q, want %q", got, p)
		}
	})
}

func TestInterpretAnthropicStarterOutput(t *testing.T) {
	exitErr := errors.New("exit status 1")
	cases := []struct {
		name     string
		stdout   string
		stderr   string
		runErr   error
		wantNil  bool
		wantAuth bool
		contains string
	}{
		{name: "success", stdout: `{"type":"result","subtype":"success","is_error":false,"result":"Session started"}`, wantNil: true},
		{name: "success with trailing newline", stdout: "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\"}\n", wantNil: true},
		{name: "logged out", stdout: `{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Please run /login"}`, runErr: exitErr, wantAuth: true, contains: "/login"},
		{name: "expired oauth on stderr", stderr: "OAuth token has expired. Please obtain a new token", runErr: exitErr, wantAuth: true},
		{name: "error result", stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`, runErr: exitErr, contains: "error_during_execution"},
		{name: "unknown flag", stderr: "error: unknown option '--max-turns'", runErr: exitErr, contains: "unknown option"},
		{name: "exit error, no output", runErr: exitErr, contains: "exit status 1"},
		{name: "exit 0 but not json", stdout: "hello", contains: "hello"},
		{name: "exit 0, empty", contains: "no result"},
		{name: "is_error with exit 0", stdout: `{"type":"result","subtype":"success","is_error":true,"result":"Overloaded"}`, contains: "Overloaded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := interpretAnthropicStarterOutput([]byte(tc.stdout), []byte(tc.stderr), tc.runErr)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("err = nil, want a failure")
			}
			if errors.Is(err, ErrAnthropicStarterAuth) != tc.wantAuth {
				t.Fatalf("auth error = %v, want %v (err %v)", errors.Is(err, ErrAnthropicStarterAuth), tc.wantAuth, err)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("err %q does not mention %q", err, tc.contains)
			}
		})
	}
}

func TestSanitizeStarterDetail(t *testing.T) {
	got := sanitizeStarterDetail("auth failed\nfor sk-ant-oat01-abcdef with\tBearer abc.def.ghi please retry")
	if strings.Contains(got, "sk-ant-oat01") || strings.Contains(got, "abc.def.ghi") || strings.Contains(got, "\n") {
		t.Fatalf("detail not sanitized: %q", got)
	}
	long := sanitizeStarterDetail(strings.Repeat("x", 500))
	if len(long) > 210 || !strings.HasSuffix(long, "...") {
		t.Fatalf("long detail not truncated: %d chars", len(long))
	}
}

func TestAnthropicStarterPing_UsesSeams(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-must-not-leak")
	var gotBin, gotDir string
	var gotArgs, gotEnv []string
	dirExisted := false
	s := &AnthropicStarter{
		resolve: func() (string, error) { return "/fake/claude", nil },
		model:   func() string { return "haiku" },
		run: func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, []byte, error) {
			gotBin, gotArgs, gotEnv, gotDir = bin, args, env, dir
			entries, err := os.ReadDir(dir)
			dirExisted = err == nil && holdsOnlyStarterConfig(entries)
			return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Session started"}`), nil, nil
		},
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotBin != "/fake/claude" || gotArgs[0] != "-p" {
		t.Fatalf("ran %q %v", gotBin, gotArgs)
	}
	if !dirExisted {
		t.Fatal("the CLI did not run in a working directory holding only the starter config")
	}
	if _, err := os.Stat(gotDir); !os.IsNotExist(err) {
		t.Fatalf("working directory %q was not removed", gotDir)
	}
	if strings.Contains(strings.Join(gotEnv, "\n"), "must-not-leak") {
		t.Fatal("ANTHROPIC_API_KEY reached the child")
	}
}

func TestAnthropicStarterPing_Errors(t *testing.T) {
	t.Run("cli missing", func(t *testing.T) {
		s := &AnthropicStarter{
			resolve: func() (string, error) { return "", ErrClaudeCLINotFound },
			model:   func() string { return "haiku" },
			run: func(context.Context, string, []string, []string, string) ([]byte, []byte, error) {
				t.Fatal("ran without a binary")
				return nil, nil, nil
			},
		}
		if err := s.Ping(context.Background()); !errors.Is(err, ErrClaudeCLINotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s := &AnthropicStarter{
			resolve: func() (string, error) { return "/fake/claude", nil },
			model:   func() string { return "haiku" },
			run: func(context.Context, string, []string, []string, string) ([]byte, []byte, error) {
				cancel()
				return nil, nil, errors.New("signal: killed")
			},
		}
		if err := s.Ping(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestNewAnthropicStarter_Model(t *testing.T) {
	t.Setenv("ANTHROPIC_STARTER_MODEL", "")
	if got := NewAnthropicStarter().Model(); got != "haiku" {
		t.Fatalf("Model() = %q", got)
	}
}

func TestLimitedBuffer(t *testing.T) {
	var b bytes.Buffer
	lb := &limitedBuffer{buf: &b, max: 5}
	for _, chunk := range []string{"abc", "defg", "hij"} {
		if n, err := lb.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = (%d, %v), want the full length and no error", chunk, n, err)
		}
	}
	if b.String() != "abcde" {
		t.Fatalf("buffer = %q, want the first 5 bytes", b.String())
	}
}

// The prompt goes through cmd.exe when the CLI is the npm claude.cmd shim on
// Windows, which re-parses quotes and metacharacters.
func TestAnthropicStarterPrompt_IsShellSafe(t *testing.T) {
	if strings.ContainsAny(anthropicStarterPrompt, "\"'`$%^&|<>()!") {
		t.Fatalf("prompt %q contains characters cmd.exe or a shell would re-interpret", anthropicStarterPrompt)
	}
}

func TestAnthropicStarterEnv_StripsOtherBillingRoutes(t *testing.T) {
	env := anthropicStarterEnv([]string{
		"ANTHROPIC_BASE_URL=https://proxy.example",
		"claude_code_use_bedrock=1",
		"CLAUDE_CODE_USE_VERTEX=1",
		"CLAUDE_CODE_USE_FOUNDRY=1",
		"HTTPS_PROXY=http://corp:3128",
	})
	joined := strings.Join(env, "\n")
	for _, gone := range []string{"ANTHROPIC_BASE_URL", "claude_code_use_bedrock", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		if strings.Contains(joined, gone) {
			t.Errorf("env kept %s:\n%s", gone, joined)
		}
	}
	if !strings.Contains(joined, "HTTPS_PROXY=http://corp:3128") {
		t.Errorf("env dropped the network proxy setting:\n%s", joined)
	}
}

// Credentials are redacted wherever they sit: in JSON, after "=" or ":", in
// quotes, or as a JWT, and the length cut never splits a character.
func TestSanitizeStarterDetail_RedactsEmbeddedSecrets(t *testing.T) {
	cases := []struct{ in, secret string }{
		{`{"error":"token=sk-ant-oat01-AbC_d-123 expired"}`, "sk-ant-oat01-AbC_d-123"},
		{`key:"sk-ant-api03-xyz"`, "sk-ant-api03-xyz"},
		{`Authorization: Bearer tok-123,next`, "tok-123"},
		{`authorization: bearer "quoted-token"`, "quoted-token"},
		{`jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1In0.c2ln rejected`, "eyJhbGciOiJIUzI1NiJ9"},
	}
	for _, tc := range cases {
		got := sanitizeStarterDetail(tc.in)
		if strings.Contains(got, tc.secret) {
			t.Errorf("sanitizeStarterDetail(%q) = %q, still contains %q", tc.in, got, tc.secret)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Errorf("sanitizeStarterDetail(%q) = %q, want a [redacted] marker", tc.in, got)
		}
	}
	if got := sanitizeStarterDetail("Invalid API key · Please run /login"); got != "Invalid API key · Please run /login" {
		t.Errorf("plain message changed: %q", got)
	}

	// 199 ASCII bytes then a 2-byte '·': a byte cut at 200 would split it.
	long := sanitizeStarterDetail(strings.Repeat("a", 199) + strings.Repeat("·", 50))
	if !utf8.ValidString(long) {
		t.Fatalf("truncated detail is not valid UTF-8: %q", long)
	}
	if !strings.HasSuffix(long, "...") {
		t.Fatalf("long detail not marked as cut: %q", long)
	}
}

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Claude Code applies its settings files itself, so an apiKeyHelper or an
// API-key env entry there would bill the turn elsewhere even with the
// process environment stripped. Those are detected and named, never echoed.
func TestClaudeSettingsBillingProblem(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"no file content", `{}`, ""},
		{"api key helper", `{"apiKeyHelper":"~/bin/get-key.sh"}`, "apiKeyHelper"},
		{"env api key", `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-secret"}}`, "env ANTHROPIC_API_KEY"},
		{"env auth token", `{"env":{"ANTHROPIC_AUTH_TOKEN":"x"}}`, "env ANTHROPIC_AUTH_TOKEN"},
		{"env base url", `{"env":{"ANTHROPIC_BASE_URL":"https://proxy"}}`, "env ANTHROPIC_BASE_URL"},
		{"env base url is the official endpoint", `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com/"}}`, ""},
		{"bedrock on", `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}`, "env CLAUDE_CODE_USE_BEDROCK"},
		{"bedrock off", `{"env":{"CLAUDE_CODE_USE_BEDROCK":"0"}}`, ""},
		{"vertex false", `{"env":{"CLAUDE_CODE_USE_VERTEX":"false"}}`, ""},
		{"empty key", `{"env":{"ANTHROPIC_API_KEY":""}}`, ""},
		{"unrelated env", `{"env":{"DISABLE_TELEMETRY":"1","ANTHROPIC_MODEL":"opus"}}`, ""},
		{"traffic switch is fine", `{"env":{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":"1"}}`, ""},
		{"not json", `{not json`, ""},
		{"login forced to console", `{"forceLoginMethod":"console"}`, "forceLoginMethod console"},
		{"login forced to subscription", `{"forceLoginMethod":"claudeai"}`, ""},
		{"console login key in .claude.json", `{"numStartups":3,"primaryApiKey":"sk-ant-api03-secret","projects":{}}`, "a Console API key login"},
		{"subscription login state", `{"numStartups":3,"oauthAccount":{"emailAddress":"x"},"projects":{}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSettings(t, tc.body)
			got := claudeSettingsBillingProblem([]string{path})
			if tc.want == "" {
				if got != "" {
					t.Fatalf("problem = %q, want none", got)
				}
				return
			}
			if !strings.HasPrefix(got, tc.want) || !strings.Contains(got, path) {
				t.Fatalf("problem = %q, want %q naming %s", got, tc.want, path)
			}
			if strings.Contains(got, "secret") || strings.Contains(got, "get-key") {
				t.Fatalf("problem %q echoes a setting's value", got)
			}
		})
	}
	if got := claudeSettingsBillingProblem([]string{filepath.Join(t.TempDir(), "missing.json")}); got != "" {
		t.Fatalf("missing file: problem = %q, want none", got)
	}
	// The default file list covers Claude Code's own state file, where a
	// Console login keeps its key, under CLAUDE_CONFIG_DIR too.
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	if files := claudeSettingsFiles(); !slices.Contains(files, filepath.Join(cfgDir, ".claude.json")) {
		t.Fatalf("settings files %v miss .claude.json", files)
	}
	// Managed settings are checked too, after the user's.
	clean := writeSettings(t, `{}`)
	managed := writeSettings(t, `{"apiKeyHelper":"/opt/helper"}`)
	if got := claudeSettingsBillingProblem([]string{clean, managed}); !strings.Contains(got, managed) {
		t.Fatalf("problem = %q, want the managed settings file named", got)
	}
}

// With a billing problem in the settings, Ping refuses before running the CLI.
func TestAnthropicStarterPing_RefusesWhenSettingsBillElsewhere(t *testing.T) {
	path := writeSettings(t, `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-x"}}`)
	ran := false
	s := &AnthropicStarter{
		resolve:       func() (string, error) { return "/fake/claude", nil },
		model:         func() string { return "haiku" },
		settingsFiles: func() []string { return []string{path} },
		run: func(context.Context, string, []string, []string, string) ([]byte, []byte, error) {
			ran = true
			return nil, nil, nil
		},
	}
	err := s.Ping(context.Background())
	if !errors.Is(err, ErrAnthropicStarterNotSubscription) {
		t.Fatalf("err = %v, want ErrAnthropicStarterNotSubscription", err)
	}
	if ran {
		t.Fatal("the CLI ran despite the billing problem")
	}
	if strings.Contains(err.Error(), "sk-ant-api03-x") {
		t.Fatalf("error leaks the key: %v", err)
	}
}

// Every run uses the same folder, emptied first, so Claude Code keeps one
// project entry for the starter, and the folder is gone afterwards.
func TestAnthropicStarterPing_FixedWorkDirIsEmptiedAndRemoved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "onwatch", "claude-starter")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Leftovers, such as a CLAUDE.md, must never be seen by the CLI.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("do things"), 0o600); err != nil {
		t.Fatal(err)
	}
	var dirs []string
	s := &AnthropicStarter{
		resolve: func() (string, error) { return "/fake/claude", nil },
		model:   func() string { return "haiku" },
		workDir: func() string { return dir },
		run: func(_ context.Context, _ string, _ []string, _ []string, d string) ([]byte, []byte, error) {
			entries, err := os.ReadDir(d)
			if err != nil || !holdsOnlyStarterConfig(entries) {
				t.Errorf("run dir %q holds more than the starter config: %v %v", d, entries, err)
			}
			dirs = append(dirs, d)
			return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Session started"}`), nil, nil
		},
	}
	for i := 0; i < 2; i++ {
		if err := s.Ping(context.Background()); err != nil {
			t.Fatalf("Ping %d: %v", i, err)
		}
	}
	if len(dirs) != 2 || dirs[0] != dir || dirs[1] != dir {
		t.Fatalf("run dirs = %v, want %q twice", dirs, dir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("working folder still exists after the run")
	}
}

// The CLI's folder goes in front of PATH so an npm claude script can find
// node under a daemon's minimal PATH; PATH is never duplicated or dropped.
func TestAnthropicStarterPathEnv(t *testing.T) {
	sep := string(os.PathListSeparator)
	bin := filepath.Join(t.TempDir(), "npm-global", "bin", "claude")
	binDir := filepath.Dir(bin)

	out := anthropicStarterPathEnv([]string{"HOME=/h", "PATH=/usr/bin" + sep + "/bin"}, bin)
	var path string
	for _, kv := range out {
		if strings.HasPrefix(kv, "PATH=") {
			if path != "" {
				t.Fatalf("PATH set twice: %v", out)
			}
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	dirs := filepath.SplitList(path)
	if len(dirs) == 0 || dirs[0] != binDir {
		t.Fatalf("PATH = %q, want %q first", path, binDir)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(path, "/usr/bin"+sep+"/bin") {
			t.Fatalf("PATH = %q, want the original entries kept", path)
		}
	} else {
		want := strings.Join([]string{binDir, "/usr/bin", "/bin", "/opt/homebrew/bin", "/usr/local/bin"}, sep)
		if path != want {
			t.Fatalf("PATH = %q, want %q: the CLI folder first, fallbacks after the user's own entries", path, want)
		}
		// A user's node folder already on PATH keeps its place ahead of the
		// fallbacks.
		nvm := anthropicStarterPathEnv([]string{"PATH=/home/u/.nvm/bin" + sep + "/usr/local/bin"}, bin)
		if nvm[0] != "PATH="+strings.Join([]string{binDir, "/home/u/.nvm/bin", "/usr/local/bin", "/opt/homebrew/bin"}, sep) {
			t.Fatalf("PATH = %q", nvm[0])
		}
	}
	if !slices.Contains(out, "HOME=/h") {
		t.Fatalf("other variables dropped: %v", out)
	}

	// Already present: unchanged.
	again := anthropicStarterPathEnv([]string{"PATH=" + path}, bin)
	if again[0] != "PATH="+path {
		t.Fatalf("PATH changed on a second pass: %q", again[0])
	}

	// Present further down: moved to the front once, so a node next to the
	// CLI wins over an older one earlier on PATH.
	later := anthropicStarterPathEnv([]string{"PATH=/old/node/bin" + sep + binDir}, bin)
	if got := filepath.SplitList(strings.TrimPrefix(later[0], "PATH=")); len(got) < 2 || got[0] != binDir || got[1] != "/old/node/bin" || slices.Index(got[1:], binDir) >= 0 {
		t.Fatalf("PATH = %q, want %q moved to the front once", later[0], binDir)
	}

	// No PATH at all: one is added.
	none := anthropicStarterPathEnv([]string{"HOME=/h"}, bin)
	if !strings.HasPrefix(none[len(none)-1], "PATH="+binDir) {
		t.Fatalf("no PATH added: %v", none)
	}
}

// holdsOnlyStarterConfig reports whether a run folder holds exactly the
// starter's own settings and MCP files.
func holdsOnlyStarterConfig(entries []os.DirEntry) bool {
	if len(entries) != len(anthropicStarterConfigFiles) {
		return false
	}
	for _, e := range entries {
		if _, ok := anthropicStarterConfigFiles[e.Name()]; !ok {
			return false
		}
	}
	return true
}

// The starter's config switches off hooks and MCP servers, and the CLI is
// pointed at it by relative name.
func TestAnthropicStarterArgs_DisableHooksAndMCP(t *testing.T) {
	args := strings.Join(anthropicStarterArgs("haiku"), " ")
	for _, want := range []string{
		"--settings " + anthropicStarterSettingsFile,
		"--strict-mcp-config",
		"--mcp-config " + anthropicStarterMCPFile,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	var settings struct {
		DisableAllHooks bool `json:"disableAllHooks"`
		Permissions     struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(anthropicStarterConfigFiles[anthropicStarterSettingsFile]), &settings); err != nil || !settings.DisableAllHooks {
		t.Fatalf("settings file does not disable hooks: %v", err)
	}
	for _, tool := range []string{"Bash", "Edit", "Write", "WebFetch", "Read", "Task"} {
		if !slices.Contains(settings.Permissions.Deny, tool) {
			t.Errorf("settings file does not deny %s: %v", tool, settings.Permissions.Deny)
		}
	}
	// A settings env entry the CLI sets for nested shells is not a billing
	// problem; only the stripped billing names are.
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"env":{"CLAUDECODE":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeSettingsBillingProblem([]string{path}); got != "" {
		t.Errorf("CLAUDECODE in settings env reported as %q", got)
	}
	var mcp struct {
		Servers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(anthropicStarterConfigFiles[anthropicStarterMCPFile]), &mcp); err != nil || mcp.Servers == nil || len(mcp.Servers) != 0 {
		t.Fatalf("MCP file is not an empty server list: %v", err)
	}
	list := anthropicStarterArgs("haiku")
	if list[len(list)-1] != anthropicStarterPrompt {
		t.Fatalf("prompt is not the last argument: %v", list)
	}
	// --mcp-config reads values until the next flag, so a flag must follow
	// its file, never the prompt.
	for i, a := range list {
		if a == "--mcp-config" && (i+2 >= len(list) || !strings.HasPrefix(list[i+2], "--")) {
			t.Fatalf("--mcp-config %s is not followed by a flag: %v", list[i+1], list)
		}
	}
}

// The state file is streamed: keys after a large, deeply nested history are
// still found, other values of any shape are skipped, and a key with an
// unexpected type is ignored rather than failing the read.
func TestReadClaudeBillingSettings_StreamsLargeStateFile(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"numStartups":7,"tips":[1,2,[3,{"a":null}]],"projects":{`)
	for i := 0; i < 20000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"/p/%d":{"history":[{"display":"hello %d","pastedContents":{}}],"allowedTools":[],"ok":true}`, i, i)
	}
	b.WriteString(`},"apiKeyHelper":42,"env":{"ANTHROPIC_API_KEY":"x"},"primaryApiKey":"sk-ant-api03-k","forceLoginMethod":null}`)
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readClaudeBillingSettings(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.PrimaryAPIKey != "sk-ant-api03-k" || got.APIKeyHelper != "" || got.ForceLoginMethod != "" || got.Env["ANTHROPIC_API_KEY"] != "x" {
		t.Fatalf("got %+v", got)
	}
	if problem := claudeSettingsBillingProblem([]string{path}); !strings.HasPrefix(problem, "a Console API key login") {
		t.Fatalf("problem = %q", problem)
	}

	for _, bad := range []string{``, `[]`, `"str"`, `{"a":`, `{"a":[1,2}`} {
		p := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readClaudeBillingSettings(p); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestClaudeSettingsDisplayPath_HidesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	inside := filepath.Join(home, ".claude", "settings.json")
	if got, want := claudeSettingsDisplayPath(inside), filepath.Join("~", ".claude", "settings.json"); got != want {
		t.Fatalf("display path = %q, want %q", got, want)
	}
	outside := filepath.Join(filepath.Dir(home), "managed-settings.json")
	if got := claudeSettingsDisplayPath(outside); got != outside {
		t.Fatalf("display path = %q, want it unchanged", got)
	}
	sibling := home + "x" + string(filepath.Separator) + "settings.json"
	if got := claudeSettingsDisplayPath(sibling); got != sibling {
		t.Fatalf("display path = %q, want it unchanged", got)
	}

	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"primaryApiKey":"sk-ant-api03-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := claudeSettingsBillingProblem([]string{path})
	if strings.Contains(got, home) || !strings.Contains(got, filepath.Join("~", ".claude.json")) || strings.Contains(got, "secret") {
		t.Fatalf("problem = %q, want it to name ~/.claude.json without the home folder or the key", got)
	}
}

// With no cache folder (a service with no HOME), the starter does not fall
// back to a fixed path in the shared temp folder; each run gets its own
// private temp folder, removed afterwards.
func TestAnthropicStarterWorkDir_NoCacheDirUsesPrivateTemp(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cache folder lookup is only cleared this way on Linux")
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if got := anthropicStarterWorkDir(); got != "" {
		t.Fatalf("work dir = %q, want \"\" with no cache folder", got)
	}

	var ran string
	s := &AnthropicStarter{
		resolve: func() (string, error) { return "/fake/claude", nil },
		model:   func() string { return "haiku" },
		workDir: anthropicStarterWorkDir,
		run: func(_ context.Context, _ string, _ []string, _ []string, d string) ([]byte, []byte, error) {
			ran = d
			info, err := os.Stat(d)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Errorf("run dir %q: %v %v, want a 0700 folder", d, info, err)
			}
			return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`), nil, nil
		},
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(ran), "onwatch-claude-starter-") {
		t.Fatalf("run dir = %q, want a fresh temp folder", ran)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("temp folder still exists after the run")
	}
}

// A settings file cut short while Claude Code rewrites it still counts for
// the keys read before the cut.
func TestClaudeSettingsBillingProblem_TruncatedFileStillCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"primaryApiKey":"sk-ant-api03-x","projects":{"/a":{"history":[{"display":"hel`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeSettingsBillingProblem([]string{path}); !strings.HasPrefix(got, "a Console API key login") {
		t.Fatalf("problem = %q, want the Console key found before the cut", got)
	}
	empty := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeSettingsBillingProblem([]string{empty}); got != "" {
		t.Fatalf("problem = %q for an empty file, want none", got)
	}
}

func TestRedactHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	in := "open " + filepath.Join(home, ".cache", "onwatch") + ": permission denied"
	got := RedactHomePath(in)
	if strings.Contains(got, home) || !strings.Contains(got, filepath.Join("~", ".cache", "onwatch")) {
		t.Fatalf("RedactHomePath(%q) = %q", in, got)
	}
	sibling := home + "x" + string(filepath.Separator) + "bin"
	if got := RedactHomePath(sibling); got != sibling {
		t.Fatalf("redacted a folder that only starts like home: %q", got)
	}
	nested := string(filepath.Separator) + "srv" + home
	if got := RedactHomePath(nested); got != nested {
		t.Fatalf("redacted a path that only contains home: %q", got)
	}
	if runtime.GOOS != "windows" {
		if got, want := RedactHomePath("file://"+home+"/x"), "file://~/x"; got != want {
			t.Fatalf("RedactHomePath(file URL) = %q, want %q", got, want)
		}
	}
	if got := RedactHomePath(home); got != "~" {
		t.Fatalf("RedactHomePath(home) = %q, want ~", got)
	}
	two := home + " and " + filepath.Join(home, "a")
	if got, want := RedactHomePath(two), "~ and "+filepath.Join("~", "a"); got != want {
		t.Fatalf("RedactHomePath(%q) = %q, want %q", two, got, want)
	}
	if got := RedactHomePath("no paths here"); got != "no paths here" {
		t.Fatalf("changed a string with no home path: %q", got)
	}
}

// A Windows home folder inside JSON output has its backslashes escaped and
// is still redacted.
func TestRedactHomePathIn_JSONEscaped(t *testing.T) {
	home := `C:\Users\bob`
	escaped := `C:\\Users\\bob`
	in := `{"error":"cannot read ` + escaped + `\\.claude.json"}`
	got := redactHomePathIn(in, escaped, true)
	if strings.Contains(got, "bob") || !strings.Contains(got, `~\\.claude.json`) {
		t.Fatalf("redactHomePathIn(%q) = %q", in, got)
	}
	if got := redactHomePathIn(`c:\users\BOB\x`, home, true); got != `~\x` {
		t.Fatalf("case-folded match = %q, want ~\\x", got)
	}
}

// A long failure detail is redacted before it is cut, so a home folder that
// straddles the cut is not left half visible.
func TestSanitizeStarterDetail_RedactsBeforeTheCut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pad := strings.Repeat("x", 200-len(home)/2)
	got := sanitizeStarterDetail("Error: " + pad + " " + filepath.Join(home, ".claude"))
	if strings.Contains(got, filepath.Dir(home)) || !strings.Contains(got, "~") {
		t.Fatalf("detail kept part of the home folder: %q", got)
	}
}

// A CLI that exits after printing success but leaves a helper holding the
// output open past the wait still counts as a successful turn.
func TestInterpretAnthropicStarterOutput_WaitDelayAfterSuccess(t *testing.T) {
	ok := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"hi"}`)
	if err := interpretAnthropicStarterOutput(ok, nil, exec.ErrWaitDelay); err != nil {
		t.Fatalf("err = %v, want success", err)
	}
	bad := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`)
	if err := interpretAnthropicStarterOutput(bad, nil, exec.ErrWaitDelay); err == nil {
		t.Fatal("a failed turn counted as success")
	}
	if err := interpretAnthropicStarterOutput(nil, nil, exec.ErrWaitDelay); err == nil {
		t.Fatal("no output counted as success")
	}
}

// When the fixed folder cannot be cleared or made, the run uses a fresh temp
// folder instead of failing every ping.
func TestAnthropicStarterPing_UnusableWorkDirFallsBackToTemp(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := filepath.Join(blocker, "claude-starter")
	var ran string
	s := &AnthropicStarter{
		resolve: func() (string, error) { return "/fake/claude", nil },
		model:   func() string { return "haiku" },
		workDir: func() string { return fixed },
		run: func(_ context.Context, _ string, _ []string, _ []string, d string) ([]byte, []byte, error) {
			ran = d
			entries, err := os.ReadDir(d)
			if err != nil || !holdsOnlyStarterConfig(entries) {
				t.Errorf("run dir %q: %v %v", d, entries, err)
			}
			return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`), nil, nil
		},
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if ran == fixed || !strings.HasPrefix(filepath.Base(ran), "onwatch-claude-starter-") {
		t.Fatalf("run dir = %q, want a fresh temp folder", ran)
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Fatal("temp folder still exists after the run")
	}
}

// A turn stopped by --max-turns after the model asked for a tool still sent a
// request, so it counts as having started the session.
func TestInterpretAnthropicStarterOutput_MaxTurnsCountsAsStarted(t *testing.T) {
	out := []byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2}`)
	if err := interpretAnthropicStarterOutput(out, nil, errors.New("exit status 1")); err != nil {
		t.Fatalf("err = %v, want success", err)
	}
	if err := interpretAnthropicStarterOutput([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true}`), nil, nil); err == nil {
		t.Fatal("an execution error counted as success")
	}
}
