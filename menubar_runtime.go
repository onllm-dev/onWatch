package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
	"github.com/onllm-dev/onwatch/v2/internal/web"
)

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
	if pid <= 0 {
		return false
	}
	if processZombie(pid) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func processZombie(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(string(out)), "Z")
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
		_ = proc.Signal(syscall.SIGTERM)
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
	if cfg == nil || cfg.TestMode || !menubar.IsSupported() || runtime.GOOS != "darwin" {
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
	cmd.Env = os.Environ()

	logFile, err := config.OpenRotatingLogFile(menubarLogPath(cfg))
	if err != nil {
		return fmt.Errorf("failed to open menubar log file: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("failed to capture menubar stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("failed to capture menubar stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := writeRuntimePID(path); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return fmt.Errorf("failed to write menubar pid file: %w", err)
	}
	logger.Info("Menubar companion started", "pid", cmd.Process.Pid, "log_path", menubarLogPath(cfg))

	var stderrBuf bytes.Buffer
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	go func() {
		defer close(stdoutDone)
		writer := io.Writer(logFile)
		if cfg.DebugMode {
			writer = io.MultiWriter(logFile, os.Stdout)
		}
		_, _ = io.Copy(writer, stdoutPipe)
	}()

	go func() {
		defer close(stderrDone)
		if cfg.DebugMode {
			writer := io.MultiWriter(logFile, os.Stderr, &stderrBuf)
			_, _ = io.Copy(writer, stderrPipe)
			return
		}
		writer := io.MultiWriter(logFile, &stderrBuf)
		_, _ = io.Copy(writer, stderrPipe)
	}()

	go func() {
		err := cmd.Wait()
		<-stdoutDone
		<-stderrDone
		_ = os.Remove(path)
		_ = logFile.Close()

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

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("Menubar runtime starting", "pid", os.Getpid(), "port", cfg.Port, "db_path", cfg.DBPath, "test_mode", cfg.TestMode)

	db, err := store.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database for menubar companion: %w", err)
	}
	defer db.Close()

	tr := tracker.New(db, logger)
	zaiTr := tracker.NewZaiTracker(db, logger)
	h := web.NewHandler(db, tr, logger, nil, cfg, zaiTr)
	h.SetVersion(version)
	h.SetAnthropicTracker(tracker.NewAnthropicTracker(db, logger))
	h.SetCopilotTracker(tracker.NewCopilotTracker(db, logger))
	h.SetCodexTracker(tracker.NewCodexTracker(db, logger))
	h.SetAntigravityTracker(tracker.NewAntigravityTracker(db, logger))
	h.SetMiniMaxTracker(tracker.NewMiniMaxTracker(db, logger))

	settings, err := db.GetMenubarSettings()
	if err != nil {
		return err
	}
	mbCfg := settings.ToConfig(cfg.Port, h.BuildMenubarSnapshot)
	mbCfg.TestMode = cfg.TestMode

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

	err = menubar.Init(mbCfg)
	if err != nil {
		logger.Error("Menubar runtime stopped with error", "error", err)
		return err
	}
	logger.Info("Menubar runtime stopped")
	return nil
}
