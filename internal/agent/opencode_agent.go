package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type OpenCodeAgent struct {
	store        *store.Store
	interval     time.Duration
	logger       *slog.Logger
	sm           *SessionManager
	notifier     *notify.NotificationEngine
	pollingCheck func() bool
}

func (a *OpenCodeAgent) SetPollingCheck(fn func() bool) {
	a.pollingCheck = fn
}

func (a *OpenCodeAgent) SetNotifier(n *notify.NotificationEngine) {
	a.notifier = n
}

func NewOpenCodeAgent(store *store.Store, interval time.Duration, logger *slog.Logger, sm *SessionManager) *OpenCodeAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenCodeAgent{
		store:    store,
		interval: interval,
		logger:   logger,
		sm:       sm,
	}
}

func (a *OpenCodeAgent) Run(ctx context.Context) error {
	a.logger.Info("OpenCode agent started", "interval", a.interval)

	defer func() {
		if a.sm != nil {
			a.sm.Close()
		}
		a.logger.Info("OpenCode agent stopped")
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

func (a *OpenCodeAgent) poll(ctx context.Context) {
	if a.pollingCheck != nil && !a.pollingCheck() {
		return
	}

	snapshot, err := a.FetchQuotas(ctx)
	if err != nil {
		a.logger.Error("Failed to fetch OpenCode quotas", "error", err)
		return
	}

	if _, err := a.store.InsertOpenCodeSnapshot(snapshot); err != nil {
		a.logger.Error("Failed to insert OpenCode snapshot", "error", err)
	}

	if a.notifier != nil {
		for _, q := range snapshot.Quotas {
			a.notifier.Check(notify.QuotaStatus{
				Provider:    "opencode",
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
}

func parseWindowUsage(html string, prefix string) (float64, float64, bool) {
	// e.g. rollingUsage:$R[123]={...usagePercent:4.5...resetInSec:6000...}
	// We construct two patterns to handle ordering differences in JS objects
	numPattern := `(-?\d+(?:\.\d+)?)`
	
	pattern1 := prefix + `:\$R\[\d+\]=\{[^}]*usagePercent:` + numPattern + `[^}]*resetInSec:` + numPattern
	pattern2 := prefix + `:\$R\[\d+\]=\{[^}]*resetInSec:` + numPattern + `[^}]*usagePercent:` + numPattern
	
	re1 := regexp.MustCompile(pattern1)
	re2 := regexp.MustCompile(pattern2)
	
	m := re1.FindStringSubmatch(html)
	if len(m) >= 3 {
		pct, _ := strconv.ParseFloat(m[1], 64)
		reset, _ := strconv.ParseFloat(m[2], 64)
		return pct, reset, true
	}
	
	m = re2.FindStringSubmatch(html)
	if len(m) >= 3 {
		reset, _ := strconv.ParseFloat(m[1], 64)
		pct, _ := strconv.ParseFloat(m[2], 64)
		return pct, reset, true
	}
	
	return 0, 0, false
}

func (a *OpenCodeAgent) FetchQuotas(ctx context.Context) (*api.OpenCodeSnapshot, error) {
	provider := "opencode-go"
	now := time.Now()

	workspaceID := os.Getenv("OPENCODE_GO_WORKSPACE_ID")
	authCookie := os.Getenv("OPENCODE_GO_AUTH_COOKIE")

	if workspaceID != "" && authCookie != "" {
		url := "https://opencode.ai/workspace/" + workspaceID + "/go"
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err == nil {
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
			if !strings.Contains(authCookie, "=") {
				req.Header.Set("Cookie", "auth="+authCookie)
			} else {
				req.Header.Set("Cookie", authCookie)
			}
			
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == 200 {
					bodyBytes, _ := io.ReadAll(resp.Body)
					html := string(bodyBytes)
					
					var quotas []api.OpenCodeQuota
					
					if pct, resetSec, ok := parseWindowUsage(html, "rollingUsage"); ok {
						resTime := now.Add(time.Duration(resetSec) * time.Second)
						quotas = append(quotas, api.OpenCodeQuota{
							Name:        "five_hour",
							Used:        pct * 12.0 / 100.0,
							Limit:       12.0,
							Utilization: pct,
							Format:      api.OpenCodeQuotaFormatPercent,
							ResetsAt:    &resTime,
						})
					}

					if pct, resetSec, ok := parseWindowUsage(html, "weeklyUsage"); ok {
						resTime := now.Add(time.Duration(resetSec) * time.Second)
						quotas = append(quotas, api.OpenCodeQuota{
							Name:        "weekly",
							Used:        pct * 30.0 / 100.0,
							Limit:       30.0,
							Utilization: pct,
							Format:      api.OpenCodeQuotaFormatPercent,
							ResetsAt:    &resTime,
						})
					}

					if pct, resetSec, ok := parseWindowUsage(html, "monthlyUsage"); ok {
						resTime := now.Add(time.Duration(resetSec) * time.Second)
						quotas = append(quotas, api.OpenCodeQuota{
							Name:        "monthly",
							Used:        pct * 60.0 / 100.0,
							Limit:       60.0,
							Utilization: pct,
							Format:      api.OpenCodeQuotaFormatPercent,
							ResetsAt:    &resTime,
						})
					}

					if len(quotas) > 0 {
						return &api.OpenCodeSnapshot{
							CapturedAt:  now,
							AccountType: api.OpenCodeAccountTypePro,
							PlanName:    "OpenCode Go",
							Quotas:      quotas,
						}, nil
					}
				}
			}
		}
	}

	// Fallback to local estimation if scraping fails or not configured
	cost5h, err := a.store.SumAPIIntegrationCost(provider, now.Add(-5*time.Hour))
	if err != nil {
		return nil, err
	}
	cost7d, err := a.store.SumAPIIntegrationCost(provider, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	cost30d, err := a.store.SumAPIIntegrationCost(provider, now.Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}

	t5h := now.Add(5 * time.Hour)
	t7d := now.Add(7 * 24 * time.Hour)
	t30d := now.Add(30 * 24 * time.Hour)

	snapshot := &api.OpenCodeSnapshot{
		CapturedAt:  now,
		AccountType: api.OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas: []api.OpenCodeQuota{
			{
				Name:        "five_hour",
				Used:        cost5h,
				Limit:       12.0,
				Utilization: (cost5h / 12.0) * 100,
				Format:      api.OpenCodeQuotaFormatCurrency,
				ResetsAt:    &t5h,
			},
			{
				Name:        "weekly",
				Used:        cost7d,
				Limit:       30.0,
				Utilization: (cost7d / 30.0) * 100,
				Format:      api.OpenCodeQuotaFormatCurrency,
				ResetsAt:    &t7d,
			},
			{
				Name:        "monthly",
				Used:        cost30d,
				Limit:       60.0,
				Utilization: (cost30d / 60.0) * 100,
				Format:      api.OpenCodeQuotaFormatCurrency,
				ResetsAt:    &t30d,
			},
		},
	}

	return snapshot, nil
}
