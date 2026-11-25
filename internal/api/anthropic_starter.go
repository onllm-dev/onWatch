package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// Anthropic auto quota-starter (Beta, issue #162).
//
// Claude's five-hour session window only begins with the first message after
// the previous one expires. The starter sends that first message for the user
// by running their own Claude Code CLI once in print mode:
//
//	claude -p --model haiku --output-format json --max-turns 1 "<prompt>"
//
// Running the official CLI, rather than calling the Messages API directly with
// the OAuth token, means the request is an ordinary Claude Code turn on the
// user's own login: it counts toward the same subscription quota onWatch
// tracks, uses Claude Code's own client identity and token refresh, and never
// bills the API. The child gets an empty working directory so no project
// CLAUDE.md or settings are loaded, and ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN
// are removed from its environment so it cannot fall back to API-key billing.

// defaultAnthropicStarterModel is the Claude Code model alias for the starter
// turn. "haiku" is the lightest model every Claude plan can use.
const defaultAnthropicStarterModel = "haiku"

// anthropicStarterPrompt asks for a reply of a few tokens. It has no quotes or
// shell metacharacters, so it passes unchanged through claude.cmd on Windows,
// where cmd.exe re-parses the arguments.
const anthropicStarterPrompt = `Reply with only these two words: Session started`

var (
	// ErrClaudeCLINotFound means no Claude Code CLI binary could be located.
	ErrClaudeCLINotFound = errors.New("anthropic starter: Claude Code CLI (claude) not found - install Claude Code or set ANTHROPIC_STARTER_CLI_PATH")
	// ErrAnthropicStarterAuth means Claude Code is not logged in or its login
	// was rejected. The user has to log in again with `claude`.
	ErrAnthropicStarterAuth = errors.New("anthropic starter: Claude Code is not logged in - run `claude` and log in")
	// ErrAnthropicStarterNotSubscription means Claude Code's own settings send
	// requests to an API key or another provider, so a starter turn would be
	// billed there and would not start the subscription's five-hour session.
	ErrAnthropicStarterNotSubscription = errors.New("anthropic starter: Claude Code is set up to use an API key or another provider, so the turn would not start your subscription session")
)

// AnthropicStarterModel returns the model used for the starter turn,
// allowing an ANTHROPIC_STARTER_MODEL override.
func AnthropicStarterModel() string {
	if m := strings.TrimSpace(os.Getenv("ANTHROPIC_STARTER_MODEL")); m != "" {
		return m
	}
	return defaultAnthropicStarterModel
}

// The starter turn must not run the user's own Claude Code extras: hooks
// (a Stop hook that sends a phone notification would fire for a turn the
// user never made) or MCP servers (each would start a process). These two
// files, written into the working folder, switch them off. They are passed by
// relative name because the CLI runs in that folder: a full path could hold
// spaces, which the npm claude.cmd shim on Windows would re-parse.
const (
	anthropicStarterSettingsFile = "onwatch-starter-settings.json"
	anthropicStarterMCPFile      = "onwatch-starter-mcp.json"
)

var anthropicStarterConfigFiles = map[string]string{
	// Hooks are off, and every built-in tool is denied (a name a CLI version
	// does not know is ignored), so a user CLAUDE.md that prompts a tool call
	// cannot run anything unattended. Deny rules win over the user's allow
	// list.
	anthropicStarterSettingsFile: `{"disableAllHooks":true,"permissions":{"deny":["Bash","BashOutput","KillShell","Edit","MultiEdit","Write","NotebookEdit","Read","Glob","Grep","LS","WebFetch","WebSearch","Task","TodoWrite","SlashCommand","Skill"]}}`,
	anthropicStarterMCPFile:      `{"mcpServers":{}}`,
}

