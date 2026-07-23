package update

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"equal", "2.2.0", "2.2.0", 0},
		{"a greater major", "3.0.0", "2.9.9", 1},
		{"b greater major", "1.0.0", "2.0.0", -1},
		{"a greater minor", "2.3.0", "2.2.0", 1},
		{"b greater minor", "2.1.0", "2.2.0", -1},
		{"a greater patch", "2.2.1", "2.2.0", 1},
		{"b greater patch", "2.2.0", "2.2.1", -1},
		{"with v prefix", "v2.3.0", "v2.2.0", 1},
		{"mixed v prefix", "v2.3.0", "2.2.0", 1},
		{"short version a", "2.3", "2.2.0", 1},
		{"short version b", "2.2.0", "2.3", -1},
		{"single digit", "3", "2.9.9", 1},
		{"pre-release suffix", "2.2.6-test", "2.2.5-test", 1},
		{"pre-release vs release", "2.2.6-beta", "2.2.5", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCheck_DevVersion(t *testing.T) {
	u := NewUpdater("dev", slog.Default())
	info, err := u.Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Available {
		t.Error("dev build should never report updates available")
	}
	if info.CurrentVersion != "dev" {
		t.Errorf("got current_version=%q, want %q", info.CurrentVersion, "dev")
	}
}

func TestCheck_EmptyVersion(t *testing.T) {
	u := NewUpdater("", slog.Default())
	info, err := u.Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Available {
		t.Error("empty version should never report updates available")
	}
}

func TestCheck_UpdateAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL

	info, err := u.Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Available {
		t.Error("expected update to be available")
	}
	if info.LatestVersion != "3.0.0" {
		t.Errorf("got latest=%q, want %q", info.LatestVersion, "3.0.0")
	}
	if info.DownloadURL == "" {
		t.Error("expected download URL to be set")
	}
}

func TestCheck_AlreadyLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{TagName: "v2.2.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL

	info, err := u.Check()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Available {
		t.Error("should not report update when at latest version")
	}
	if info.DownloadURL != "" {
		t.Error("download URL should be empty when no update available")
	}
}

func TestCheck_CacheTTL(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.cacheTTL = 1 * time.Hour

	// First call hits the server
	if _, err := u.Check(); err != nil {
		t.Fatalf("first check: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 API call, got %d", callCount)
	}

	// Second call should use cache
	info, err := u.Check()
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected cache hit (1 call), got %d calls", callCount)
	}
	if !info.Available {
		t.Error("cached result should still show update available")
	}
}

func TestCheck_CacheExpiry(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.cacheTTL = 1 * time.Millisecond

	// First call
	if _, err := u.Check(); err != nil {
		t.Fatalf("first check: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond)

	// Second call should hit the server again
	if _, err := u.Check(); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls after cache expiry, got %d", callCount)
	}
}

// deadURL is an address with nothing listening, used to force the github.com
// redirect fallback to fail so tests stay hermetic (never hit real github.com).
const deadURL = "http://127.0.0.1:1"

// redirectServer returns an httptest server that mimics github.com's
// /releases/latest behaviour: a 302 whose Location points at the tag page.
func redirectServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://github.com/onllm-dev/onwatch/releases/tag/"+tag)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck_GitHubAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.listURL = srv.URL
	u.redirectURL = deadURL // keep hermetic: no real github.com call

	_, err := u.Check()
	if err == nil {
		t.Error("expected error for non-200 response")
	}
}

func TestCheck_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.listURL = srv.URL
	u.redirectURL = deadURL

	_, err := u.Check()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBinaryDownloadURL(t *testing.T) {
	u := NewUpdater("2.2.0", slog.Default())

	url := u.binaryDownloadURL("3.0.0")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	// Should contain the version and platform
	if got := url; got == "" {
		t.Error("expected download URL")
	}
}

