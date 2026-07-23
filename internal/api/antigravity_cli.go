package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	pty "github.com/aymanbagabas/go-pty"
)

// Errors specific to the agy CLI runner.
var (
	ErrAgyBinaryNotFound = errors.New("antigravity cli: agy binary not found")
	ErrAgyNotReady       = errors.New("antigravity cli: quota service did not become ready")
)

const (
	agyMetadataBody    = `{"metadata":{"ideName":"antigravity","extensionName":"antigravity","locale":"en","ideVersion":"unknown"}}`
	agyQuotaSummaryRPC = "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary"
	agyUserStatusRPC   = "/exa.language_server_pb.LanguageServerService/GetUserStatus"

	agyReadinessTimeout = 90 * time.Second
	agyReadinessPoll    = 2 * time.Second
	agyDefaultWarmTTL   = 5 * time.Minute
	agyMaxFailures      = 2
)

// agySession holds a live, managed agy process and its verified connection.
type agySession struct {
	pty  pty.Pty
	cmd  *pty.Cmd
	conn *AntigravityConnection
}

// AntigravityCLIRunner manages a bounded warm agy process so onWatch can read
// the richer RetrieveUserQuotaSummary payload. The agy CLI exposes its quota
// server only while an interactive process is alive and exits without a TTY,
// so the process is launched in a pseudo-terminal, kept warm between polls,
// relaunched on failure, and reaped on shutdown. Only the process this runner
// launched is ever killed - a user's own interactive agy is never touched.
type AntigravityCLIRunner struct {
	logger  *slog.Logger
	client  *AntigravityClient
	warmTTL time.Duration

	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu          sync.Mutex
	sess        *agySession
	lastUsed    time.Time
	failures    int
	watchdog    sync.Once
}

// NewAntigravityCLIRunner creates a runner. It does not launch agy until the
// first Fetch call.
func NewAntigravityCLIRunner(logger *slog.Logger) *AntigravityCLIRunner {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AntigravityCLIRunner{
		logger:     logger.With("component", "antigravity-cli"),
		client:     NewAntigravityClient(logger),
		warmTTL:    agyDefaultWarmTTL,
		rootCtx:    ctx,
		rootCancel: cancel,
	}
}

// resolveAgyPath locates the agy binary, honoring an explicit override before
// falling back to PATH and well-known install locations.
func resolveAgyPath() (string, error) {
	binName := "agy"
	if runtime.GOOS == "windows" {
		binName = "agy.exe"
	}

	if override := strings.TrimSpace(os.Getenv("ANTIGRAVITY_CLI_PATH")); override != "" {
		if fi, err := os.Stat(override); err == nil && !fi.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("%w: ANTIGRAVITY_CLI_PATH=%q not found", ErrAgyBinaryNotFound, override)
	}

	if p, err := exec.LookPath(binName); err == nil {
		return p, nil
	}

	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", binName))
		if runtime.GOOS == "windows" {
			candidates = append(candidates,
				filepath.Join(home, "AppData", "Local", "Programs", "agy", binName),
				filepath.Join(home, "AppData", "Local", "agy", binName),
			)
		}
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates,
			"/opt/homebrew/bin/agy",
			"/usr/local/bin/agy",
			"/usr/bin/agy",
		)
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", ErrAgyBinaryNotFound
}

// Fetch ensures a ready agy session and returns a CLI-sourced snapshot.
func (r *AntigravityCLIRunner) Fetch(ctx context.Context) (*AntigravitySnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureLocked(ctx); err != nil {
		r.recordFailureLocked()
		return nil, err
	}

	base := r.sess.conn.BaseURL
	summary, status, err := r.post(ctx, base, agyQuotaSummaryRPC)
	if err != nil || status != http.StatusOK {
		r.recordFailureLocked()
		if err == nil {
			err = fmt.Errorf("antigravity cli: quota summary status %d", status)
		}
		return nil, err
	}

	models, err := ParseAgyQuotaSummary(summary)
	if err != nil {
		r.recordFailureLocked()
		return nil, err
	}

	snap := &AntigravitySnapshot{
		CapturedAt: time.Now(),
		Source:     AntigravitySourceCLI,
		Models:     models,
		RawJSON:    string(summary),
	}

	// Identity/plan are best-effort; the quota summary already succeeded.
	if statusBody, st, serr := r.post(ctx, base, agyUserStatusRPC); serr == nil && st == http.StatusOK {
		if us, perr := ParseAntigravityResponse(statusBody); perr == nil && us.UserStatus != nil {
			snap.Email = us.UserStatus.Email
			if us.UserStatus.PlanStatus != nil {
				snap.PromptCredits = us.UserStatus.PlanStatus.AvailablePromptCredits
				if us.UserStatus.PlanStatus.PlanInfo != nil {
					snap.PlanName = us.UserStatus.PlanStatus.PlanInfo.PlanName
					snap.MonthlyCredits = us.UserStatus.PlanStatus.PlanInfo.MonthlyPromptCredits
				}
			}
		}
	}

	r.failures = 0
	r.lastUsed = time.Now()
	r.startWatchdog()
	return snap, nil
}