// anthropicStarterArgs are the CLI arguments for one starter turn.
func anthropicStarterArgs(model string) []string {
	// --mcp-config takes a list, so another flag must follow it: the prompt
	// right after it would be read as a second config.
	return []string{
		"-p",
		"--mcp-config", anthropicStarterMCPFile,
		"--strict-mcp-config",
		"--settings", anthropicStarterSettingsFile,
		"--model", model,
		"--output-format", "json",
		"--max-turns", "1",
		anthropicStarterPrompt,
	}
}

// anthropicStarterStrippedEnv are removed from the child's environment because
// each would send the turn somewhere other than the user's Claude
// subscription: an API key or token billed per request, a different API
// endpoint, or a cloud provider (Bedrock, Vertex, Foundry). None of those
// would start the five-hour window onWatch is tracking.
var anthropicStarterStrippedEnv = map[string]bool{
	"ANTHROPIC_API_KEY":                        true,
	"ANTHROPIC_AUTH_TOKEN":                     true,
	"ANTHROPIC_BASE_URL":                       true,
	"CLAUDE_CODE_USE_BEDROCK":                  true,
	"CLAUDE_CODE_USE_VERTEX":                   true,
	"CLAUDE_CODE_USE_FOUNDRY":                  true,
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": true,
}

// anthropicStarterNestedEnv are set by Claude Code in the shells it runs. An
// onWatch started from such a shell would pass them on, and the CLI then takes
// the starter turn for a session nested in another and refuses to run it.
var anthropicStarterNestedEnv = map[string]bool{
	"CLAUDECODE":             true,
	"CLAUDE_CODE_ENTRYPOINT": true,
}

// anthropicStarterEnv returns env without the variables in
// anthropicStarterStrippedEnv, plus the switch that turns off Claude Code's
// non-essential traffic for this one-shot run.
func anthropicStarterEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if anthropicStarterStrippedEnv[strings.ToUpper(name)] || anthropicStarterNestedEnv[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
}

// anthropicStarterPathEnv puts the CLI's own folder at the front of PATH and,
// on macOS and Linux, the usual Homebrew folders at the end. A daemon under
// launchd or systemd runs with a minimal PATH, and an npm-installed claude is
// a script that starts with `#!/usr/bin/env node`: without this, a CLI found
// in a fallback location could not find node and every ping would fail. The
// CLI's folder goes first because npm usually installs claude in the same
// folder as the node that installed it, so that node is the one that runs it. The
// Homebrew fallbacks go last so they never push ahead of the user's PATH.
func anthropicStarterPathEnv(env []string, bin string) []string {
	front := []string{filepath.Dir(bin)}
	var back []string
	if runtime.GOOS != "windows" {
		back = []string{"/opt/homebrew/bin", "/usr/local/bin"}
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 || !strings.EqualFold(kv[:i], "PATH") || found {
			out = append(out, kv)
			continue
		}
		found = true
		out = append(out, kv[:i+1]+extendPathList(kv[i+1:], front, back))
	}
	if !found {
		out = append(out, "PATH="+extendPathList("", front, back))
	}
	return out
}

// extendPathList puts front dirs first, moving any that path already has, and
// adds back dirs after path, skipping any it already has.
func extendPathList(path string, front, back []string) string {
	moved := map[string]bool{}
	for _, d := range front {
		moved[d] = true
	}
	existing := map[string]bool{}
	var current []string
	for _, d := range filepath.SplitList(path) {
		if moved[d] && d != "" && d != "." {
			continue
		}
		existing[d] = true
		current = append(current, d)
	}
	pick := func(dirs []string) []string {
		var picked []string
		for _, d := range dirs {
			if d == "" || d == "." || existing[d] {
				continue
			}
			existing[d] = true
			picked = append(picked, d)
		}
		return picked
	}
	all := append(pick(front), current...)
	all = append(all, pick(back)...)
	return strings.Join(all, string(os.PathListSeparator))
}

