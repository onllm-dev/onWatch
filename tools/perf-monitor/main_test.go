package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	defer r.Close()
	// Closes the writer if fn fails the test before the explicit Close below,
	// so the reader goroutine still sees EOF.
	defer w.Close()

	// Drain the pipe concurrently. Pipe buffers are small (a few KB on
	// Windows), so reading only after fn returns deadlocks once fn writes
	// more than the buffer holds.
	type readResult struct {
		out []byte
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		out, err := io.ReadAll(r)
		done <- readResult{out: out, err: err}
	}()

	os.Stdout = w
	func() {
		defer func() { os.Stdout = oldStdout }()
		fn()
	}()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	res := <-done
	if res.err != nil {
		t.Fatalf("read stdout: %v", res.err)
	}
	return string(res.out)
}

// isolatePIDFile points the tool's PID file lookup at a fresh temp home and
// returns the path it will read, with its directory created. HOME (Unix),
// USERPROFILE and LOCALAPPDATA (Windows) are all redirected so the real
// onWatch PID file is never read, signalled or removed.
func isolatePIDFile(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	path := pidFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir pid dir: %v", err)
	}
	return path
}

// exitAtStartEnv makes a copy of this test binary exit 0 before flag parsing,
// so it can stand in for an onwatch binary that dies during startup.
const exitAtStartEnv = "PERF_MONITOR_EXIT_AT_START"

func init() {
	if os.Getenv(exitAtStartEnv) == "1" {
		os.Exit(0)
	}
}

// idleHelperEnv turns a re-executed copy of this test binary into an idle
// process that exits cleanly on os.Interrupt, like the onWatch daemon.
const idleHelperEnv = "PERF_MONITOR_IDLE_HELPER"

func TestHelperIdleProcess(t *testing.T) {
	if os.Getenv(idleHelperEnv) != "1" {
		return
	}
	// Notify also re-enables SIGINT if the test run started with it ignored.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	select {
	case <-interrupted:
		os.Exit(0)
	case <-time.After(2 * time.Minute):
		os.Exit(3)
	}
}

// startIdleHelper starts an idle helper process running this test binary
// (perf-monitor.test, so not an onWatch process). The returned channel closes
// once the process has exited and been reaped.
func startIdleHelper(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	return startIdleHelperBinary(t, os.Args[0])
}

// startOnwatchNamedIdleHelper starts an idle helper from a copy of this test
// binary named like the onWatch executable, so it passes isOnwatchProcess.
func startOnwatchNamedIdleHelper(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	selfPath, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	self, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	bin := filepath.Join(t.TempDir(), onwatchBinaryName)
	if err := os.WriteFile(bin, self, 0o755); err != nil {
		t.Fatalf("write onwatch-named helper: %v", err)
	}
	return startIdleHelperBinary(t, bin)
}

