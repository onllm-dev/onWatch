package web

import "testing"

func TestTitleCaseWords(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"pro", "Pro"},
		{"PRO", "Pro"},
		{"team plan", "Team Plan"},
		{"  spaced   out  ", "Spaced Out"},
		// A multi-byte first rune must not be split mid-UTF-8.
		{"éclair plan", "Éclair Plan"},
		{"日本 plan", "日本 Plan"},
	}
	for _, tt := range tests {
		if got := titleCaseWords(tt.in); got != tt.want {
			t.Errorf("titleCaseWords(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
