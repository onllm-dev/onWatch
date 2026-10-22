package web

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nodeScriptTimeout bounds every node script run. A script that asserts
// correctly finishes in well under a second; the margin covers a cold node
// start on a loaded CI runner.
const nodeScriptTimeout = 15 * time.Second

// runNodeScript executes script with node and fails the test with the exit
// error, stdout and stderr if it does not succeed.
//
// The script is written to a temp file and passed as an argument instead of
// being piped through stdin. Node reads a stdin script until EOF before it
// executes anything, so a child that never sees EOF produces no output and no
// assertion failure - it just sits there until the harness kills it, which on
// Windows surfaces as a bare "exit status 1" after the full timeout. A file
// needs no EOF handshake and runs the same on every platform.
func runNodeScript(t testing.TB, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required")
	}
	path := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write node script: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeScriptTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, node, path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return
	}
	// A killed child reports its platform exit code (1 on Windows), which says
	// nothing about why it was still running. Name the deadline explicitly and
	// keep both streams so a future failure is diagnosable.
	if ctx.Err() != nil {
		t.Fatalf("node script did not finish within %s: %v\nscript: %s\nstdout:\n%s\nstderr:\n%s",
			nodeScriptTimeout, err, path, stdout.String(), stderr.String())
	}
	t.Fatalf("node script failed: %v\nscript: %s\nstdout:\n%s\nstderr:\n%s",
		err, path, stdout.String(), stderr.String())
}

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