func startIdleHelperBinary(t *testing.T, bin string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(bin, "-test.run=^TestHelperIdleProcess$")
	cmd.Env = append(os.Environ(), idleHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd, exited
}

func TestParsePIDFile(t *testing.T) {
	cases := map[string]int{
		"1234:9211\n": 1234, // current onWatch format: pid:port
		"1234\n":      1234, // older bare-pid format
		"not-a-pid":   0,
		"":            0,
		"-5:9211":     0,
	}
	for in, want := range cases {
		if got := parsePIDFile([]byte(in)); got != want {
			t.Errorf("parsePIDFile(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCalculateStats_EmptySamples(t *testing.T) {
	stats := calculateStats("idle", nil)

	if stats.Phase != "idle" {
		t.Fatalf("expected phase idle, got %q", stats.Phase)
	}
	if stats.Samples != 0 || stats.MinRSSMB != 0 || stats.MaxRSSMB != 0 || stats.AvgRSSMB != 0 || stats.P95RSSMB != 0 {
		t.Fatalf("expected zero-value stats for empty input, got %+v", stats)
	}
}

func TestCalculateStats_ComputesMinMaxAverageAndP95(t *testing.T) {
	mb := uint64(1024 * 1024)
	samples := []MemorySample{
		{RSSBytes: 5 * mb},
		{RSSBytes: 1 * mb},
		{RSSBytes: 3 * mb},
		{RSSBytes: 4 * mb},
		{RSSBytes: 2 * mb},
	}

	stats := calculateStats("load", samples)

	if stats.Phase != "load" {
		t.Fatalf("expected phase load, got %q", stats.Phase)
	}
	if stats.Samples != 5 {
		t.Fatalf("expected 5 samples, got %d", stats.Samples)
	}
	if stats.MinRSSMB != 1 {
		t.Fatalf("expected min 1MB, got %v", stats.MinRSSMB)
	}
	if stats.MaxRSSMB != 5 {
		t.Fatalf("expected max 5MB, got %v", stats.MaxRSSMB)
	}
	if stats.AvgRSSMB != 3 {
		t.Fatalf("expected avg 3MB, got %v", stats.AvgRSSMB)
	}
	if stats.P95RSSMB != 5 {
		t.Fatalf("expected p95 5MB, got %v", stats.P95RSSMB)
	}
}

func TestGenerateRecommendation_CoversAllBranches(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   string
	}{
		{
			name:   "high idle memory",
			report: Report{IdleStats: PhaseStats{AvgRSSMB: 55}, DeltaRSSMB: 2},
			want:   "High idle memory",
		},
		{
			name:   "large increase under load",
			report: Report{IdleStats: PhaseStats{AvgRSSMB: 20}, DeltaRSSMB: 12},
			want:   "Large memory increase under load",
		},
		{
			name:   "minimal overhead",
			report: Report{IdleStats: PhaseStats{AvgRSSMB: 20}, DeltaRSSMB: 0.5},
			want:   "Excellent! Minimal memory overhead",
		},
		{
			name:   "acceptable overhead",
			report: Report{IdleStats: PhaseStats{AvgRSSMB: 20}, DeltaRSSMB: 3},
			want:   "Good performance. Memory overhead is acceptable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateRecommendation(&tt.report)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("expected recommendation %q in %q", tt.want, got)
			}
		})
	}
}

func TestDisplayResults_FormatsAndTruncatesOutput(t *testing.T) {
	report := &Report{
		IdleStats:    PhaseStats{Samples: 2, MinRSSMB: 10, MaxRSSMB: 12, AvgRSSMB: 11, P95RSSMB: 12},
		LoadStats:    PhaseStats{Samples: 3, MinRSSMB: 11, MaxRSSMB: 15, AvgRSSMB: 13, P95RSSMB: 15},
		DeltaRSSMB:   2,
		DeltaPercent: 18.18,
		RequestMetrics: []RequestMetric{{
			Endpoint: "/api/history?range=6h&very-long-parameter=true",
			Count:    7,
			AvgTime:  1500 * time.Microsecond,
		}},
		Recommendation: "All clear",
	}

	out := captureStdout(t, func() {
		displayResults(report)
	})

	for _, want := range []string{"MONITORING RESULTS", "IDLE STATE", "LOAD STATE", "COMPARISON", "HTTP REQUEST PERFORMANCE", "All clear"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got %s", want, out)
		}
	}
	if !strings.Contains(out, "/api/history?range=6h&...") {
		t.Fatalf("expected long endpoint to be truncated, got %s", out)
	}
}

func TestSaveReport_WritesJSONFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	report := &Report{
		Timestamp:      time.Date(2026, 3, 4, 15, 4, 5, 0, time.UTC),
		PID:            1234,
		Port:           9211,
		Recommendation: "Looks good",
	}

	out := captureStdout(t, func() {
		saveReport(report)
	})

	filename := filepath.Join(tempDir, "perf-report-20260304-150405.json")
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read report file: %v", err)
	}
	if !strings.Contains(string(data), "\"pid\": 1234") {
		t.Fatalf("expected saved JSON to include pid, got %s", string(data))
	}
	if !strings.Contains(out, "perf-report-20260304-150405.json") {
		t.Fatalf("expected stdout to mention filename, got %s", out)
	}
}

func TestFindOnWatchProcess_InvalidPidFileFallsBackToPortScanAndReturnsZero(t *testing.T) {
	pidFile := isolatePIDFile(t)
	if err := os.WriteFile(pidFile, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	got := findonWatchProcess(65530)
	if got != 0 {
		t.Fatalf("expected no process found, got %d", got)
	}
}

func TestGetProcessMemory_ParsesOrGracefullyReturnsZero(t *testing.T) {
	rss, vms := getProcessMemory(os.Getpid())
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if rss != 0 || vms != 0 {
			t.Fatalf("expected zero memory on unsupported OS, got rss=%d vms=%d", rss, vms)
		}
		return
	}
	if rss == 0 && vms == 0 {
		// Allowed when ps output cannot be parsed in restricted env.
		return
	}
	if rss == 0 || vms == 0 {
		t.Fatalf("expected both values populated or both zero, got rss=%d vms=%d", rss, vms)
	}
}

