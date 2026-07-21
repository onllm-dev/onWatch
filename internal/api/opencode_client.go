package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	openCodeDashboardURLPrefix = "https://opencode.ai/workspace/"
	openCodeDashboardURLSuffix = "/go"
	openCodeUserAgent          = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Gecko/20100101 Firefox/148.0"
	openCodeScrapeTimeout      = 10 * time.Second
	openCodeMaxBodyBytes       = 2 << 20 // 2 MiB
)

var (
	ErrOpenCodeUnauthorized    = errors.New("opencode: unauthorized")
	ErrOpenCodeForbidden       = errors.New("opencode: forbidden")
	ErrOpenCodeServerError     = errors.New("opencode: server error")
	ErrOpenCodeNetworkError    = errors.New("opencode: network error")
	ErrOpenCodeInvalidResponse = errors.New("opencode: invalid response")
	ErrOpenCodeParseFailed     = errors.New("opencode: parse failed")
	ErrOpenCodeMissingConfig   = errors.New("opencode: missing workspace id or auth cookie")
)

type OpenCodeClient struct {
	httpClient  *http.Client
	logger      *slog.Logger
	dashboardURLPrefix string
}

type OpenCodeClientOption func(*OpenCodeClient)

func WithOpenCodeHTTPTransport(rt http.RoundTripper) OpenCodeClientOption {
	return func(c *OpenCodeClient) {
		c.httpClient.Transport = rt
	}
}

func WithOpenCodeTimeout(timeout time.Duration) OpenCodeClientOption {
	return func(c *OpenCodeClient) {
		c.httpClient.Timeout = timeout
	}
}

func WithOpenCodeBaseURL(baseURL string) OpenCodeClientOption {
	return func(c *OpenCodeClient) {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		c.dashboardURLPrefix = baseURL + "/workspace/"
	}
}

func NewOpenCodeClient(logger *slog.Logger, opts ...OpenCodeClientOption) *OpenCodeClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &OpenCodeClient{
		httpClient: &http.Client{
			Timeout: openCodeScrapeTimeout,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: openCodeScrapeTimeout,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		logger:             logger,
		dashboardURLPrefix: openCodeDashboardURLPrefix,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type scrapedWindowUsage struct {
	usagePercent float64
	resetInSec   float64
}

func (c *OpenCodeClient) FetchSnapshot(ctx context.Context, workspaceID, authCookie string) (*OpenCodeSnapshot, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	authCookie = strings.TrimSpace(authCookie)
	if workspaceID == "" || authCookie == "" {
		return nil, ErrOpenCodeMissingConfig
	}

	capturedAt := time.Now().UTC()
	html, err := c.fetchDashboardHTML(ctx, workspaceID, authCookie)
	if err != nil {
		return nil, err
	}

	quotas, err := parseOpenCodeQuotas(html, capturedAt)
	if err != nil {
		return nil, err
	}

	return &OpenCodeSnapshot{
		CapturedAt:  capturedAt,
		AccountType: OpenCodeAccountTypePro,
		PlanName:    "OpenCode Go",
		Quotas:      quotas,
	}, nil
}

func (c *OpenCodeClient) fetchDashboardHTML(ctx context.Context, workspaceID, authCookie string) (string, error) {
	dashboardURL := c.dashboardURLPrefix + url.PathEscape(workspaceID) + openCodeDashboardURLSuffix

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dashboardURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request: %v", ErrOpenCodeNetworkError, err)
	}
	req.Header.Set("User-Agent", openCodeUserAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Cookie", openCodeAuthCookieHeader(authCookie))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrOpenCodeNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeMaxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("%w: read body: %v", ErrOpenCodeNetworkError, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return string(body), nil
	case http.StatusUnauthorized:
		return "", ErrOpenCodeUnauthorized
	case http.StatusForbidden:
		return "", ErrOpenCodeForbidden
	default:
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("%w: http %d", ErrOpenCodeServerError, resp.StatusCode)
		}
		return "", fmt.Errorf("%w: http %d: %s", ErrOpenCodeInvalidResponse, resp.StatusCode, sanitizeOpenCodeMessage(string(body)))
	}
}

func openCodeAuthCookieHeader(authCookie string) string {
	if strings.Contains(authCookie, "=") {
		return authCookie
	}
	return "auth=" + authCookie
}

