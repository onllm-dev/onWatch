package main

import (
	"errors"
	"testing"
)

type fakeSettings struct {
	value string
	err   error
}

func (f fakeSettings) GetSetting(string) (string, error) { return f.value, f.err }

func TestProviderToggleOn(t *testing.T) {
	cases := []struct {
		name     string
		db       fakeSettings
		fallback bool
		want     bool
	}{
		{"on", fakeSettings{value: `{"anthropic":{"auto_start_5h":"on"}}`}, false, true},
		{"off overrides env on", fakeSettings{value: `{"anthropic":{"auto_start_5h":"off"}}`}, true, false},
		{"missing field uses env", fakeSettings{value: `{"anthropic":{"source":"auto"}}`}, true, true},
		{"missing provider uses env", fakeSettings{value: `{"codex":{"auto_start_5h":"on"}}`}, false, false},
		{"empty value uses env", fakeSettings{value: `{"anthropic":{"auto_start_5h":""}}`}, true, true},
		{"no settings", fakeSettings{}, false, false},
		{"read error", fakeSettings{err: errors.New("db closed")}, true, true},
		{"bad json", fakeSettings{value: `{`}, true, true},
		{"non-string value", fakeSettings{value: `{"anthropic":{"auto_start_5h":true}}`}, false, false},
	}
	for _, tc := range cases {
		if got := providerToggleOn(tc.db, "anthropic", "auto_start_5h", tc.fallback); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