func TestGetProcessMemory_NonexistentPidReturnsZero(t *testing.T) {
	rss, vms := getProcessMemory(999999)
	if rss != 0 || vms != 0 {
		t.Fatalf("expected zero memory for nonexistent pid, got rss=%d vms=%d", rss, vms)
	}
}

func TestIsOnwatchProcess_UnknownPidReturnsFalse(t *testing.T) {
	if isOnwatchProcess(999999) {
		t.Fatal("expected unknown pid to not be identified as onwatch")
	}
}

// Only the executable's base name counts: the test binary (perf-monitor.test)
// is not onWatch, a binary named onwatch is, wherever it lives.
func TestIsOnwatchProcess_MatchesExecutableBaseName(t *testing.T) {
	if isOnwatchProcess(os.Getpid()) {
		t.Fatalf("the perf-monitor test binary must not be identified as onwatch (name %q)", processCommandName(os.Getpid()))
	}
	helper, _ := startOnwatchNamedIdleHelper(t)
	if !isOnwatchProcess(helper.Process.Pid) {
		t.Fatalf("a process running %s must be identified as onwatch (name %q)", onwatchBinaryName, processCommandName(helper.Process.Pid))
	}
}

func TestGenerateLoad_CollectsMetricsDeterministically(t *testing.T) {
	mux := http.NewServeMux()
	for _, endpoint := range []string{"/", "/api/providers", "/api/current", "/api/history", "/api/cycles", "/api/summary", "/api/sessions", "/api/insights"} {
		local := endpoint
		mux.HandleFunc(local, func(w http.ResponseWriter, r *http.Request) {
			if local == "/api/history" && r.URL.RawQuery != "range=6h" {
				t.Fatalf("expected query range=6h, got %q", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	out := captureStdout(t, func() {
		metrics := generateLoad(ts.URL, 120*time.Millisecond)
		if len(metrics) != 8 {
			t.Fatalf("expected metrics for 8 endpoints, got %d", len(metrics))
		}
		for _, m := range metrics {
			if m.Count < 1 {
				t.Fatalf("expected at least one request for %s", m.Endpoint)
			}
			// A local request can measure 0s on a coarse clock (Windows), so
			// check the aggregates are consistent rather than strictly positive.
			if m.MinTime < 0 || m.MinTime > m.AvgTime || m.AvgTime > m.MaxTime {
				t.Fatalf("inconsistent durations for %s: min=%v avg=%v max=%v", m.Endpoint, m.MinTime, m.AvgTime, m.MaxTime)
			}
		}
	})

	if !strings.Contains(out, "Total requests made") {
		t.Fatalf("expected summary output from generateLoad, got %s", out)
	}
}

func TestIsProcessRunning_CurrentAndNonexistentPID(t *testing.T) {
	if !isProcessRunning(os.Getpid()) {
		t.Fatal("expected current process to be running")
	}
	if isProcessRunning(999999) {
		t.Fatal("expected nonexistent pid to not be running")
	}
	if isProcessRunning(0) || isProcessRunning(-1) {
		t.Fatal("expected non-positive pids to not be running")
	}
}

func TestIsProcessRunning_ExitedProcess(t *testing.T) {
	cmd, exited := startIdleHelper(t)
	if !isProcessRunning(cmd.Process.Pid) {
		t.Fatalf("expected helper %d to be running", cmd.Process.Pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	<-exited
	if isProcessRunning(cmd.Process.Pid) {
		t.Fatalf("expected exited helper %d to not be running", cmd.Process.Pid)
	}
}

func TestFindOnWatchProcess_ValidPIDInFileUsesIsProcessRunningBranch(t *testing.T) {
	pidFile := isolatePIDFile(t)
	// onWatch writes "pid:port".
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+":65529"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	if got := findonWatchProcess(65529); got != os.Getpid() {
		t.Fatalf("expected pid file branch to return current pid %d, got %d", os.Getpid(), got)
	}
}

func TestStopOnWatch_RemovesInvalidPIDFileSafely(t *testing.T) {
	pidFile := isolatePIDFile(t)
	if err := os.WriteFile(pidFile, []byte("invalid-pid"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stoponWatch(65528)

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, stat err=%v", err)
	}
}

func TestStartOnWatch_BinaryMissingReturnsZero(t *testing.T) {
	tempDir := t.TempDir()
	// An empty PATH keeps the fallback lookup from finding an installed onwatch
	// and starting a real daemon instead of reporting the missing binary.
	t.Setenv("PATH", tempDir)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	pid := startonWatch(65527)
	if pid != 0 {
		t.Fatalf("expected startonWatch to fail with missing binary, got pid %d", pid)
	}
}

func TestSampleMemory_StopsWithoutCollectingOnImmediateStop(t *testing.T) {
	samplesMutex.Lock()
	samples = nil
	samplesMutex.Unlock()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		sampleMemory(os.Getpid(), "idle", stop)
		close(done)
	}()

	close(stop)

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("sampleMemory did not stop promptly")
	}

	samplesMutex.RLock()
	defer samplesMutex.RUnlock()
	if len(samples) != 0 {
		t.Fatalf("expected no samples on immediate stop, got %d", len(samples))
	}
}

func TestRunMonitoring_ZeroDurationReturnsDeterministicReport(t *testing.T) {
	samplesMutex.Lock()
	samples = nil
	samplesMutex.Unlock()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	mux := http.NewServeMux()
	for _, path := range []string{"/", "/api/providers", "/api/current", "/api/history", "/api/cycles", "/api/summary", "/api/sessions", "/api/insights"} {
		p := path
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if p == "/api/history" && r.URL.RawQuery != "range=6h" {
				t.Fatalf("expected history query range=6h, got %q", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusOK)
		})
	}

	server := &http.Server{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Handler: mux}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = server.ListenAndServe()
	}()
	t.Cleanup(func() {
		_ = server.Close()
		wg.Wait()
	})

	report := runMonitoring(os.Getpid(), port, 0)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.PID != os.Getpid() {
		t.Fatalf("expected pid %d, got %d", os.Getpid(), report.PID)
	}
	if report.Port != port {
		t.Fatalf("expected port %d, got %d", port, report.Port)
	}
	if report.IdleDuration != 0 || report.LoadDuration != 0 {
		t.Fatalf("expected zero durations, got idle=%v load=%v", report.IdleDuration, report.LoadDuration)
	}
	if len(report.RequestMetrics) != 0 {
		t.Fatalf("expected no request metrics with zero duration, got %d", len(report.RequestMetrics))
	}
	if !strings.Contains(report.Recommendation, "Excellent! Minimal memory overhead") {
		t.Fatalf("unexpected recommendation: %q", report.Recommendation)
	}
}

func TestStartOnWatch_ProcessDiesDuringStartupReturnsZero(t *testing.T) {
	tempDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWD) }()

	// A copy of this test binary stands in for onwatch and exits at once on
	// every platform. A shell script would not be executable on Windows.
	t.Setenv(exitAtStartEnv, "1")
	selfPath, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	self, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, onwatchBinaryName), self, 0o755); err != nil {
		t.Fatalf("write failing onwatch binary: %v", err)
	}

	pid := startonWatch(65524)
	if pid != 0 {
		t.Fatalf("expected zero pid when process dies during startup, got %d", pid)
	}
}

