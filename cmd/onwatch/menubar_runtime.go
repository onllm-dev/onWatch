package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// closeLog closes an optional log file.
func closeLog(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

// companionOwnsLog reports whether the spawned companion writes menubar.log
// itself. It matches runMenubarCommand: debug mode keeps the log on
// stdout/stderr, everything else opens the file.
func companionOwnsLog(cfg *config.Config) bool {
	return cfg != nil && !cfg.DebugMode && !cfg.TestMode
}

// companionRelayWriter returns the daemon-side stdout sink, or nil when the
// companion owns the log file (the daemon then drains the pipe only, so a
// hung daemon cannot block the companion's writes once the 16KB pipe fills).
func companionRelayWriter(logFile io.Writer, cfg *config.Config) io.Writer {
	if companionOwnsLog(cfg) {
		return nil
	}
	if cfg != nil && cfg.DebugMode {
		return io.MultiWriter(logFile, os.Stdout)
	}
	return logFile
}

// openCompanionLog opens the companion log in append mode at the same path the
// daemon passes in its startup log line ("log_path").
func openCompanionLog(path string) (io.WriteCloser, error) {
	return config.OpenRotatingLogFile(path)
}

func menubarPIDPath(testMode bool) string {
	name := "onwatch-menubar.pid"
	if testMode {
		name = "onwatch-menubar-test.pid"
	}
	return filepath.Join(pidDir, name)
}

func readRuntimePID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	content := strings.TrimSpace(string(data))
	fmt.Sscanf(content, "%d", &pid)
	return pid
}