// ResolveClaudeCLIPath finds the Claude Code CLI: ANTHROPIC_STARTER_CLI_PATH,
// then PATH, then the usual install locations (a daemon started by launchd or
// systemd often runs with a minimal PATH).
func ResolveClaudeCLIPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ANTHROPIC_STARTER_CLI_PATH")); override != "" {
		// The CLI runs in the starter's own folder, where a relative path
		// would point somewhere else, so make it absolute here.
		if abs, err := filepath.Abs(override); err == nil {
			override = abs
		}
		fi, err := os.Stat(override)
		if err == nil && !fi.IsDir() {
			return override, nil
		}
		problem := "does not exist"
		if err == nil {
			problem = "is a folder, not the claude program"
		}
		// %s, not %q: quoting would double a Windows path's backslashes and
		// hide the home folder from RedactHomePath.
		return "", fmt.Errorf("%w (ANTHROPIC_STARTER_CLI_PATH %s %s)", ErrClaudeCLINotFound, override, problem)
	}

	// LookPath("claude") also finds claude.exe and the npm claude.cmd shim on
	// Windows, through PATHEXT.
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}

	binName := "claude"
	if runtime.GOOS == "windows" {
		binName = "claude.exe"
	}

	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", binName),
			filepath.Join(home, ".claude", "local", binName),
			filepath.Join(home, ".npm-global", "bin", binName),
		)
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(home, "AppData", "Roaming", "npm", "claude.cmd"))
		}
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/opt/homebrew/bin/claude", "/usr/local/bin/claude", "/usr/bin/claude")
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", ErrClaudeCLINotFound
}

// anthropicStarterWaitDelay is how long Run waits for the output pipes to
// close after the CLI was killed. A variable so tests can shorten it.
var anthropicStarterWaitDelay = 5 * time.Second

