package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/procscan"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// museCLIBusy reports whether a live `muse` CLI process is running. The usage
// probe shares the Meta /v1/responses quota with that session; probing while
// it is active 429s the TUI. A variable so tests can stub it. The context
// bounds the scan so a cancelled poll does not wait out a wedged `ps`.
var museCLIBusy = museProcessNamed

// isMuseCommandLine reports whether a full process command line belongs to the
// Muse CLI.
//
// A false negative is the costly direction here: missing a live CLI lets the
// probe run against the same API key and 429 the user's session, which is the
// whole reason the guard exists. npm/bun global installs put a shebang
// launcher on PATH, and the kernel hands the interpreter the unresolved path,
// so ps reports `node /usr/local/bin/muse` rather than `muse` - the same case
// isClaudeCodeCommandLine handles.
func isMuseCommandLine(name string) func(string) bool {
	return func(cmdline string) bool {
		line := strings.TrimSpace(cmdline)
		if line == "" {
			return false
		}
		// Desktop bundles and Electron helpers are never the CLI.
		if strings.Contains(line, ".app/Contents/") || strings.Contains(line, "--type=") {
			return false
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return false
		}
		base := filepath.Base(fields[0])
		if runtime.GOOS == "windows" {
			base = strings.TrimSuffix(base, ".exe")
		}
		if base == name {
			return true
		}
		// A JS runtime hosting the CLI through its bin entry.
		if !isJSRuntime(base) {
			return false
		}
		for _, arg := range fields[1:] {
			if strings.HasPrefix(arg, "-") {
				continue // interpreter flag
			}
			if !strings.ContainsAny(arg, "/.") {
				continue // subcommand such as `deno run`
			}
			// First script-like argument decides.
			argBase := filepath.Base(arg)
			if runtime.GOOS == "windows" {
				argBase = strings.TrimSuffix(argBase, ".exe")
			}
			return argBase == name
		}
		return false
	}
}

// museProcessNamed uses the shared process scan so the guard also works on
// Windows, where pgrep does not exist and the probe would otherwise never skip.
func museProcessNamed(ctx context.Context, name string) bool {
	if name == "" {
		return false
	}
	return procscan.RunningContext(ctx, name+".exe", isMuseCommandLine(name))
}

// museFetcher is the usage-probe surface the Muse agent needs.
type museFetcher interface {
	FetchSnapshot(ctx context.Context) (*api.MuseSnapshot, error)
}

// MuseAgent manages the background polling loop for Muse coding-plan usage.
// Each poll sends one minimal streamed probe to the Meta Model API.
type MuseAgent struct {
	client       museFetcher
	store        *store.Store
	tracker      *tracker.MuseTracker
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

// SetPollingCheck sets a function that is called before each poll.
func (a *MuseAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

// SetNotifier sets the notification engine for sending alerts.
func (a *MuseAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

// NewMuseAgent creates a new MuseAgent with the given dependencies.
func NewMuseAgent(client museFetcher, store *store.Store, tr *tracker.MuseTracker, interval time.Duration, logger *slog.Logger, sm *SessionManager) *MuseAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &MuseAgent{
		client:   client,
		store:    store,
		tracker:  tr,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

// Run starts the Muse agent's polling loop.
func (a *MuseAgent) Run(ctx context.Context) error {
	a.logger.Info("Muse agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("Muse agent stopped")
	}()

	a.poll(ctx)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.poll(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// setCLIActive records or clears the live-CLI pause marker. Failures are
// logged and ignored: the marker is presentational and must never block a poll.
func (a *MuseAgent) setCLIActive(active bool) {
	if a.store == nil {
		return
	}
	value := ""
	if active {
		value = time.Now().UTC().Format(time.RFC3339)
	}
	if err := a.store.SetSetting(store.SettingMuseCLIActiveAt, value); err != nil {
		a.logger.Debug("muse: failed to record live-CLI pause marker", "error", err)
	}
}

// poll performs a single Muse poll cycle.
func (a *MuseAgent) poll(ctx context.Context) {
	if a.client == nil {
		return
	}
	if a.pollingCheck != nil && !a.pollingCheck() {
		return // polling disabled for this provider
	}
	if museCLIBusy != nil && museCLIBusy(ctx, "muse") {
		a.logger.Info("Muse poll skipped: live muse CLI would race the usage probe")
		// Record the skip so the dashboard can say the provider is paused
		// rather than letting the cards age into "stale" through a long
		// coding session, which is exactly when quota is being spent.
		a.setCLIActive(true)
		return
	}

	a.setCLIActive(false)

	snapshot, err := a.client.FetchSnapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if api.IsMuseRateLimited(err) {
			a.logger.Warn("Muse usage probe rate-limited; skipping this cycle")
			return
		}
		a.logger.Error("Failed to fetch Muse quotas", "error", err)
		return
	}

	if _, err := a.store.InsertMuseSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert Muse snapshot", "error", err)
		return
	}

	if a.tracker != nil {
		if err := a.tracker.Process(snapshot); err != nil {
			a.logger.Error("Muse tracker processing failed", "error", err)
		}
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "muse",
				QuotaKey:    q.Name,
				Utilization: q.Utilization,
				Limit:       q.Limit,
			})
		}
	}

	if a.sm != nil {
		var values []float64
		for _, q := range snapshot.Quotas {
			values = append(values, q.Utilization)
		}
		a.sm.ReportPoll(values)
	}

	a.logger.Info("Muse poll complete",
		"window_used_pct", snapshot.WindowUsedPct,
		"weekly_used_pct", snapshot.WeeklyUsedPct,
		"quotas", len(snapshot.Quotas),
	)
}