func TestStopOnWatch_ValidPIDFileSignalsProcess(t *testing.T) {
	pidFile := isolatePIDFile(t)
	helper, exited := startOnwatchNamedIdleHelper(t)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(helper.Process.Pid)+":65523"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stoponWatch(65523)

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatalf("expected helper process %d to be stopped", helper.Process.Pid)
	}
	if isProcessRunning(helper.Process.Pid) {
		t.Fatalf("expected helper process %d to be gone", helper.Process.Pid)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removed, stat err=%v", err)
	}
}

// A PID file left behind by a crashed onWatch can name a PID that the OS has
// since reused for an unrelated process. stoponWatch must treat it as stale:
// never signal or kill it, and still remove the file.
func TestStopOnWatch_StalePIDFileDoesNotKillOtherProcess(t *testing.T) {
	pidFile := isolatePIDFile(t)
	helper, exited := startIdleHelper(t)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(helper.Process.Pid)+":65522"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stoponWatch(65522)

	select {
	case <-exited:
		t.Fatalf("stoponWatch stopped non-onwatch process %d named in a stale PID file", helper.Process.Pid)
	case <-time.After(1 * time.Second):
	}
	if !isProcessRunning(helper.Process.Pid) {
		t.Fatalf("expected non-onwatch process %d to keep running", helper.Process.Pid)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("expected stale pid file removed, stat err=%v", err)
	}
}

