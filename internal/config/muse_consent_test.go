package config

import "testing"

// Every Muse poll spends a prompt from the user's own 5h window, so a key found
// by auto-detection must not start polling on its own.
func TestHasProviderMuseRequiresConsent(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"nothing configured", Config{}, false},
		{"auto-detected key only", Config{MuseAPIKey: "detected"}, false},
		{"explicit enable", Config{MuseEnabled: true}, true},
		{"explicit key and enable", Config{MuseAPIKey: "k", MuseEnabled: true}, true},
		{"opt-out beats enable", Config{MuseEnabled: true, MuseDisabled: true}, false},
		{"opt-out beats an explicit key", Config{MuseAPIKey: "k", MuseEnabled: true, MuseDisabled: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.HasProvider("muse"); got != tt.want {
				t.Fatalf("HasProvider(muse) = %v, want %v", got, tt.want)
			}
		})
	}
}

// An explicit META_API_KEY is consent, so Load must still enable tracking.
func TestLoadMuseExplicitKeyEnables(t *testing.T) {
	t.Setenv("META_API_KEY", "explicit-key")
	t.Setenv("MUSE_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.HasProvider("muse") {
		t.Fatal("an explicit META_API_KEY must enable Muse")
	}
}

// MUSE_ENABLED=false must win even when a key is present.
func TestLoadMuseOptOutBeatsKey(t *testing.T) {
	t.Setenv("META_API_KEY", "explicit-key")
	t.Setenv("MUSE_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HasProvider("muse") {
		t.Fatal("MUSE_ENABLED=false must disable Muse even with META_API_KEY set")
	}
}
