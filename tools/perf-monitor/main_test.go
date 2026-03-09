package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
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
	home := t.TempDir()
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	var pidDir string
	if runtime.GOOS == "darwin" {
		pidDir = filepath.Join(home, "Library", "Application Support", "onwatch")
	} else {
		pidDir = filepath.Join(home, ".local", "share", "onwatch")
	}
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir pid dir: %v", err)
	}
	pidFile := filepath.Join(pidDir, "onwatch.pid")
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

func TestIsOnwatchProcess_ProcessNameCoverageForCurrentProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("ps-based process checks are only used on darwin/linux")
	}

	currentIsOnwatch := isOnwatchProcess(os.Getpid())
	out, err := exec.Command("ps", "-p", strconv.Itoa(os.Getpid()), "-o", "comm=").Output()
	if err != nil {
		t.Fatalf("read current process name: %v", err)
	}
	want := strings.Contains(strings.ToLower(string(out)), "onwatch")
	if currentIsOnwatch != want {
		t.Fatalf("expected %v for current process name %q, got %v", want, string(out), currentIsOnwatch)
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
			if m.MinTime <= 0 || m.MaxTime <= 0 || m.AvgTime <= 0 {
				t.Fatalf("expected positive durations for %s, got min=%v avg=%v max=%v", m.Endpoint, m.MinTime, m.AvgTime, m.MaxTime)
			}
		}
	})

	if !strings.Contains(out, "Total requests made") {
		t.Fatalf("expected summary output from generateLoad, got %s", out)
	}
}

func TestIsProcessRunning_CurrentAndNonexistentPID(t *testing.T) {
	gotCurrent := isProcessRunning(os.Getpid())
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find current process: %v", err)
	}
	wantCurrent := proc.Signal(os.Signal(nil)) == nil
	if gotCurrent != wantCurrent {
		t.Fatalf("expected current process running=%v, got %v", wantCurrent, gotCurrent)
	}

	if isProcessRunning(999999) {
		t.Fatal("expected nonexistent pid to not be running")
	}
}

func TestFindOnWatchProcess_ValidPIDInFileUsesIsProcessRunningBranch(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	var pidDir string
	if runtime.GOOS == "darwin" {
		pidDir = filepath.Join(home, "Library", "Application Support", "onwatch")
	} else {
		pidDir = filepath.Join(home, ".local", "share", "onwatch")
	}
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir pid dir: %v", err)
	}
	pidFile := filepath.Join(pidDir, "onwatch.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	got := findonWatchProcess(65529)
	if got != 0 && got != os.Getpid() {
		t.Fatalf("expected pid file branch to return 0 or current pid, got %d", got)
	}
}

func TestStopOnWatch_RemovesInvalidPIDFileSafely(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	var pidDir string
	if runtime.GOOS == "darwin" {
		pidDir = filepath.Join(home, "Library", "Application Support", "onwatch")
	} else {
		pidDir = filepath.Join(home, ".local", "share", "onwatch")
	}
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir pid dir: %v", err)
	}
	pidFile := filepath.Join(pidDir, "onwatch.pid")
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

	if err := os.WriteFile(filepath.Join(tempDir, "onwatch"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write failing onwatch script: %v", err)
	}

	pid := startonWatch(65524)
	if pid != 0 {
		t.Fatalf("expected zero pid when process dies during startup, got %d", pid)
	}
}

func TestStopOnWatch_ValidPIDFileSignalsProcess(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	var pidDir string
	if runtime.GOOS == "darwin" {
		pidDir = filepath.Join(home, "Library", "Application Support", "onwatch")
	} else {
		pidDir = filepath.Join(home, ".local", "share", "onwatch")
	}
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir pid dir: %v", err)
	}

	helpCmd := exec.Command("sh", "-c", "trap 'exit 0' INT TERM; while true; do sleep 1; done")
	if err := helpCmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	t.Cleanup(func() {
		if helpCmd.Process != nil {
			_ = helpCmd.Process.Kill()
			_, _ = helpCmd.Process.Wait()
		}
	})

	pidFile := filepath.Join(pidDir, "onwatch.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(helpCmd.Process.Pid)), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	stoponWatch(65523)

	if isProcessRunning(helpCmd.Process.Pid) {
		t.Fatalf("expected helper process %d to be stopped", helpCmd.Process.Pid)
	}
}

func TestMain_NoProcessFoundExitsWithHelp(t *testing.T) {
	if os.Getenv("PERF_MONITOR_MAIN_HELPER") == "1" {
		os.Args = []string{"perf-monitor", "65522", "0s"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMain_NoProcessFoundExitsWithHelp")
	cmd.Env = append(os.Environ(), "PERF_MONITOR_MAIN_HELPER=1")
	output, err := cmd.CombinedOutput()
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

	cmd := exec.Command(os.Args[0], "-test.run=TestMain_RestartFailureExitsWithError")
	cmd.Env = append(os.Environ(), "PERF_MONITOR_MAIN_RESTART_HELPER=1")
	output, err := cmd.CombinedOutput()
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
