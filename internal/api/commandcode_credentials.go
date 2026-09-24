package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// CommandCodeCredentials holds a resolved Command Code credential.
type CommandCodeCredentials struct {
	APIKey string // Bearer key for the Command Code alpha API
	Source string // "env", "commandcode-auth", "pi-auth", "omp-auth"
}

// commandCodeEnvKeys are the environment variables that hold an explicit key.
// Both spellings are accepted because the CLI, the pi provider and the docs
// are inconsistent: the vendor uses COMMAND_CODE_*, the reference provider
// also reads COMMANDCODE_*.
var commandCodeEnvKeys = []string{"COMMAND_CODE_API_KEY", "COMMANDCODE_API_KEY"}

// CommandCodeAuthPath returns an explicit auth-file override, or "" to use the
// default search list. COMMANDCODE_AUTH_PATH exists for non-standard layouts
// and for tests.
func CommandCodeAuthPath() string {
	return strings.TrimSpace(os.Getenv("COMMANDCODE_AUTH_PATH"))
}

// commandCodeAuthPaths returns the credential files to probe, in priority
// order. The Command Code CLI's own store wins over the pi / OMP agent stores,
// which cache the same credential under a provider key.
func commandCodeAuthPaths() []struct{ path, source string } {
	if override := CommandCodeAuthPath(); override != "" {
		return []struct{ path, source string }{{override, "commandcode-auth"}}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []struct{ path, source string }{
		{filepath.Join(home, ".commandcode", "auth.json"), "commandcode-auth"},
		{filepath.Join(home, ".pi", "agent", "auth.json"), "pi-auth"},
		{filepath.Join(home, ".omp", "agent", "auth.json"), "omp-auth"},
	}
}

// commandCodeCredentialKey extracts the key from one credential entry. The
// entry is either a bare string key or an object such as
// {"type":"oauth","access":"user_..."} or {"type":"api","key":"user_..."}.
func commandCodeCredentialKey(value any) string {
	switch v := value.(type) {
	case string:
		return cleanCommandCodeKey(v)
	case map[string]any:
		if key := stringField(v, "access"); key != "" {
			return key
		}
		return stringField(v, "key")
	}
	return ""
}

// stringField reads a trimmed, whitespace-free string field from a decoded
// JSON object.
func stringField(obj map[string]any, name string) string {
	s, ok := obj[name].(string)
	if !ok {
		return ""
	}
	return cleanCommandCodeKey(s)
}

// cleanCommandCodeKey rejects empty values and anything holding whitespace,
// which would mean the file stored something other than a token.
func cleanCommandCodeKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n\r") {
		return ""
	}
	return s
}

// readCommandCodeAuthFile reads one auth file and returns the Command Code key
// it holds, or "".
//
// The recognised layouts are the ones the CLI and the pi provider write:
//
//	{"apiKey":"user_..."}                          (Command Code CLI)
//	{"commandcode":"user_..."}
//	{"commandcode":{"type":"oauth","access":"..."}}
//	{"command-code":{"type":"api","key":"..."}}    (pi / OMP agent store)
func readCommandCodeAuthFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if !commandCodeAuthFilePermsOK(info.Mode().Perm()) {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var top map[string]any
	if json.Unmarshal(data, &top) != nil {
		return ""
	}
	for _, field := range []string{"apiKey", "commandcode", "command-code"} {
		raw, ok := top[field]
		if !ok {
			continue
		}
		if key := commandCodeCredentialKey(raw); key != "" {
			return key
		}
	}
	return ""
}

// DetectCommandCodeCredentials resolves the API key without logging it.
// An explicit environment variable always wins over the on-disk stores.
func DetectCommandCodeCredentials(logger *slog.Logger) *CommandCodeCredentials {
	if logger == nil {
		logger = slog.Default()
	}
	for _, env := range commandCodeEnvKeys {
		if key := cleanCommandCodeKey(os.Getenv(env)); key != "" {
			return &CommandCodeCredentials{APIKey: key, Source: "env"}
		}
	}
	for _, candidate := range commandCodeAuthPaths() {
		if key := readCommandCodeAuthFile(candidate.path); key != "" {
			logger.Info("commandcode: API key detected from auth file", "source", candidate.source)
			return &CommandCodeCredentials{APIKey: key, Source: candidate.source}
		}
	}
	return nil
}