// ensureLocked guarantees a live, responsive session, relaunching if needed.
// Caller must hold r.mu.
func (r *AntigravityCLIRunner) ensureLocked(ctx context.Context) error {
	if r.sess != nil && r.sessionHealthy(ctx) {
		return nil
	}
	r.teardownLocked()

	binPath, err := resolveAgyPath()
	if err != nil {
		return err
	}

	sess, err := r.launch(binPath)
	if err != nil {
		return err
	}

	conn, err := r.awaitReady(ctx, sess)
	if err != nil {
		r.killSession(sess)
		return err
	}
	sess.conn = conn
	r.sess = sess
	r.logger.Info("agy session ready", "pid", sess.cmd.Process.Pid, "port", conn.Port)
	return nil
}

// launch starts agy inside a pseudo-terminal and drains its output.
func (r *AntigravityCLIRunner) launch(binPath string) (*agySession, error) {
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("antigravity cli: open pty: %w", err)
	}
	cmd := p.CommandContext(r.rootCtx, binPath)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("antigravity cli: start agy: %w", err)
	}
	// Drain PTY output so the process is not blocked on a full buffer.
	go func() { _, _ = io.Copy(io.Discard, p) }()
	r.logger.Debug("launched managed agy", "pid", cmd.Process.Pid, "path", binPath)
	return &agySession{pty: p, cmd: cmd}, nil
}

// awaitReady polls until the quota endpoint parses, since a fresh agy can bind
// a port before its token source and quota service are initialized.
func (r *AntigravityCLIRunner) awaitReady(ctx context.Context, sess *agySession) (*AntigravityConnection, error) {
	deadline := time.Now().Add(agyReadinessTimeout)
	pid := sess.cmd.Process.Pid
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		ports, err := r.client.discoverPorts(ctx, pid)
		if err == nil && len(ports) > 0 {
			if conn, _ := r.client.probeForConnectAPI(ctx, ports, ""); conn != nil {
				if _, status, perr := r.post(ctx, conn.BaseURL, agyQuotaSummaryRPC); perr == nil && status == http.StatusOK {
					return conn, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(agyReadinessPoll):
		}
	}
	return nil, ErrAgyNotReady
}

// sessionHealthy returns true if the current session still serves quota data.
func (r *AntigravityCLIRunner) sessionHealthy(ctx context.Context) bool {
	if r.sess == nil || r.sess.conn == nil || r.sess.cmd == nil || r.sess.cmd.Process == nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, status, err := r.post(checkCtx, r.sess.conn.BaseURL, agyQuotaSummaryRPC)
	return err == nil && status == http.StatusOK
}

// post issues a Connect-RPC POST against the agy language server. No CSRF token
// is required for the CLI's server.
func (r *AntigravityCLIRunner) post(ctx context.Context, baseURL, rpcPath string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+rpcPath, strings.NewReader(agyMetadataBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")

	resp, err := r.client.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (r *AntigravityCLIRunner) recordFailureLocked() {
	r.failures++
	if r.failures >= agyMaxFailures {
		r.logger.Warn("agy session unhealthy, tearing down for relaunch", "failures", r.failures)
		r.teardownLocked()
		r.failures = 0
	}
}

// startWatchdog reaps the warm session after it has been idle past warmTTL.
func (r *AntigravityCLIRunner) startWatchdog() {
	r.watchdog.Do(func() {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-r.rootCtx.Done():
					return
				case <-ticker.C:
					r.mu.Lock()
					if r.sess != nil && r.warmTTL > 0 && time.Since(r.lastUsed) > r.warmTTL {
						r.logger.Info("agy session idle, tearing down", "idle", time.Since(r.lastUsed).Round(time.Second))
						r.teardownLocked()
					}
					r.mu.Unlock()
				}
			}
		}()
	})
}

// Stop tears down any managed agy process. Safe to call multiple times.
func (r *AntigravityCLIRunner) Stop() {
	r.mu.Lock()
	r.teardownLocked()
	r.mu.Unlock()
	r.rootCancel()
}

// teardownLocked kills the managed session. Caller must hold r.mu.
func (r *AntigravityCLIRunner) teardownLocked() {
	if r.sess == nil {
		return
	}
	r.killSession(r.sess)
	r.sess = nil
}

func (r *AntigravityCLIRunner) killSession(sess *agySession) {
	if sess == nil {
		return
	}
	if sess.cmd != nil && sess.cmd.Process != nil {
		killAgyTree(sess.cmd.Process)
	}
	if sess.pty != nil {
		_ = sess.pty.Close()
	}
}