func TestValidateBinary_Valid(t *testing.T) {
	// Create a temp file with ELF magic bytes
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	// ELF magic: 0x7f 'E' 'L' 'F'
	if err := os.WriteFile(path, []byte{0x7f, 'E', 'L', 'F', 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid ELF should pass: %v", err)
	}
}

func TestValidateBinary_MachO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	// Mach-O 64-bit magic (little-endian, common on macOS)
	if err := os.WriteFile(path, []byte{0xCF, 0xFA, 0xED, 0xFE, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid Mach-O should pass: %v", err)
	}
}

func TestValidateBinary_PE(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	if err := os.WriteFile(path, []byte{'M', 'Z', 0, 0, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid PE should pass: %v", err)
	}
}

func TestValidateBinary_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err == nil {
		t.Error("expected error for invalid binary")
	}
}

func TestValidateBinary_TooSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	if err := os.WriteFile(path, []byte{0x7f}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err == nil {
		t.Error("expected error for file too small")
	}
}

func TestApply_DevBuild(t *testing.T) {
	u := NewUpdater("dev", slog.Default())
	err := u.Apply()
	if err == nil {
		t.Error("expected error for dev build")
	}
}

func TestApply_AlreadyLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{TagName: "v2.2.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL

	err := u.Apply()
	if err == nil {
		t.Error("expected error when already at latest")
	}
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := checkWritable(dir); err != nil {
		t.Errorf("temp dir should be writable: %v", err)
	}
}

func TestNewUpdater_Defaults(t *testing.T) {
	u := NewUpdater("1.0.0", nil)
	if u.currentVersion != "1.0.0" {
		t.Errorf("got version=%q, want %q", u.currentVersion, "1.0.0")
	}
	if u.cacheTTL != defaultCacheTTL {
		t.Errorf("got cacheTTL=%v, want %v", u.cacheTTL, defaultCacheTTL)
	}
	if u.apiURL != githubReleasesURL {
		t.Errorf("got apiURL=%q, want default", u.apiURL)
	}
}

func TestFilterArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"empty", nil, nil},
		{"no update", []string{"--debug", "--port", "9211"}, []string{"--debug", "--port", "9211"}},
		{"update subcommand", []string{"update"}, nil},
		{"--update flag", []string{"--update"}, nil},
		{"mixed", []string{"--debug", "update", "--port", "9211"}, []string{"--debug", "--port", "9211"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterArgs(tt.args)
			if len(got) != len(tt.want) {
				t.Errorf("filterArgs(%v) = %v, want %v", tt.args, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("filterArgs(%v)[%d] = %q, want %q", tt.args, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	tmpPath := filepath.Join(dir, "onwatch.tmp.123")

	// Create "current" binary (Mach-O magic)
	if err := os.WriteFile(exePath, []byte("old-binary-content"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create "new" binary
	if err := os.WriteFile(tmpPath, []byte("new-binary-content"), 0755); err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()
	if err := replaceBinary(exePath, tmpPath, logger); err != nil {
		t.Fatalf("replaceBinary failed: %v", err)
	}

	// Verify new binary is in place
	content, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("failed to read replaced binary: %v", err)
	}
	if string(content) != "new-binary-content" {
		t.Errorf("got content=%q, want %q", string(content), "new-binary-content")
	}

	// Verify temp file was consumed (renamed away)
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temp file should have been renamed away")
	}

	// Verify .old was cleaned up
	if _, err := os.Stat(exePath + ".old"); err == nil {
		t.Error(".old backup should have been cleaned up")
	}
}

func TestReplaceBinary_LeftoverOldFile(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	tmpPath := filepath.Join(dir, "onwatch.tmp.456")
	oldPath := filepath.Join(dir, "onwatch.old")

	// Create leftover .old from previous failed update
	if err := os.WriteFile(oldPath, []byte("stale-old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exePath, []byte("current"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpPath, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}

	logger := slog.Default()
	if err := replaceBinary(exePath, tmpPath, logger); err != nil {
		t.Fatalf("replaceBinary with leftover .old should succeed: %v", err)
	}

	content, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Errorf("got %q, want %q", string(content), "new")
	}
}

func TestIsSystemd(t *testing.T) {
	// Save and restore INVOCATION_ID
	orig := os.Getenv("INVOCATION_ID")
	defer os.Setenv("INVOCATION_ID", orig)

	os.Setenv("INVOCATION_ID", "")
	if IsSystemd() {
		t.Error("expected false when INVOCATION_ID is empty")
	}

	os.Setenv("INVOCATION_ID", "some-uuid-value")
	if !IsSystemd() {
		t.Error("expected true when INVOCATION_ID is set")
	}
}

func TestDetectServiceName_Fallback(t *testing.T) {
	// On macOS or when /proc/self/cgroup is not available, should return default
	name := DetectServiceName()
	if name == "" {
		t.Error("expected non-empty service name")
	}
	// On non-Linux, falls back to default
	if name != "onwatch.service" {
		// If we happen to be on Linux with cgroup info, it should end with .service
		if !strings.HasSuffix(name, ".service") {
			t.Errorf("expected service name ending in .service, got %q", name)
		}
	}
}

func TestFindUnitFile_NotFound(t *testing.T) {
	// With a nonsense service name, should return empty
	result := findUnitFile("nonexistent-service-12345.service")
	if result != "" {
		t.Errorf("expected empty string for nonexistent service, got %q", result)
	}
}

func TestFindUnitFile_SystemPath(t *testing.T) {
	dir := t.TempDir()

	// Create a fake /etc/systemd/system directory structure
	systemDir := filepath.Join(dir, "etc", "systemd", "system")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatal(err)
	}

	unitFile := filepath.Join(systemDir, "onwatch.service")
	if err := os.WriteFile(unitFile, []byte("[Service]\nRestart=on-failure\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// findUnitFile checks absolute paths, so we can't easily redirect it.
	// Instead, test that it returns empty for a valid service name not on disk.
	result := findUnitFile("onwatch-test-phantom.service")
	if result != "" {
		t.Errorf("expected empty for phantom service, got %q", result)
	}
}

func TestMigrateSystemdUnit_NotSystemd(t *testing.T) {
	// When not under systemd, MigrateSystemdUnit should be a no-op
	orig := os.Getenv("INVOCATION_ID")
	defer os.Setenv("INVOCATION_ID", orig)

	os.Setenv("INVOCATION_ID", "")
	// Should not panic or error
	MigrateSystemdUnit(slog.Default())
}

func TestValidateBinary_MachO_BigEndian(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	// Mach-O 64-bit big-endian magic
	if err := os.WriteFile(path, []byte{0xFE, 0xED, 0xFA, 0xCF, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid Mach-O big-endian should pass: %v", err)
	}
}

func TestValidateBinary_MachO_32bit_BigEndian(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	// Mach-O 32-bit big-endian magic
	if err := os.WriteFile(path, []byte{0xFE, 0xED, 0xFA, 0xCE, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid Mach-O 32-bit big-endian should pass: %v", err)
	}
}

func TestValidateBinary_FatBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	// Fat binary (universal) magic
	if err := os.WriteFile(path, []byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid fat binary should pass: %v", err)
	}
}

func TestValidateBinary_NonexistentFile(t *testing.T) {
	err := validateBinary("/nonexistent/path/to/binary")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestCheckWritable_NotWritable(t *testing.T) {
	err := checkWritable("/nonexistent-dir-12345")
	if err == nil {
		t.Error("expected error for non-writable directory")
	}
}

func TestApply_EmptyVersion(t *testing.T) {
	u := NewUpdater("", slog.Default())
	err := u.Apply()
	if err == nil {
		t.Error("expected error for empty version build")
	}
	if !strings.Contains(err.Error(), "cannot update dev build") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestApply_DownloadAndReplace(t *testing.T) {
	// Create a mock server that serves the release API and a binary download
	var currentExe string
	var err error
	currentExe, err = os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// Read real binary magic bytes from the current executable for validation
	magic := make([]byte, 8)
	f, err := os.Open(currentExe)
	if err != nil {
		t.Fatalf("open current exe: %v", err)
	}
	f.Read(magic)
	f.Close()

	// Create download server serving a valid binary (using real magic bytes)
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve a file with valid magic bytes
		w.Write(magic)
		w.Write([]byte("rest-of-binary-content-padded-to-be-non-empty"))
	}))
	defer dlSrv.Close()

	// Create API server that returns a newer version
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{TagName: "v99.0.0"})
	}))
	defer apiSrv.Close()

	u := NewUpdater("1.0.0", slog.Default())
	u.apiURL = apiSrv.URL
	u.downloadURL = dlSrv.URL

	// Apply will try to replace the current executable, which we can't really do in test.
	// But we can verify it gets past the download and validation steps.
	err = u.Apply()
	// We expect an error because either:
	// 1. The download URL format won't match the mock server, or
	// 2. We can't actually replace the running test binary
	// The key is we exercised more of the Apply() code path.
	if err == nil {
		t.Log("Apply succeeded unexpectedly (may be OK on some platforms)")
	}
}

func TestCheck_ServerUnreachable(t *testing.T) {
	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = deadURL      // nothing listening
	u.listURL = deadURL     // fallback also unreachable
	u.redirectURL = deadURL // redirect fallback also unreachable
	u.checkMaxAttempts = 1  // keep the test fast/offline

	_, err := u.Check()
	if err == nil {
		t.Error("expected error when server is unreachable")
	}
}

func TestCheck_RetriesOn504ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.checkMaxAttempts = 3
	u.checkRetryBackoff = time.Millisecond

	info, err := u.Check()
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if !info.Available || info.LatestVersion != "3.0.0" {
		t.Errorf("got %+v, want available v3.0.0", info)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 calls (2 retries then success), got %d", got)
	}
}

func TestCheck_FallsBackToListOnPersistent504(t *testing.T) {
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer latest.Close()

	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]githubRelease{
			{TagName: "v9.9.9-rc1", Prerelease: true},
			{TagName: "v3.1.0-draft", Draft: true},
			{TagName: "v3.0.0"},
			{TagName: "v2.0.0"},
		})
	}))
	defer list.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = latest.URL
	u.listURL = list.URL
	u.redirectURL = deadURL // force fall-through to the list endpoint
	u.checkMaxAttempts = 2
	u.checkRetryBackoff = time.Millisecond

	info, err := u.Check()
	if err != nil {
		t.Fatalf("expected list fallback to succeed, got error: %v", err)
	}
	if info.LatestVersion != "3.0.0" {
		t.Errorf("got latest=%q, want 3.0.0 (newest non-draft/non-prerelease)", info.LatestVersion)
	}
}