// claudeSettingsFiles lists the Claude Code settings files that apply to a run
// outside any project: the user's settings and the machine-wide managed
// settings. Project settings do not apply, because the turn runs in an empty
// folder.
func claudeSettingsFiles() []string {
	var files []string
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		files = append(files, filepath.Join(dir, "settings.json"), filepath.Join(dir, ".claude.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		// ~/.claude.json is Claude Code's own state, where a Console
		// login keeps its API key.
		files = append(files, filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json"))
	}
	switch runtime.GOOS {
	case "darwin":
		files = append(files, "/Library/Application Support/ClaudeCode/managed-settings.json")
	case "windows":
		files = append(files,
			`C:\ProgramData\ClaudeCode\managed-settings.json`,
			`C:\Program Files\ClaudeCode\managed-settings.json`)
	default:
		files = append(files, "/etc/claude-code/managed-settings.json")
	}
	return files
}

// claudeSettingsBillingProblem returns which setting in the given Claude Code
// settings files would send a starter turn somewhere other than the user's
// subscription (an apiKeyHelper, an env entry such as ANTHROPIC_API_KEY, a
// login forced to the Console, or a Console API key saved by `claude` login),
// or "" when none does. Stripping the process environment cannot undo these,
// because Claude Code applies them itself. Values are never returned, only
// names. A missing or unreadable file counts as no problem.
func claudeSettingsBillingProblem(files []string) string {
	for _, path := range files {
		// A file cut short (Claude Code rewriting it) still counts for the
		// keys read before the cut; only a file that yields nothing is
		// skipped.
		settings, _ := readClaudeBillingSettings(path)
		where := claudeSettingsDisplayPath(path)
		if strings.TrimSpace(settings.APIKeyHelper) != "" {
			return "apiKeyHelper in " + where
		}
		if strings.EqualFold(strings.TrimSpace(settings.ForceLoginMethod), "console") {
			return "forceLoginMethod console in " + where
		}
		if strings.TrimSpace(settings.PrimaryAPIKey) != "" {
			return "a Console API key login in " + where
		}
		for name, value := range settings.Env {
			upper := strings.ToUpper(name)
			if !anthropicStarterStrippedEnv[upper] || upper == "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC" {
				continue
			}
			v := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			if v == "" || v == "0" || v == "false" || v == "<nil>" {
				continue
			}
			// The official endpoint is where a subscription turn goes anyway.
			if upper == "ANTHROPIC_BASE_URL" && strings.TrimRight(v, "/") == "https://api.anthropic.com" {
				continue
			}
			return "env " + name + " in " + where
		}
	}
	return ""
}

// RedactHomePath replaces the home folder in s with ~, so a starter failure
// sent to a webhook does not carry the account's username. Only a whole
// folder name is replaced (the home folder followed by a separator or the
// end), and on Windows letter case is ignored.
func RedactHomePath(s string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	home = strings.TrimRight(home, `/\`)
	if len(home) < 2 {
		return s
	}
	foldCase := runtime.GOOS == "windows"
	s = redactHomePathIn(s, home, foldCase)
	// A path inside JSON output has each backslash escaped (C:\\Users\\bob).
	if escaped := strings.ReplaceAll(home, `\`, `\\`); escaped != home {
		s = redactHomePathIn(s, escaped, foldCase)
	}
	return s
}

// redactHomePathIn replaces each whole-folder occurrence of home in s with ~.
func redactHomePathIn(s, home string, foldCase bool) string {
	same := func(a, b string) bool { return a == b }
	if foldCase {
		same = strings.EqualFold
	}
	var b strings.Builder
	last := 0
	for i := 0; i+len(home) <= len(s); i++ {
		end := i + len(home)
		if !same(s[i:end], home) {
			continue
		}
		// Whole folder name only: not /home/al inside /home/alice. (The
		// home path starts with a separator, so /srv/home/al matches only
		// where /home/al is itself a path.)
		if i > 0 && isPathNameByte(s[i-1]) {
			continue
		}
		if end == len(s) || !isPathNameByte(s[end]) {
			b.WriteString(s[last:i])
			b.WriteString("~")
			last = end
			i = end - 1
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// isPathNameByte reports whether c can continue a file or folder name.
func isPathNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '-' || c == '_' || c >= 0x80
}

// claudeSettingsDisplayPath shortens a path under the home folder to ~, so a
// reason that ends up in a notification or webhook does not carry the
// account's username.
func claudeSettingsDisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join("~", rel)
}

// claudeBillingSettings are the Claude Code settings that decide where a turn
// is billed.
type claudeBillingSettings struct {
	APIKeyHelper     string
	Env              map[string]any
	ForceLoginMethod string
	PrimaryAPIKey    string
}

// readClaudeBillingSettings reads only the billing keys from a Claude Code
// settings or state file. ~/.claude.json holds every project's history and
// can run to many megabytes, so the file is streamed and every other value
// is skipped token by token, never held in memory whole.
func readClaudeBillingSettings(path string) (claudeBillingSettings, error) {
	var out claudeBillingSettings
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReader(f))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return out, fmt.Errorf("%s: not a JSON object", path)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		key, _ := tok.(string)
		switch key {
		case "apiKeyHelper":
			err = decodeLenient(dec, &out.APIKeyHelper)
		case "forceLoginMethod":
			err = decodeLenient(dec, &out.ForceLoginMethod)
		case "primaryApiKey":
			err = decodeLenient(dec, &out.PrimaryAPIKey)
		case "env":
			err = decodeLenient(dec, &out.Env)
		default:
			err = skipJSONValue(dec)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// decodeLenient decodes the next value into v, leaving v unset when the value
// has another type (a null, or a number where a string was expected).
func decodeLenient(dec *json.Decoder, v any) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	_ = json.Unmarshal(raw, v)
	return nil
}

// skipJSONValue consumes the next value, however deeply nested, one token at
// a time.
func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

// anthropicStarterWorkDir is the folder the CLI runs in. It is a fixed path,
// emptied before and removed after each run: fixed so Claude Code files every
// starter turn under one project instead of a new one per run, and empty but
// for the starter's own config so no project CLAUDE.md, settings or .mcp.json
// is picked up. User-level hooks and MCP servers are switched off by flags
// (see anthropicStarterArgs); a user-level CLAUDE.md is still read, which
// only adds to the turn's input.
//
// It returns "" when the user has no cache folder (a service with no HOME),
// and the run then uses a fresh private temp folder: a fixed path in the
// shared temp folder could be created, or pointed elsewhere, by another user.
func anthropicStarterWorkDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "onwatch", "claude-starter")
}

// anthropicStarterResult is the part of `claude -p --output-format json`
// output the starter reads.
type anthropicStarterResult struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// anthropicStarterRunFunc runs the CLI and returns stdout, stderr and the
// exit error. A seam so tests never spawn Claude Code.
type anthropicStarterRunFunc func(ctx context.Context, bin string, args, env []string, dir string) (stdout, stderr []byte, err error)

func runAnthropicStarterCLI(ctx context.Context, bin string, args, env []string, dir string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &stdout, max: 1 << 16}
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 1 << 14}
	// When ctx ends, the CLI and every process it started are killed. A
	// child that escaped the kill can still keep the output pipes open, so
	// WaitDelay stops Run from waiting on those pipes forever.
	cmd.WaitDelay = anthropicStarterWaitDelay
	setAnthropicStarterTreeKill(cmd)
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// limitedBuffer keeps at most max bytes and discards the rest, so a chatty
// child cannot grow onWatch's memory.
type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

// AnthropicStarter sends the starter turn through the Claude Code CLI.
type AnthropicStarter struct {
	resolve       func() (string, error)
	run           anthropicStarterRunFunc
	model         func() string
	settingsFiles func() []string
	workDir       func() string
}

// NewAnthropicStarter returns a starter that runs the real Claude Code CLI.
func NewAnthropicStarter() *AnthropicStarter {
	return &AnthropicStarter{
		resolve:       ResolveClaudeCLIPath,
		run:           runAnthropicStarterCLI,
		model:         AnthropicStarterModel,
		settingsFiles: claudeSettingsFiles,
		workDir:       anthropicStarterWorkDir,
	}
}

// Model returns the model the next starter turn will use.
func (s *AnthropicStarter) Model() string { return s.model() }

// Ping runs one minimal Claude Code turn. It returns nil only when the CLI
// reports a completed, successful turn. ctx bounds the whole run.
func (s *AnthropicStarter) Ping(ctx context.Context) error {
	bin, err := s.resolve()
	if err != nil {
		return err
	}
	if s.settingsFiles != nil {
		if problem := claudeSettingsBillingProblem(s.settingsFiles()); problem != "" {
			return fmt.Errorf("%w (%s)", ErrAnthropicStarterNotSubscription, problem)
		}
	}

	var dir string
	if s.workDir != nil {
		dir = s.workDir()
	}
	// The fixed folder may not clear: on Windows a process left from a timed
	// out run can still hold it open. Then this run uses a fresh temp folder
	// rather than fail every ping until that process exits.
	if dir != "" && (os.RemoveAll(dir) != nil || os.MkdirAll(dir, 0o700) != nil) {
		dir = ""
	}
	if dir == "" {
		dir, err = os.MkdirTemp("", "onwatch-claude-starter-")
		if err != nil {
			return fmt.Errorf("anthropic starter: temp dir: %w", err)
		}
	}
	defer os.RemoveAll(dir)
	for name, content := range anthropicStarterConfigFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("anthropic starter: write %s: %w", name, err)
		}
	}

	stdout, stderr, runErr := s.run(ctx, bin, anthropicStarterArgs(s.model()), anthropicStarterPathEnv(anthropicStarterEnv(os.Environ()), bin), dir)
	if ctx.Err() != nil {
		// A turn that printed its result just as the time ran out (or while
		// a helper held the output open) still started the session.
		if anthropicStarterOutputShowsStart(stdout) {
			return nil
		}
		return ctx.Err()
	}
	return interpretAnthropicStarterOutput(stdout, stderr, runErr)
}

// anthropicStarterOutputShowsStart reports whether the CLI printed a result
// that means the turn reached the model: a success, or a turn stopped by
// --max-turns.
func anthropicStarterOutputShowsStart(stdout []byte) bool {
	var res anthropicStarterResult
	if json.Unmarshal(bytes.TrimSpace(stdout), &res) != nil || res.Type != "result" {
		return false
	}
	return (!res.IsError && res.Subtype == "success") || res.Subtype == "error_max_turns"
}

// interpretAnthropicStarterOutput turns the CLI's output into an error, or nil
// for a successful turn.
func interpretAnthropicStarterOutput(stdout, stderr []byte, runErr error) error {
	var res anthropicStarterResult
	parsed := json.Unmarshal(bytes.TrimSpace(stdout), &res) == nil && res.Type == "result"

	// ErrWaitDelay means the CLI exited but something it started held the
	// output open past the wait; a success result it printed still counts.
	if parsed && (runErr == nil || errors.Is(runErr, exec.ErrWaitDelay)) && !res.IsError && res.Subtype == "success" {
		return nil
	}
	// error_max_turns means the model answered with a tool call (a user
	// CLAUDE.md can prompt one) and --max-turns 1 stopped the turn there. The
	// request reached the model, so the session started all the same.
	if parsed && res.Subtype == "error_max_turns" {
		return nil
	}

	detail := ""
	if parsed {
		detail = res.Result
		if detail == "" {
			detail = res.Subtype
		}
	}
	if detail == "" {
		detail = string(stderr)
	}
	if detail == "" && !parsed {
		detail = string(stdout)
	}
	detail = sanitizeStarterDetail(detail)

	if looksLikeClaudeLoginError(detail) {
		return fmt.Errorf("%w: %s", ErrAnthropicStarterAuth, detail)
	}
	switch {
	case detail != "" && runErr != nil:
		return fmt.Errorf("anthropic starter: claude exited with %v: %s", runErr, detail)
	case runErr != nil:
		return fmt.Errorf("anthropic starter: claude exited with %v", runErr)
	case detail != "":
		return fmt.Errorf("anthropic starter: turn did not succeed: %s", detail)
	default:
		return errors.New("anthropic starter: claude returned no result")
	}
}

func looksLikeClaudeLoginError(detail string) bool {
	d := strings.ToLower(detail)
	for _, marker := range []string{"/login", "not logged in", "please log in", "invalid api key", "oauth token has expired", "authentication_error"} {
		if strings.Contains(d, marker) {
			return true
		}
	}
	return false
}

// starterSecretPatterns match credentials wherever they appear in CLI output:
// Anthropic keys and OAuth tokens (sk-ant-...), JWTs, and the value after
// "Bearer". Matching inside quotes, after "=" or ":", or in JSON matters,
// because the detail ends up in logs and webhooks.
var starterSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]+`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+(\.[A-Za-z0-9_\-]+)?`),
	regexp.MustCompile(`(?i)(bearer\s+["']?)[^\s"',]+`),
}

// sanitizeStarterDetail keeps a failure reason short and on one line for logs
// and notifications, and drops anything that looks like a credential.
func sanitizeStarterDetail(s string) string {
	// Before the cut below, so a home folder cut mid-name is never left
	// for a later redaction to miss.
	s = RedactHomePath(s)
	s = strings.Join(strings.Fields(s), " ")
	for i, re := range starterSecretPatterns {
		if i == len(starterSecretPatterns)-1 {
			s = re.ReplaceAllString(s, "${1}[redacted]")
		} else {
			s = re.ReplaceAllString(s, "[redacted]")
		}
	}
	const max = 200
	if len(s) > max {
		// Cut on a character boundary so a multi-byte rune is never split.
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}
