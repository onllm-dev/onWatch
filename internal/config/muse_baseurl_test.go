package config

import "testing"

func TestLoadMuseBaseURLOverride(t *testing.T) {
	t.Setenv("MUSE_BASE_URL", "https://proxy.internal/meta")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MuseBaseURL != "https://proxy.internal/meta" {
		t.Fatalf("MuseBaseURL = %q, want the MUSE_BASE_URL override", cfg.MuseBaseURL)
	}
}

func TestLoadMuseBaseURLDefaultsEmpty(t *testing.T) {
	t.Setenv("MUSE_BASE_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MuseBaseURL != "" {
		t.Fatalf("MuseBaseURL = %q, want empty so the client keeps its default", cfg.MuseBaseURL)
	}
}