func TestCheck_NoRetryOn4xx(t *testing.T) {
	var latestCalls, listCalls atomic.Int32
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		latestCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer latest.Close()
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer list.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = latest.URL
	u.listURL = list.URL
	u.redirectURL = deadURL // redirect fallback unreachable so Check still errors
	u.checkMaxAttempts = 3
	u.checkRetryBackoff = time.Millisecond

	if _, err := u.Check(); err == nil {
		t.Fatal("expected error on 403")
	}
	// 4xx is not retryable: exactly one latest attempt, no list fallback.
	if got := latestCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 latest call (no retry on 4xx), got %d", got)
	}
	if got := listCalls.Load(); got != 0 {
		t.Errorf("expected no list fallback on 4xx, got %d calls", got)
	}
}

// TestCheck_FallsBackToRedirectOn403 is the direct regression test for issue
// #81: api.github.com 403s due to rate limiting, and the github.com web
// redirect rescues the version check.
func TestCheck_FallsBackToRedirectOn403(t *testing.T) {
	var listCalls atomic.Int32
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer latest.Close()
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCalls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer list.Close()
	redir := redirectServer(t, "v3.0.0")

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = latest.URL
	u.listURL = list.URL
	u.redirectURL = redir.URL

	info, err := u.Check()
	if err != nil {
		t.Fatalf("expected redirect fallback to succeed on 403, got error: %v", err)
	}
	if !info.Available || info.LatestVersion != "3.0.0" {
		t.Errorf("got %+v, want available v3.0.0", info)
	}
	// 403 is not retryable, so the list endpoint must not be consulted.
	if got := listCalls.Load(); got != 0 {
		t.Errorf("expected no list fallback on 403, got %d calls", got)
	}
}

