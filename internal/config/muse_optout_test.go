package config

import "testing"

// MUSE_ENABLED=false is an explicit opt-out: main.go skips auto-detect, so the
// provider list must not advertise Muse off the back of local credentials.
func TestLoadMuseExplicitOptOut(t *testing.T) {
	t.Setenv("MUSE_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MuseEnabled {
		t.Error("MuseEnabled = true, want false")
	}
	if !cfg.MuseDisabled {
		t.Error("MuseDisabled = false, want true so the opt-out is distinguishable from 'not configured'")
	}
}

func TestLoadMuseNotDisabledByDefault(t *testing.T) {
	t.Setenv("MUSE_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MuseDisabled {
		t.Error("MuseDisabled = true with no MUSE_ENABLED set, want false")
	}
}
