package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MuseCredentials holds a resolved Muse coding-plan credential.
type MuseCredentials struct {
	APIKey string // Bearer [REDACTED] the Meta Model API
	Model  string // model used for the usage probe
	Source string // "env", "muse-auth", "keychain", "keyring"
}

// MuseAuthFilePath returns the native Muse login file, honouring
// MUSE_AUTH_PATH for non-standard layouts.
func MuseAuthFilePath() string {
	if p := strings.TrimSpace(os.Getenv("MUSE_AUTH_PATH")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "muse", "auth.json")
	}
	return filepath.Join(home, ".config", "muse", "auth.json")
}

// MuseSettingsPath returns the native Muse settings file (probe model source).
func MuseSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "muse", "settings.json")
	}
	return filepath.Join(home, ".config", "muse", "settings.json")
}

// ResolveMuseModel picks the probe model: META_MUSE_MODEL wins, then the
// model in the native Muse settings file, then DefaultMuseModel.
func ResolveMuseModel() string {
	if m := strings.TrimSpace(os.Getenv("META_MUSE_MODEL")); m != "" {
		return m
	}
	if path := MuseSettingsPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var settings struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(data, &settings) == nil {
				if m := strings.TrimSpace(settings.Model); m != "" {
					return m
				}
			}
		}
	}
	return DefaultMuseModel
}

// DetectMuseCredentials resolves the Muse API key without logging it.
// META_API_KEY always takes priority over the `muse login` session.
func DetectMuseCredentials(logger *slog.Logger) *MuseCredentials {
	if logger == nil {
		logger = slog.Default()
	}
	if key := strings.TrimSpace(os.Getenv("META_API_KEY")); key != "" {
		if strings.ContainsAny(key, " \t\n\r") {
			logger.Warn("muse: META_API_KEY looks invalid; ignoring")
		} else {
			return &MuseCredentials{APIKey: key, Model: ResolveMuseModel(), Source: "env"}
		}
	}
	if key := readMuseAuthFileKey(); key != "" {
		logger.Info("muse: API key detected from Muse login file")
		return &MuseCredentials{APIKey: key, Model: ResolveMuseModel(), Source: "muse-auth"}
	}
	if key, source := readMusePlatformKey(logger); key != "" {
		logger.Info("muse: API key detected from system credential store", "source", source)
		return &MuseCredentials{APIKey: key, Model: ResolveMuseModel(), Source: source}
	}
	return nil
}

// readMuseAuthFileKey reads providers.meta.api_key from the Muse login file.
// The file must not be group/other-accessible; the key is never logged.
func readMuseAuthFileKey() string {
	path := MuseAuthFilePath()
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.Mode().Perm()&0o077 != 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var auth struct {
		Providers map[string]struct {
			APIKey string `json:"api_key"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return ""
	}
	key := strings.TrimSpace(auth.Providers["meta"].APIKey)
	if key == "" || strings.Contains(key, " ") {
		return ""
	}
	return key
}

// museKeychainPayload is the JSON stored by `muse login` in the system
// credential store (service ai.meta.dev.credentials, account meta).
type museKeychainPayload struct {
	APIKey      string `json:"api_key"`
	AccessToken string `json:"access_token"`
}

// musePlatformKey picks the long-lived key, preferring api_key over the
// short-lived access token.
func musePlatformKey(payload string) string {
	var p museKeychainPayload
	if json.Unmarshal([]byte(payload), &p) != nil {
		if key := strings.TrimSpace(payload); key != "" && !strings.Contains(key, " ") {
			return key
		}
		return ""
	}
	if key := strings.TrimSpace(p.APIKey); key != "" {
		return key
	}
	return strings.TrimSpace(p.AccessToken)
}