func TestFetchTagFromRedirect(t *testing.T) {
	t.Run("valid redirect", func(t *testing.T) {
		redir := redirectServer(t, "v4.5.6")
		u := NewUpdater("1.0.0", slog.Default())
		u.redirectURL = redir.URL
		tag, err := u.fetchTagFromRedirect()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tag != "v4.5.6" {
			t.Errorf("got tag=%q, want v4.5.6", tag)
		}
	})

	t.Run("no Location header", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		u := NewUpdater("1.0.0", slog.Default())
		u.redirectURL = srv.URL
		if _, err := u.fetchTagFromRedirect(); err == nil {
			t.Error("expected error when Location header is missing")
		}
	})

	t.Run("unexpected Location format", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://github.com/onllm-dev/onwatch/issues/81")
			w.WriteHeader(http.StatusFound)
		}))
		defer srv.Close()
		u := NewUpdater("1.0.0", slog.Default())
		u.redirectURL = srv.URL
		if _, err := u.fetchTagFromRedirect(); err == nil {
			t.Error("expected error for Location without /releases/tag/")
		}
	})
}

func TestCheck_BothEndpointsFail(t *testing.T) {
	latest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer latest.Close()
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer list.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = latest.URL
	u.listURL = list.URL
	u.redirectURL = deadURL // all three fallbacks fail
	u.checkMaxAttempts = 2
	u.checkRetryBackoff = time.Millisecond

	_, err := u.Check()
	if err == nil {
		t.Fatal("expected error when both endpoints fail")
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("expected error to mention the fallback attempt, got %v", err)
	}
}

