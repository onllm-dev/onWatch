package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	ErrCursorUnauthorized    = errors.New("cursor: unauthorized - invalid or expired token")
	ErrCursorForbidden       = errors.New("cursor: forbidden - access denied")
	ErrCursorServerError     = errors.New("cursor: server error")
	ErrCursorNetworkError    = errors.New("cursor: network error")
	ErrCursorInvalidResponse = errors.New("cursor: invalid response")
	ErrCursorSessionExpired  = errors.New("cursor: session expired - re-authentication required")
)

const (
	CursorBaseURL       = "https://api2.cursor.sh"
	CursorClientID      = "KbZUR41cY7W6zRSdpSUJ7I7mLYBKOCmB"
	CursorRefreshBuffer = 5 * time.Minute
)

var cursorOAuthURL = "https://api2.cursor.sh/oauth/token"

type CursorClient struct {
	httpClient *http.Client
	token      string
	tokenMu    sync.RWMutex
	baseURL    string
	logger     *slog.Logger
}

type CursorOption func(*CursorClient)

func WithCursorBaseURL(url string) CursorOption {
	return func(c *CursorClient) {
		c.baseURL = url
	}
}

func WithCursorTimeout(timeout time.Duration) CursorOption {
	return func(c *CursorClient) {
		c.httpClient.Timeout = timeout
	}
}

func NewCursorClient(token string, logger *slog.Logger, opts ...CursorOption) *CursorClient {
	client := &CursorClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		token:   token,
		baseURL: CursorBaseURL,
		logger:  logger,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

func (c *CursorClient) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

func (c *CursorClient) getToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *CursorClient) GetToken() string {
	return c.getToken()
}

func (c *CursorClient) FetchQuotas(ctx context.Context) (*CursorSnapshot, error) {
	token := c.getToken()

	usage, err := c.fetchCurrentPeriodUsage(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("cursor: fetch usage: %w", err)
	}

	planInfo, err := c.fetchPlanInfo(ctx, token)
	if err != nil {
		c.logger.Warn("cursor: failed to fetch plan info, continuing without", "error", err)
	}

	planName := ""
	if planInfo != nil {
		planName = planInfo.PlanInfo.PlanName
	}
	normalizedPlan := NormalizeCursorPlanName(planName)

	var requestUsage *CursorRequestUsageResponse
	if shouldFetchCursorRequestBasedUsage(usage, normalizedPlan) {
		ru, err := c.fetchRequestBasedUsage(ctx, token)
		if err != nil {
			c.logger.Warn("cursor: failed to fetch request-based usage", "error", err)
		} else {
			requestUsage = ru
		}
	}
	useRequestBased := shouldUseCursorRequestBasedUsage(usage, requestUsage)

	var creditGrants *CursorCreditGrantsResponse
	var stripeResp *CursorStripeResponse

	if !useRequestBased {
		cg, err := c.fetchCreditGrants(ctx, token)
		if err != nil {
			c.logger.Debug("cursor: failed to fetch credit grants", "error", err)
		} else {
			creditGrants = cg
		}

		sr, err := c.fetchStripeBalance(ctx, token)
		if err != nil {
			c.logger.Debug("cursor: failed to fetch Stripe balance", "error", err)
		} else {
			stripeResp = sr
		}
	}

	accountType := DetermineCursorAccountType(planName, usage, useRequestBased)
	snapshot := ToCursorSnapshot(usage, planInfo, creditGrants, stripeResp, requestUsage, useRequestBased)

	c.logger.Debug("cursor: quotas fetched",
		"account_type", accountType,
		"plan_name", planName,
		"quota_count", len(snapshot.Quotas),
	)

	return snapshot, nil
}

func shouldFetchCursorRequestBasedUsage(usage *CursorUsageResponse, normalizedPlan string) bool {
	if usage == nil || !usage.Enabled {
		return false
	}

	hasPlanUsage := usage.PlanUsage != nil
	hasPlanUsageLimit := hasPlanUsage && usage.PlanUsage.Limit > 0
	if hasPlanUsageLimit {
		return false
	}

	return normalizedPlan == "" || normalizedPlan == "enterprise" || normalizedPlan == "team"
}

func shouldUseCursorRequestBasedUsage(usage *CursorUsageResponse, requestUsage *CursorRequestUsageResponse) bool {
	if usage == nil || !usage.Enabled || requestUsage == nil || len(requestUsage.Models) == 0 {
		return false
	}

	return usage.PlanUsage == nil || usage.PlanUsage.Limit <= 0
}

func (c *CursorClient) fetchCurrentPeriodUsage(ctx context.Context, token string) (*CursorUsageResponse, error) {
	body, err := c.connectPost(ctx, token, "/aiserver.v1.DashboardService/GetCurrentPeriodUsage", nil)
	if err != nil {
		return nil, err
	}
	return ParseCursorUsageResponse(body)
}

func (c *CursorClient) fetchPlanInfo(ctx context.Context, token string) (*CursorPlanInfoResponse, error) {
	body, err := c.connectPost(ctx, token, "/aiserver.v1.DashboardService/GetPlanInfo", nil)
	if err != nil {
		return nil, err
	}
	return ParseCursorPlanInfoResponse(body)
}