// runMainHelper re-executes this test binary as a child running main().
//
// The child is deliberately isolated: it runs in an empty directory with an
// empty PATH and a temp home, so startonWatch's binary search (./onwatch, ../onwatch,
// ../../onwatch, then PATH) genuinely finds nothing. Without that isolation the
// child locates the repo-root binary built by `app.sh --build` (or an installed
// onwatch on PATH), starts a real daemon instead of failing, and then the
// daemon inherits the child's stdout so CombinedOutput never reaches EOF - the
// test hangs until the panic timeout and leaks the daemon. The context bounds
// the wait so a regression fails fast instead of stalling the whole suite.
func runMainHelper(t *testing.T, envVar string) ([]byte, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run="+t.Name())
	cmd.Dir = t.TempDir()
	// A temp home keeps the child away from the real onWatch PID file, which
	// --restart would otherwise use to stop the developer's running daemon.
	home := t.TempDir()
	cmd.Env = append(os.Environ(), envVar+"=1", "PATH="+t.TempDir(),
		"HOME="+home, "USERPROFILE="+home, "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"))

	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("helper did not exit within 30s (it likely started a real daemon); output=%s", string(output))
	}
	return output, err
}

func TestMain_NoProcessFoundExitsWithHelp(t *testing.T) {
	if os.Getenv("PERF_MONITOR_MAIN_HELPER") == "1" {
		os.Args = []string{"perf-monitor", "65522", "0s"}
		main()
		return
	}

	output, err := runMainHelper(t, "PERF_MONITOR_MAIN_HELPER")
	if err == nil {
		t.Fatalf("expected helper to exit non-zero, output=%s", string(output))
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d; output=%s", exitErr.ExitCode(), string(output))
	}
	if !strings.Contains(string(output), "onWatch process not found") {
		t.Fatalf("expected missing-process message, got %s", string(output))
	}
}

func TestMain_RestartFailureExitsWithError(t *testing.T) {
	if os.Getenv("PERF_MONITOR_MAIN_RESTART_HELPER") == "1" {
		os.Args = []string{"perf-monitor", "--restart", "65521", "0s"}
		main()
		return
	}

	output, err := runMainHelper(t, "PERF_MONITOR_MAIN_RESTART_HELPER")
	if err == nil {
		t.Fatalf("expected helper to exit non-zero, output=%s", string(output))
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d; output=%s", exitErr.ExitCode(), string(output))
	}
	if !strings.Contains(string(output), "Failed to start onWatch") {
		t.Fatalf("expected restart failure message, got %s", string(output))
	}
}

// Positional arguments must be counted independently of flags. Indexing on the
// raw argv position made `--restart 8080 30s` read "8080" as the duration and
// keep the default port 9211 - so a restart aimed at another port stopped
// whatever was listening on 9211 instead.
func TestParseArgs_FlagBeforePositionals(t *testing.T) {
	cases := []struct {
		args     []string
		wantPort int
		wantDur  time.Duration
		wantRst  bool
	}{
		{[]string{"perf-monitor", "--restart", "8080", "30s"}, 8080, 30 * time.Second, true},
		{[]string{"perf-monitor", "8080", "30s"}, 8080, 30 * time.Second, false},
		{[]string{"perf-monitor", "8080", "--restart", "30s"}, 8080, 30 * time.Second, true},
		{[]string{"perf-monitor"}, 9211, time.Minute, false},
		{[]string{"perf-monitor", "-r"}, 9211, time.Minute, true},
	}

	for _, c := range cases {
		port, duration, restart := parseArgs(c.args[1:])
		if port != c.wantPort || duration != c.wantDur || restart != c.wantRst {
			t.Errorf("parseArgs(%v) = (%d, %s, %v), want (%d, %s, %v)",
				c.args[1:], port, duration, restart, c.wantPort, c.wantDur, c.wantRst)
		}
	}
}