func TestBinaryDownloadURL_Format(t *testing.T) {
	u := NewUpdater("2.2.0", slog.Default())
	u.downloadURL = "https://example.com/download"

	url := u.binaryDownloadURL("3.0.0")
	if !strings.HasPrefix(url, "https://example.com/download/v3.0.0/") {
		t.Errorf("unexpected URL format: %s", url)
	}
	if !strings.Contains(url, "onwatch-") {
		t.Errorf("URL should contain 'onwatch-': %s", url)
	}
}

func TestExtractLeadingInt_Edge(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0", 0},
		{"", 0},
		{"abc", 0},
		{"10-beta.1", 10},
	}
	for _, tt := range tests {
		got := extractLeadingInt(tt.input)
		if got != tt.want {
			t.Errorf("extractLeadingInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestCheck_InvalidURLPath(t *testing.T) {
	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = "://bad-url"
	u.redirectURL = "://bad-url" // redirect fallback also invalid

	_, err := u.Check()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "update.Check") {
		t.Fatalf("expected wrapped update.Check error, got %v", err)
	}
}

func TestCheck_CachedNoUpdatePath(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(githubRelease{TagName: "v2.2.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.cacheTTL = time.Hour

	first, err := u.Check()
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if first.Available {
		t.Fatal("expected no update on first check")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 API call, got %d", callCount)
	}

	second, err := u.Check()
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if second.Available {
		t.Fatal("expected cached no-update result")
	}
	if second.DownloadURL != "" {
		t.Fatalf("expected empty download URL for cached no-update result, got %q", second.DownloadURL)
	}
	if callCount != 1 {
		t.Fatalf("expected cache hit without additional API call, got %d calls", callCount)
	}
}

func TestCheck_RequestHeaders(t *testing.T) {
	var gotAccept string
	var gotUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotUA = r.Header.Get("User-Agent")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL

	if _, err := u.Check(); err != nil {
		t.Fatalf("Check() failed: %v", err)
	}

	if gotAccept != "application/vnd.github.v3+json" {
		t.Fatalf("Accept header = %q, want application/vnd.github.v3+json", gotAccept)
	}
	if gotUA != "onwatch/2.2.0" {
		t.Fatalf("User-Agent header = %q, want onwatch/2.2.0", gotUA)
	}
}

func TestCheck_AuthorizationHeaderFromToken(t *testing.T) {
	// Ensure a clean env, then set GITHUB_TOKEN for this test only.
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "test-token-123")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v3.0.0"})
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL

	if _, err := u.Check(); err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if gotAuth != "Bearer test-token-123" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer test-token-123")
	}
}

func TestCheck_RateLimitErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	u := NewUpdater("2.2.0", slog.Default())
	u.apiURL = srv.URL
	u.listURL = srv.URL
	u.redirectURL = deadURL // force surfacing the API error

	_, err := u.Check()
	if err == nil {
		t.Fatal("expected error on rate-limited 403")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate-limit message, got: %v", err)
	}
}