func (c *CursorClient) fetchCreditGrants(ctx context.Context, token string) (*CursorCreditGrantsResponse, error) {
	body, err := c.connectPost(ctx, token, "/aiserver.v1.DashboardService/GetCreditGrantsBalance", nil)
	if err != nil {
		return nil, err
	}
	return ParseCursorCreditGrantsResponse(body)
}

func (c *CursorClient) fetchStripeBalance(ctx context.Context, token string) (*CursorStripeResponse, error) {
	userID := ExtractJWTSubject(token)
	if userID == "" {
		c.logger.Debug("cursor: cannot extract user ID from token for Stripe endpoint")
		return nil, nil
	}

	sessionToken := url.QueryEscape(userID) + "%3A%3A" + url.QueryEscape(token)

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://cursor.com/api/auth/stripe", nil)
	if err != nil {
		return nil, fmt.Errorf("cursor: create Stripe request: %w", err)
	}
	req.Header.Set("Cookie", "WorkosCursorSessionToken="+sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCursorNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading Stripe body: %v", ErrCursorInvalidResponse, err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Debug("cursor: Stripe endpoint returned non-200", "status", resp.StatusCode)
		return nil, nil
	}

	return ParseCursorStripeResponse(body)
}

func (c *CursorClient) fetchRequestBasedUsage(ctx context.Context, token string) (*CursorRequestUsageResponse, error) {
	userID := ExtractJWTSubject(token)
	if userID == "" {
		return nil, fmt.Errorf("cursor: cannot extract user ID from token for request-based usage")
	}

	sessionToken := url.QueryEscape(userID) + "%3A%3A" + url.QueryEscape(token)

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("https://cursor.com/api/usage?user=%s", url.QueryEscape(userID))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cursor: create request-based usage request: %w", err)
	}
	req.Header.Set("Cookie", "WorkosCursorSessionToken="+sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCursorNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading usage body: %v", ErrCursorInvalidResponse, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrCursorUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrCursorForbidden
	case resp.StatusCode >= 500:
		return nil, ErrCursorServerError
	default:
		return nil, fmt.Errorf("cursor: unexpected status %d for usage endpoint", resp.StatusCode)
	}

	return ParseCursorRequestUsageResponse(body)
}

func (c *CursorClient) connectPost(ctx context.Context, token, path string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("cursor: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader([]byte("{}"))
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("cursor: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")

	c.logger.Debug("cursor: Connect RPC request", "path", path, "token", redactCursorToken(token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrCursorNetworkError, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrCursorInvalidResponse, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrCursorUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return nil, ErrCursorForbidden
	case resp.StatusCode >= 500:
		return nil, ErrCursorServerError
	default:
		return nil, fmt.Errorf("cursor: unexpected status %d for %s", resp.StatusCode, path)
	}
}

func RefreshCursorToken(ctx context.Context, refreshToken string) (*CursorOAuthResponse, error) {
	reqBody := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     CursorClientID,
		"refresh_token": refreshToken,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("cursor: marshal refresh request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cursorOAuthURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cursor: create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cursor: refresh network error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("cursor: read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cursor: refresh failed with HTTP %d: %s", resp.StatusCode, string(body))
	}

	oauthResp, err := ParseCursorOAuthResponse(body)
	if err != nil {
		return nil, fmt.Errorf("cursor: parse refresh response: %w", err)
	}

	if oauthResp.ShouldLogout {
		return nil, ErrCursorSessionExpired
	}

	if oauthResp.AccessToken == "" {
		return nil, fmt.Errorf("cursor: refresh returned empty access token")
	}

	return oauthResp, nil
}

func redactCursorToken(token string) string {
	if token == "" {
		return "(empty)"
	}
	if len(token) < 8 {
		return "***...***"
	}
	return token[:4] + "***...***" + token[len(token)-3:]
}

func NeedsCursorRefresh(creds *CursorCredentials) bool {
	if creds == nil || creds.AccessToken == "" {
		return true
	}
	if creds.ExpiresAt.IsZero() {
		return false
	}
	return creds.ExpiresIn < CursorRefreshBuffer
}

// IsCursorAuthError returns true if the error indicates an auth failure (401/403).
func IsCursorAuthError(err error) bool {
	return errors.Is(err, ErrCursorUnauthorized) || errors.Is(err, ErrCursorForbidden)
}

// IsCursorSessionExpired returns true if the error indicates the session is expired.
func IsCursorSessionExpired(err error) bool {
	return errors.Is(err, ErrCursorSessionExpired)
}

// WriteCursorTokenToSQLite writes a token back to Cursor's state.vscdb.
func WriteCursorTokenToSQLite(dbPath, key, value string) error {
	// This needs to use database/sql with the modernc.org/sqlite driver
	// to write back to Cursor's state.vscdb. We open it as a separate connection.
	// We use parameterized queries for safety.
	db, err := openCursorStateDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(
		"INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

func openCursorStateDB(dbPath string) (*sql.DB, error) {
	//nolint:rowserrcheck
	db, err := sql.Open("sqlite", dbPath+"?mode=rw")
	if err != nil {
		return nil, fmt.Errorf("cursor: open state db: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
