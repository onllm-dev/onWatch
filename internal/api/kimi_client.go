package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Errors for Kimi Code API failures.
var (
	ErrKimiUnauthorized    = errors.New("kimi: unauthorized - invalid or expired token")
	ErrKimiRateLimited     = errors.New("kimi: rate limited")
	ErrKimiServerError     = errors.New("kimi: server error")
	ErrKimiNetworkError    = errors.New("kimi: network error")
	ErrKimiInvalidResponse = errors.New("kimi: invalid response")
	ErrKimiNoCredentials   = errors.New("kimi: no credentials found")
)

// KimiClient polls Kimi Code usage quotas (OAuth).
type KimiClient struct {
	httpClient *http.Client
	baseURL    string
	oauthHost  string
	clientID   string
	logger     *slog.Logger
	// optional static token (Docker / env override). When empty, loads from disk + refresh.
	staticToken string
}

// KimiOption configures a KimiClient.
type KimiOption func(*KimiClient)

// WithKimiBaseURL sets the coding API base URL (default https://api.kimi.com/coding/v1).
func WithKimiBaseURL(u string) KimiOption {
	return func(c *KimiClient) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithKimiOAuthHost sets the OAuth host (default https://auth.kimi.com).
func WithKimiOAuthHost(u string) KimiOption {
	return func(c *KimiClient) { c.oauthHost = strings.TrimRight(u, "/") }
}

// WithKimiTimeout sets the HTTP client timeout.
func WithKimiTimeout(d time.Duration) KimiOption {
	return func(c *KimiClient) { c.httpClient.Timeout = d }
}

// WithKimiStaticToken forces a bearer token (skips disk credentials / refresh).
func WithKimiStaticToken(token string) KimiOption {
	return func(c *KimiClient) { c.staticToken = strings.TrimSpace(token) }
}

// NewKimiClient creates a Kimi Code API client.
// token may be empty when using auto-detected OAuth credentials.
func NewKimiClient(token string, logger *slog.Logger, opts ...KimiOption) *KimiClient {
	if logger == nil {
		logger = slog.Default()
	}
	base := os.Getenv("KIMI_CODE_BASE_URL")
	if base == "" {
		base = DefaultKimiCodeBase
	}
	oauth := os.Getenv("KIMI_CODE_OAUTH_HOST")
	if oauth == "" {
		oauth = os.Getenv("KIMI_OAUTH_HOST")
	}
	if oauth == "" {
		oauth = DefaultKimiOAuthHost
	}
	c := &KimiClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          2,
				MaxIdleConnsPerHost:   2,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		baseURL:     strings.TrimRight(base, "/"),
		oauthHost:   strings.TrimRight(oauth, "/"),
		clientID:    KimiCodeClientID,
		logger:      logger,
		staticToken: strings.TrimSpace(token),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// FetchSnapshot retrieves current Kimi Code quotas and returns a snapshot.
func (c *KimiClient) FetchSnapshot(ctx context.Context) (*KimiSnapshot, error) {
	token, err := c.resolveAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	body, err := c.getUsages(ctx, token)
	if err != nil {
		// One refresh retry on 401 when using disk credentials.
		if errors.Is(err, ErrKimiUnauthorized) && c.staticToken == "" {
			if rerr := c.refreshAndPersist(ctx); rerr == nil {
				token, err = c.resolveAccessToken(ctx)
				if err == nil {
					body, err = c.getUsages(ctx, token)
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}

	var resp KimiUsagesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKimiInvalidResponse, err)
	}
	snap := resp.ToSnapshot(time.Now().UTC())
	return snap, nil
}

func (c *KimiClient) getUsages(ctx context.Context, token string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := c.baseURL + "/usages"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("kimi: creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "onwatch/kimi-code")

	c.logger.Debug("fetching Kimi Code usages", "url", url)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKimiNetworkError, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", ErrKimiNetworkError, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrKimiUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrKimiRateLimited
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrKimiServerError, resp.StatusCode)
	default:
		return nil, fmt.Errorf("%w: status %d: %s", ErrKimiInvalidResponse, resp.StatusCode, truncateKimiBody(body))
	}
}

func (c *KimiClient) resolveAccessToken(ctx context.Context) (string, error) {
	if c.staticToken != "" {
		return c.staticToken, nil
	}
	creds := LoadKimiCredentialsCached(c.logger, false)
	if creds == nil {
		return "", ErrKimiNoCredentials
	}
	if !creds.Expired() && creds.AccessToken != "" {
		return creds.AccessToken, nil
	}
	if err := c.refreshAndPersist(ctx); err != nil {
		// fall back to stale access token if refresh failed but we still have one
		if creds.AccessToken != "" {
			c.logger.Warn("kimi: refresh failed, trying existing access token", "error", err)
			return creds.AccessToken, nil
		}
		return "", err
	}
	creds = LoadKimiCredentialsCached(c.logger, true)
	if creds == nil || creds.AccessToken == "" {
		return "", ErrKimiNoCredentials
	}
	return creds.AccessToken, nil
}

func (c *KimiClient) refreshAndPersist(ctx context.Context) error {
	// Try every known credential store (kimi-code + kimi-cli). After migration,
	// one path may hold a dead refresh token while the other still works.
	candidates := DetectAllKimiCredentials(c.logger)
	if len(candidates) == 0 {
		return ErrKimiNoCredentials
	}

	var lastErr error
	for _, creds := range candidates {
		if creds.RefreshToken == "" {
			continue
		}
		token, err := c.refreshToken(ctx, creds.RefreshToken)
		if err != nil {
			lastErr = err
			c.logger.Debug("kimi: refresh failed for credentials",
				"source", creds.Source, "path", creds.Path, "error", err)
			continue
		}
		creds.AccessToken = token.AccessToken
		if token.RefreshToken != "" {
			creds.RefreshToken = token.RefreshToken
		}
		if token.ExpiresIn > 0 {
			creds.ExpiresIn = float64(token.ExpiresIn)
			creds.ExpiresAt = float64(time.Now().Unix() + int64(token.ExpiresIn))
		}
		if token.TokenType != "" {
			creds.TokenType = token.TokenType
		}
		if token.Scope != "" {
			creds.Scope = token.Scope
		}
		if err := SaveKimiCredentials(creds); err != nil {
			c.logger.Warn("kimi: failed to persist refreshed credentials",
				"source", creds.Source, "path", creds.Path, "error", err)
		}
		InvalidateKimiCredentialsCache()
		// Prefer the refreshed set on next load
		kimiCredMu.Lock()
		kimiCredCache = creds
		kimiCredAt = time.Now()
		kimiCredMu.Unlock()
		c.logger.Info("Kimi Code token refreshed from credentials",
			"source", creds.Source, "path", creds.Path)
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return ErrKimiNoCredentials
}

type kimiTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func (c *KimiClient) refreshToken(ctx context.Context, refreshToken string) (*kimiTokenResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.clientID)

	endpoint := c.oauthHost + "/api/oauth/token"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("kimi: refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "onwatch/kimi-code")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKimiNetworkError, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: reading refresh body: %v", ErrKimiNetworkError, err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrKimiUnauthorized
		}
		return nil, fmt.Errorf("%w: refresh status %d: %s", ErrKimiInvalidResponse, resp.StatusCode, truncateKimiBody(body))
	}
	var tok kimiTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("%w: refresh parse: %v", ErrKimiInvalidResponse, err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty access_token on refresh", ErrKimiInvalidResponse)
	}
	return &tok, nil
}

func truncateKimiBody(b []byte) string {
	const max = 200
	s := string(b)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