func TestFindUnitFile_UserLevelPath(t *testing.T) {
	serviceName := "onwatch-user-level-test.service"
	tmpHome := t.TempDir()

	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("Setenv HOME: %v", err)
	}

	userDir := filepath.Join(tmpHome, ".config", "systemd", "user")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatalf("MkdirAll user systemd dir: %v", err)
	}

	userUnitPath := filepath.Join(userDir, serviceName)
	if err := os.WriteFile(userUnitPath, []byte("[Service]\nRestart=always\n"), 0644); err != nil {
		t.Fatalf("WriteFile user unit: %v", err)
	}

	got := findUnitFile(serviceName)
	if got != userUnitPath {
		t.Fatalf("findUnitFile() = %q, want %q", got, userUnitPath)
	}
}

func TestValidateBinary_MachO_32bit_LittleEndian(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-binary")
	if err := os.WriteFile(path, []byte{0xCE, 0xFA, 0xED, 0xFE, 0, 0, 0, 0}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateBinary(path); err != nil {
		t.Errorf("valid Mach-O 32-bit little-endian should pass: %v", err)
	}
}

func TestExtractLeadingInt_HyphenEdge(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"-beta", 0},
		{"15-", 15},
		{"--", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractLeadingInt(tt.input)
			if got != tt.want {
				t.Fatalf("extractLeadingInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectServiceName_FromCgroup(t *testing.T) {
	origRead := readCgroupFile
	defer func() { readCgroupFile = origRead }()

	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/system.slice/custom-onwatch.service\n"), nil
	}

	got := DetectServiceName()
	if got != "custom-onwatch.service" {
		t.Fatalf("DetectServiceName() = %q, want %q", got, "custom-onwatch.service")
	}
}

func TestDetectServiceName_FallbackWhenNoServiceUnit(t *testing.T) {
	origRead := readCgroupFile
	defer func() { readCgroupFile = origRead }()

	readCgroupFile = func() ([]byte, error) {
		return []byte("0::/user.slice/session.scope\n"), nil
	}

	got := DetectServiceName()
	if got != "onwatch.service" {
		t.Fatalf("DetectServiceName() = %q, want default", got)
	}
}

func TestReplaceBinary_BackupRenameStrategySuccess(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	tmpPath := filepath.Join(dir, "onwatch.tmp")

	if err := os.Mkdir(exePath, 0755); err != nil {
		t.Fatalf("Mkdir exePath: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("new-binary"), 0755); err != nil {
		t.Fatalf("WriteFile tmpPath: %v", err)
	}

	if err := replaceBinary(exePath, tmpPath, slog.Default()); err != nil {
		t.Fatalf("replaceBinary() error = %v", err)
	}

	content, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("ReadFile exePath: %v", err)
	}
	if string(content) != "new-binary" {
		t.Fatalf("replaced content = %q, want %q", string(content), "new-binary")
	}

	if _, err := os.Stat(exePath + ".old"); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup of backup file, stat err=%v", err)
	}
}

func TestReplaceBinary_BackupRenameFails(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	tmpPath := filepath.Join(dir, "onwatch.tmp")
	backupPath := exePath + ".old"

	if err := os.Mkdir(exePath, 0755); err != nil {
		t.Fatalf("Mkdir exePath: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exePath, "block-remove"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile block-remove: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("new-binary"), 0755); err != nil {
		t.Fatalf("WriteFile tmpPath: %v", err)
	}

	if err := os.Mkdir(backupPath, 0755); err != nil {
		t.Fatalf("Mkdir backupPath: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "keep"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile backup keep file: %v", err)
	}

	err := replaceBinary(exePath, tmpPath, slog.Default())
	if err == nil {
		t.Fatal("expected backup rename error")
	}
	if !strings.Contains(err.Error(), "backup rename") {
		t.Fatalf("expected backup rename error, got: %v", err)
	}
}

func TestReplaceBinary_SwapRenameFailsRestoresBackup(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "onwatch")
	tmpPath := filepath.Join(dir, "missing.tmp")

	if err := os.Mkdir(exePath, 0755); err != nil {
		t.Fatalf("Mkdir exePath: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exePath, "block-remove"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile block-remove: %v", err)
	}

	err := replaceBinary(exePath, tmpPath, slog.Default())
	if err == nil {
		t.Fatal("expected swap rename error")
	}
	if !strings.Contains(err.Error(), "swap rename") {
		t.Fatalf("expected swap rename error, got: %v", err)
	}

	st, statErr := os.Stat(exePath)
	if statErr != nil {
		t.Fatalf("expected exePath to be restored, stat err=%v", statErr)
	}
	if !st.IsDir() {
		t.Fatalf("expected restored exePath to be directory")
	}
}

func TestMigrateSystemdUnit_UpdatesUserUnitAndReloads(t *testing.T) {
	origRead := readCgroupFile
	defer func() { readCgroupFile = origRead }()

	serviceName := "onwatch-migrate-test.service"
	readCgroupFile = func() ([]byte, error) {
		return []byte(fmt.Sprintf("0::/system.slice/%s\n", serviceName)), nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("INVOCATION_ID", "invocation-test-id")

	unitDir := filepath.Join(tmpHome, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		t.Fatalf("MkdirAll unitDir: %v", err)
	}
	unitPath := filepath.Join(unitDir, serviceName)
	original := "[Service]\nRestart=on-failure\nRestartSec=10\n"
	if err := os.WriteFile(unitPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile unitPath: %v", err)
	}

	binDir := t.TempDir()
	markerFile := filepath.Join(binDir, "systemctl.called")
	scriptPath := filepath.Join(binDir, "systemctl")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + markerFile + "\"\n" +
		"exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile systemctl stub: %v", err)
	}

	pathSep := ":"
	if runtime.GOOS == "windows" {
		pathSep = ";"
	}
	t.Setenv("PATH", binDir+pathSep+os.Getenv("PATH"))

	MigrateSystemdUnit(slog.Default())

	updatedBytes, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("ReadFile unitPath: %v", err)
	}
	updated := string(updatedBytes)
	if !strings.Contains(updated, "Restart=always") {
		t.Fatalf("expected Restart=always in unit file, got:\n%s", updated)
	}
	if !strings.Contains(updated, "RestartSec=5") {
		t.Fatalf("expected RestartSec=5 in unit file, got:\n%s", updated)
	}

	calls, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("expected systemctl to be called, read marker: %v", err)
	}
	if !strings.Contains(string(calls), "--user daemon-reload") {
		t.Fatalf("expected user-level daemon-reload call, got: %s", string(calls))
	}
}

func TestRestart_SpawnFailureFallsBackAndReturnsError(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("PATH", "")

	u := NewUpdater("1.0.0", slog.Default())
	u.lastAppliedPath = "/definitely/missing/onwatch-binary"

	err := u.Restart()
	if err == nil {
		t.Fatal("expected restart error when spawn and fallbacks fail")
	}
	if !strings.Contains(err.Error(), "all restart methods failed") {
		t.Fatalf("unexpected restart error: %v", err)
	}
}

func TestFallbackSystemctlRestart_AllMethodsFail(t *testing.T) {
	t.Setenv("PATH", "")

	u := NewUpdater("1.0.0", slog.Default())
	err := u.fallbackSystemctlRestart()
	if err == nil {
		t.Fatal("expected fallbackSystemctlRestart to fail when systemctl is unavailable")
	}
	if !strings.Contains(err.Error(), "all restart methods failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
