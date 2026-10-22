package web

import (
	"encoding/json"
	"strings"
	"testing"
)

// jsStringLiteral renders s as a JavaScript string literal. Node tests that
// require() a file interpolate a filesystem path into the script source; a
// Windows path such as C:\a\b\peak-hours.js contains backslashes, so embedding
// it between raw quotes makes JavaScript read escape sequences (\a, \b) and the
// require fails with a mangled module name. JSON string quoting is valid
// JavaScript and leaves the path intact on every platform.
func jsStringLiteral(t testing.TB, s string) string {
	t.Helper()
	quoted, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("quote %q: %v", s, err)
	}
	return string(quoted)
}

func TestJSStringLiteralKeepsWindowsPathIntact(t *testing.T) {
	path := `C:\Users\runneradmin\AppData\Local\onWatch\internal\web\static\peak-hours.js`
	literal := jsStringLiteral(t, path)

	// JavaScript reads a lone backslash as an escape, so every one in the path
	// must appear doubled inside the literal.
	if got, want := strings.Count(literal, `\`), 2*strings.Count(path, `\`); got != want {
		t.Fatalf("literal %s has %d backslashes, want %d", literal, got, want)
	}
	var roundTrip string
	if err := json.Unmarshal([]byte(literal), &roundTrip); err != nil {
		t.Fatalf("literal %s is not a quoted string: %v", literal, err)
	}
	if roundTrip != path {
		t.Fatalf("round trip = %q, want %q", roundTrip, path)
	}
}