func writeRuntimePID(path string) error {
	if err := ensurePIDDir(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

func processRunning(pid int) bool {
	return processAlive(pid)
}

func menubarLogNames(testMode bool) []string {
	if testMode {
		return []string{"menubar-test.log", ".onwatch-menubar-test.log"}
	}
	return []string{"menubar.log", ".onwatch-menubar.log"}
}

func menubarLogPath(cfg *config.Config) string {
	testMode := cfg != nil && cfg.TestMode
	name := menubarLogNames(testMode)[0]

	if cfg == nil || cfg.DBPath == "" {
		return filepath.Join(pidDir, name)
	}
	return filepath.Join(filepath.Dir(cfg.DBPath), name)
}

func stopMenubarProcess(testMode bool) error {
	path := menubarPIDPath(testMode)
	pid := readRuntimePID(path)
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = terminateProcess(proc)
	}
	_ = os.Remove(path)
	return nil
}

func waitForServerReady(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func startMenubarCompanion(cfg *config.Config, logger *slog.Logger) error {
	if cfg == nil || cfg.TestMode || !menubar.IsSupported() || !menubar.SessionAvailable() {
		return nil
	}
	logger.Info("Starting menubar companion process")
	settings, err := store.New(cfg.DBPath)
	if err == nil {
		defer settings.Close()
		if menubarSettings, settingsErr := settings.GetMenubarSettings(); settingsErr == nil && menubarSettings != nil && !menubarSettings.Enabled {
			return nil
		}
	}
	path := menubarPIDPath(cfg.TestMode)
	if pid := readRuntimePID(path); pid > 0 {
		if processRunning(pid) {
			return nil
		}
		_ = os.Remove(path)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	args := []string{"menubar", fmt.Sprintf("--port=%d", cfg.Port), fmt.Sprintf("--db=%s", cfg.DBPath)}
	if cfg.TestMode {
		args = append(args, "--test")
	}
	cmd := exec.Command(exe, args...)
	// Hand over our PID so the companion can quit on its own once this daemon
	// is gone instead of leaving a dead tray icon behind.
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", daemonPIDEnv, os.Getpid()))
	cmd.SysProcAttr = companionSysProcAttr()

	// The daemon-side log file only exists for the legacy relay path (debug
	// mode or test mode). In the normal path the companion opens menubar.log
	// itself, so a long daemon stall cannot let its 16KB pipe fill and block
	// the companion's refresh worker.
	var logFile *os.File
	var relayStdout io.Writer
	if !companionOwnsLog(cfg) {
		logFile, err = config.OpenRotatingLogFile(menubarLogPath(cfg))
		if err != nil {
			return fmt.Errorf("failed to open menubar log file: %w", err)
		}
		relayStdout = companionRelayWriter(logFile, cfg)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		closeLog(logFile)
		return fmt.Errorf("failed to capture menubar stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		closeLog(logFile)
		return fmt.Errorf("failed to capture menubar stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		closeLog(logFile)
		return err
	}
	if err := writeRuntimePID(path); err != nil {
		_ = cmd.Process.Kill()
		closeLog(logFile)
		return fmt.Errorf("failed to write menubar pid file: %w", err)
	}
	logger.Info("Menubar companion started", "pid", cmd.Process.Pid, "log_path", menubarLogPath(cfg))

	var stderrBuf bytes.Buffer
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	// The companion opens menubar.log itself. Relaying its pipes into the same
	// file would write every line twice; when the companion owns the log the
	// daemon drains the pipes only (an undrained pipe blocks the companion),
	// keeping stderr for crash diagnostics.
	go func() {
		defer close(stdoutDone)
		if relayStdout == nil {
			_, _ = io.Copy(io.Discard, stdoutPipe)
			return
		}
		_, _ = io.Copy(relayStdout, stdoutPipe)
	}()

	go func() {
		defer close(stderrDone)
		writer := io.Writer(&stderrBuf)
		if relayStdout != nil {
			// Legacy path: the daemon still owns the log file.
			if cfg.DebugMode {
				writer = io.MultiWriter(logFile, os.Stderr, &stderrBuf)
			} else {
				writer = io.MultiWriter(logFile, &stderrBuf)
			}
		} else if cfg.DebugMode {
			writer = io.MultiWriter(os.Stderr, &stderrBuf)
		}
		_, _ = io.Copy(writer, stderrPipe)
	}()

	go func() {
		err := cmd.Wait()
		<-stdoutDone
		<-stderrDone
		_ = os.Remove(path)
		closeLog(logFile)

		if err != nil {
			exitCode := -1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			logger.Error("Menubar companion crashed", "exit_code", exitCode, "stderr", strings.TrimSpace(stderrBuf.String()))
			return
		}
		logger.Info("Menubar companion exited normally")
	}()

	return nil
}

func runMenubarCommand() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config for menubar companion: %w", err)
	}
	if !menubar.IsSupported() {
		return fmt.Errorf("menubar companion is not available in this build")
	}

	// A second companion must exit before it creates a tray icon. A daemon
	// restart can launch a new companion while the old one is still inside its
	// watcher grace period; the old one releases the lock when it exits.
	lock, err := acquireMenubarLock(cfg.TestMode)
	if err != nil {
		if errors.Is(err, errMenubarCompanionRunning) {
			return nil
		}
		return fmt.Errorf("failed to acquire menubar lock: %w", err)
	}
	defer lock.Close()

	// Write the log here instead of relying on the daemon to read stdout over a
	// pipe. A daemon hung for a long time stops draining that 16KB pipe, and
	// the companion's log writes then block its refresh worker.
	logSink := io.Writer(os.Stdout)
	if cfg.DebugMode {
		logSink = io.MultiWriter(os.Stdout, os.Stderr)
	} else {
		logFile, err := config.OpenRotatingLogFile(menubarLogPath(cfg))
		if err == nil {
			defer logFile.Close()
			logSink = logFile
		}
		// Fall back to stdout; the daemon's relay then still records the lines.
	}

	logger := slog.New(slog.NewTextHandler(logSink, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("Menubar runtime starting", "pid", os.Getpid(), "port", cfg.Port, "db_path", cfg.DBPath, "test_mode", cfg.TestMode, "log_path", menubarLogPath(cfg))

	db, err := store.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database for menubar companion: %w", err)
	}
	settings, err := db.GetMenubarSettings()
	if err != nil {
		db.Close()
		return err
	}
	db.Close()

	mbCfg := settings.ToConfig(cfg.Port, httpSnapshotProvider(cfg.Port))
	mbCfg.TestMode = cfg.TestMode
	mbCfg.MistralRetry = httpMistralRetry(cfg.Port, cfg.BasePath)

	pidPath := menubarPIDPath(cfg.TestMode)
	if err := writeRuntimePID(pidPath); err != nil {
		return fmt.Errorf("failed to write menubar pid file: %w", err)
	}
	defer os.Remove(pidPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		_ = menubar.Stop()
	}()

	watchStop := make(chan struct{})
	defer close(watchStop)
	go watchDaemon(newDaemonWatcher(cfg, daemonPIDFromEnv()), daemonWatchInterval, watchStop, func() {
		logger.Info("Daemon is gone, stopping menubar companion")
		_ = menubar.Stop()
	})

	err = menubar.Init(mbCfg)
	if err != nil {
		logger.Error("Menubar runtime stopped with error", "error", err)
		return err
	}
	logger.Info("Menubar runtime stopped")
	return nil
}

func httpSnapshotProvider(port int) menubar.SnapshotProvider {
	url := fmt.Sprintf("http://localhost:%d/api/menubar/summary", port)
	// The companion tick is 60 s. The old 5 s cap tripped whenever a provider
	// poll held the single SQLite connection while the snapshot was built; 15 s
	// leaves headroom and still fails well inside the tick.
	const attemptTimeout = 15 * time.Second
	client := &http.Client{Timeout: attemptTimeout}
	attempt := func() (*menubar.Snapshot, error) {
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		defer cancel()
		return fetchMenubarSnapshot(ctx, client, url)
	}
	return func() (*menubar.Snapshot, error) {
		snapshot, err := attempt()
		if err == nil {
			return snapshot, nil
		}
		// One retry: a timeout is often a transient write burst that clears
		// within a moment, and a single miss must not be treated as failure.
		snapshot, retryErr := attempt()
		if retryErr != nil {
			return nil, fmt.Errorf("%w (retry: %v)", err, retryErr)
		}
		return snapshot, nil
	}
}

func fetchMenubarSnapshot(ctx context.Context, client *http.Client, url string) (*menubar.Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("menubar snapshot request failed: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("menubar snapshot fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("menubar snapshot request returned %s", resp.Status)
	}
	var snapshot menubar.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("menubar snapshot decode failed: %w", err)
	}
	return &snapshot, nil
}

func httpMistralRetry(port int, basePath string) func() error {
	url := fmt.Sprintf("http://localhost:%d%s/api/menubar/mistral/retry", port, basePath)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Requested-With", "onWatch")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("Mistral retry request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			return fmt.Errorf("Mistral retry returned HTTP %d", resp.StatusCode)
		}
		return nil
	}
}

// portFilePath is the discovery file thin clients (GNOME extension, VS Code
// extension, tray companion) read to find the dashboard port.
func portFilePath() string {
	return filepath.Join(pidDir, "port")
}

func writePortFile(port int) error {
	return writePortFileTo(portFilePath(), port)
}

func writePortFileTo(path string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", port)), 0o644)
}