func sanitizeOpenCodeMessage(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return "unknown"
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func parseOpenCodeQuotas(html string, capturedAt time.Time) ([]OpenCodeQuota, error) {
	rolling := parseSSRWindowUsage(html, "rollingUsage")
	weekly := parseSSRWindowUsage(html, "weeklyUsage")
	monthly := parseSSRWindowUsage(html, "monthlyUsage")

	if rolling == nil && weekly == nil && monthly == nil {
		dataSlot := parseDataSlotFormat(html)
		rolling = dataSlot["rolling"]
		weekly = dataSlot["weekly"]
		monthly = dataSlot["monthly"]
	}

	var quotas []OpenCodeQuota
	if rolling != nil {
		quotas = append(quotas, windowToQuota("five_hour", *rolling, capturedAt))
	}
	if weekly != nil {
		quotas = append(quotas, windowToQuota("weekly", *weekly, capturedAt))
	}
	if monthly != nil {
		quotas = append(quotas, windowToQuota("monthly", *monthly, capturedAt))
	}

	if len(quotas) == 0 {
		return nil, fmt.Errorf("%w: could not parse rollingUsage, weeklyUsage, or monthlyUsage", ErrOpenCodeParseFailed)
	}
	return quotas, nil
}

func windowToQuota(name string, window scrapedWindowUsage, capturedAt time.Time) OpenCodeQuota {
	pct := window.usagePercent
	if pct < 0 {
		pct = 0
	}
	resetSec := window.resetInSec
	if resetSec < 0 {
		resetSec = 0
	}
	resetsAt := capturedAt.Add(time.Duration(resetSec) * time.Second)
	return OpenCodeQuota{
		Name:        name,
		Used:        pct,
		Limit:       100,
		Utilization: pct,
		Format:      OpenCodeQuotaFormatPercent,
		ResetsAt:    &resetsAt,
	}
}

var openCodeScrapedNumberPattern = `(-?\d+(?:\.\d+)?)`

func parseSSRWindowUsage(html, prefix string) *scrapedWindowUsage {
	pattern1 := prefix + `:\$R\[\d+\]=\{[^}]*usagePercent:` + openCodeScrapedNumberPattern + `[^}]*resetInSec:` + openCodeScrapedNumberPattern
	pattern2 := prefix + `:\$R\[\d+\]=\{[^}]*resetInSec:` + openCodeScrapedNumberPattern + `[^}]*usagePercent:` + openCodeScrapedNumberPattern

	re1 := regexp.MustCompile(pattern1)
	re2 := regexp.MustCompile(pattern2)

	if m := re1.FindStringSubmatch(html); len(m) >= 3 {
		if pct, reset, ok := parseScrapedNumbers(m[1], m[2]); ok {
			return &scrapedWindowUsage{usagePercent: pct, resetInSec: reset}
		}
	}
	if m := re2.FindStringSubmatch(html); len(m) >= 3 {
		if reset, pct, ok := parseScrapedNumbers(m[1], m[2]); ok {
			return &scrapedWindowUsage{usagePercent: pct, resetInSec: reset}
		}
	}
	return nil
}

func parseScrapedNumbers(a, b string) (float64, float64, bool) {
	first, err1 := strconv.ParseFloat(a, 64)
	second, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return first, second, true
}

func parseDataSlotFormat(html string) map[string]*scrapedWindowUsage {
	result := make(map[string]*scrapedWindowUsage)
	parts := strings.Split(html, `data-slot="usage-item"`)
	for i := 1; i < len(parts); i++ {
		content := parts[i]

		labelRe := regexp.MustCompile(`data-slot="usage-label">([^<]+)<`)
		labelMatch := labelRe.FindStringSubmatch(content)
		if len(labelMatch) < 2 {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(labelMatch[1]))

		usageRe := regexp.MustCompile(`data-slot="usage-value">[^0-9]*(\d+(?:\.\d+)?)`)
		usageMatch := usageRe.FindStringSubmatch(content)
		if len(usageMatch) < 2 {
			continue
		}
		usagePercent, err := strconv.ParseFloat(usageMatch[1], 64)
		if err != nil {
			continue
		}

		resetRe := regexp.MustCompile(`data-slot="(reset-time|reset-now)">([\s\S]*?)</span>`)
		resetMatch := resetRe.FindStringSubmatch(content)
		if len(resetMatch) < 3 {
			continue
		}

		var resetInSec float64
		if resetMatch[1] == "reset-now" {
			resetInSec = 0
		} else {
			resetContent := resetMatch[2]
			resetContent = regexp.MustCompile(`<!--.*?-->`).ReplaceAllString(resetContent, "")
			resetContent = strings.TrimSpace(resetContent)
			resetContent = regexp.MustCompile(`(?i)Resets?\s*in\s*`).ReplaceAllString(resetContent, "")
			parsed, ok := parseHumanReadableTime(resetContent)
			if !ok {
				continue
			}
			resetInSec = parsed
		}

		var windowKey string
		switch {
		case strings.Contains(label, "rolling"):
			windowKey = "rolling"
		case strings.Contains(label, "weekly"):
			windowKey = "weekly"
		case strings.Contains(label, "monthly"):
			windowKey = "monthly"
		default:
			continue
		}

		result[windowKey] = &scrapedWindowUsage{
			usagePercent: usagePercent,
			resetInSec:   resetInSec,
		}
	}
	return result
}

func parseHumanReadableTime(timeStr string) (float64, bool) {
	normalized := strings.ToLower(strings.TrimSpace(timeStr))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "reset-now", "reset now", "now", "resets now":
		return 0, true
	}

	var totalSeconds float64
	hasDuration := false

	dayRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*days?`)
	hourRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*hours?`)
	minuteRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*minutes?`)
	secondRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*seconds?`)

	if m := dayRe.FindStringSubmatch(normalized); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			totalSeconds += v * 86400
			hasDuration = true
		}
	}
	if m := hourRe.FindStringSubmatch(normalized); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			totalSeconds += v * 3600
			hasDuration = true
		}
	}
	if m := minuteRe.FindStringSubmatch(normalized); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			totalSeconds += v * 60
			hasDuration = true
		}
	}
	if m := secondRe.FindStringSubmatch(normalized); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			totalSeconds += v
			hasDuration = true
		}
	}

	return totalSeconds, hasDuration
}

func IsOpenCodeAuthError(err error) bool {
	return errors.Is(err, ErrOpenCodeUnauthorized) || errors.Is(err, ErrOpenCodeForbidden)
}
