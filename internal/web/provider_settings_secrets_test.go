package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newProviderSecretTestHandler(t *testing.T) (*Handler, *store.Store) {
	t.Helper()
	t.Setenv("ONWATCH_CREDENTIAL_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return NewHandler(s, nil, nil, nil, &config.Config{}), s
}

func TestProviderSettingsDatabaseNeverReceivesPlaintextSecret(t *testing.T) {
	t.Setenv("ONWATCH_CREDENTIAL_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	dbPath := filepath.Join(t.TempDir(), "onwatch.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil, nil, nil, &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"provider_settings":{"deepseek":{"api_key":"must-not-reach-sqlite"}}}`))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			t.Fatal(readErr)
		}
		if strings.Contains(string(raw), "must-not-reach-sqlite") {
			t.Fatalf("plaintext provider secret found in %s", filepath.Base(path))
		}
	}
}

func TestProviderSettingsEncryptsAndMasksSecrets(t *testing.T) {
	h, s := newProviderSecretTestHandler(t)
	body := `{"provider_settings":{"deepseek":{"api_key":"deepseek-plaintext"},"moonshot":{"api_key":"moonshot-plaintext"}}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "deepseek-plaintext") || strings.Contains(rr.Body.String(), "moonshot-plaintext") || strings.Contains(rr.Body.String(), "v1:") {
		t.Fatalf("PUT response exposed provider secret material: %s", rr.Body.String())
	}

	raw, err := s.GetSetting("provider_settings")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "deepseek-plaintext") || strings.Contains(raw, "moonshot-plaintext") {
		t.Fatalf("provider settings retained plaintext: %s", raw)
	}
	var stored map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"deepseek", "moonshot"} {
		value, _ := stored[provider]["api_key"].(string)
		if !strings.HasPrefix(value, "v1:") {
			t.Fatalf("%s api_key was not encrypted: %q", provider, value)
		}
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getRR := httptest.NewRecorder()
	h.GetSettings(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getRR.Code, getRR.Body.String())
	}
	if strings.Contains(getRR.Body.String(), "deepseek-plaintext") || strings.Contains(getRR.Body.String(), "moonshot-plaintext") || strings.Contains(getRR.Body.String(), "v1:") {
		t.Fatalf("GET response exposed provider secret material: %s", getRR.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(getRR.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	providers := response["provider_settings"].(map[string]interface{})
	for _, provider := range []string{"deepseek", "moonshot"} {
		settings := providers[provider].(map[string]interface{})
		if settings["api_key"] != "" || settings["api_key_set"] != true {
			t.Fatalf("%s secret mask = %#v", provider, settings)
		}
	}
}

func TestProviderSettingsBlankSecretPreservesExistingCiphertext(t *testing.T) {
	h, s := newProviderSecretTestHandler(t)
	first := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"provider_settings":{"deepseek":{"api_key":"keep-me"}}}`))
	firstRR := httptest.NewRecorder()
	h.UpdateSettings(firstRR, first)
	if firstRR.Code != http.StatusOK {
		t.Fatalf("initial PUT status=%d body=%s", firstRR.Code, firstRR.Body.String())
	}
	before, _ := s.GetSetting("provider_settings")

	blank := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"provider_settings":{"deepseek":{"api_key":""}}}`))
	blankRR := httptest.NewRecorder()
	h.UpdateSettings(blankRR, blank)
	if blankRR.Code != http.StatusOK {
		t.Fatalf("blank PUT status=%d body=%s", blankRR.Code, blankRR.Body.String())
	}
	after, _ := s.GetSetting("provider_settings")
	if after != before {
		t.Fatalf("blank secret replaced existing ciphertext\nbefore=%s\nafter=%s", before, after)
	}
}

func TestApplyProviderSettingsDecryptsAndMigratesLegacyPlaintext(t *testing.T) {
	_, s := newProviderSecretTestHandler(t)
	legacy := `{"deepseek":{"api_key":"legacy-deepseek"},"moonshot":{"api_key":"legacy-moonshot"}}`
	if err := s.SetSetting("provider_settings", legacy); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	ApplyProviderSettingsFromDB(s, cfg, nil)
	if cfg.DeepSeekAPIKey != "legacy-deepseek" || cfg.MoonshotAPIKey != "legacy-moonshot" {
		t.Fatalf("legacy secrets were not applied in memory: deepseek=%q moonshot=%q", cfg.DeepSeekAPIKey, cfg.MoonshotAPIKey)
	}
	raw, err := s.GetSetting("provider_settings")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "legacy-deepseek") || strings.Contains(raw, "legacy-moonshot") || !strings.Contains(raw, "v1:") {
		t.Fatalf("legacy provider secrets were not migrated: %s", raw)
	}

	restarted := &config.Config{}
	ApplyProviderSettingsFromDB(s, restarted, nil)
	if restarted.DeepSeekAPIKey != "legacy-deepseek" || restarted.MoonshotAPIKey != "legacy-moonshot" {
		t.Fatalf("encrypted secrets were not applied after restart: deepseek=%q moonshot=%q", restarted.DeepSeekAPIKey, restarted.MoonshotAPIKey)
	}
}

func TestApplyProviderSettingsIgnoresInvalidCiphertext(t *testing.T) {
	_, s := newProviderSecretTestHandler(t)
	if err := s.SetSetting("provider_settings", `{"deepseek":{"api_key":"v1:not-valid"}}`); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DeepSeekAPIKey: "environment-fallback"}
	ApplyProviderSettingsFromDB(s, cfg, nil)
	if cfg.DeepSeekAPIKey != "environment-fallback" {
		t.Fatalf("invalid ciphertext replaced safe config fallback: %q", cfg.DeepSeekAPIKey)
	}
}
