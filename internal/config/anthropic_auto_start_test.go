package config

import "testing"

// The Claude five-hour auto-start is opt-in: unset or anything but "true"
// leaves it off, so an upgrade never turns it on by itself.
func TestLoadAnthropicAutoStart5h(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"false", false},
		{"1", false},
		{"yes", false},
		{"true", true},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("ANTHROPIC_AUTO_START_5H", tc.value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.AnthropicAutoStart5h != tc.want {
				t.Fatalf("AnthropicAutoStart5h = %v, want %v", cfg.AnthropicAutoStart5h, tc.want)
			}
		})
	}
}
