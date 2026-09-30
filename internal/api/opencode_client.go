package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const openCodeDefaultBaseURL = "https://opencode.ai"

var (
	ErrOpenCodeUnauthorized    = errors.New("opencode: unauthorized")
	ErrOpenCodeForbidden       = errors.New("opencode: forbidden")
	ErrOpenCodeServerError     = errors.New("opencode: server error")
	ErrOpenCodeNetworkError    = errors.New("opencode: network error")
	ErrOpenCodeInvalidResponse = errors.New("opencode: invalid response")
	ErrOpenCodeParseFailed     = errors.New("opencode: parse failed")
	ErrOpenCodeMissingConfig   = errors.New("opencode: missing usage api key, or workspace id and session cookie")
)

type OpenCodeClient struct {
	httpClient  *http.Client
	logger      *slog.Logger
	goStatusURL string

	usageMu     sync.Mutex
	usageStatus *openCodeGoStatus // last good go/status, reused for openCodeUsageMinInterval
	usageKey    string            // credential the cached status belongs to
	usageAt     time.Time
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
		c.goStatusURL = baseURL + openCodeGoStatusPath
	}
}

func NewOpenCodeClient(logger *slog.Logger, opts ...OpenCodeClientOption) *OpenCodeClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &OpenCodeClient{
		httpClient: &http.Client{
			Timeout: openCodeUsageTimeout,
			// A redirect is the console sending an unauthenticated request to
			// login; never follow it with the session cookie attached.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: openCodeUsageTimeout,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		logger:      logger,
		goStatusURL: openCodeDefaultBaseURL + openCodeGoStatusPath,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// FetchSnapshot reads the Go meters with the browser's console session. The
// console authenticates with the __Host-console_session cookie and needs the
// workspace ID as x-org-id; the old auth cookie and the /workspace/<id>/go
// page no longer work (issue #134).
func (c *OpenCodeClient) FetchSnapshot(ctx context.Context, workspaceID, sessionCookie string) (*OpenCodeSnapshot, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	sessionCookie = strings.TrimSpace(sessionCookie)
	if workspaceID == "" || sessionCookie == "" {
		return nil, ErrOpenCodeMissingConfig
	}
	cookieHeader := openCodeConsoleCookieHeader(sessionCookie)
	snap, err := c.goStatusSnapshot(ctx, "cookie\x00"+workspaceID+"\x00"+cookieHeader, func(req *http.Request) {
		req.Header.Set("Cookie", cookieHeader)
		req.Header.Set("x-org-id", workspaceID)
	})
	if errors.Is(err, ErrOpenCodeUnauthorized) {
		return nil, fmt.Errorf("%w: paste the __Host-console_session cookie from opencode.ai/console", err)
	}
	return snap, err
}

// openCodeConsoleCookieHeader sends a bare value as __Host-console_session and
// a pasted cookie header verbatim (minus a copied "Cookie:" prefix). A bare
// value may itself end in "=" padding, so only a named session cookie or a
// multi-cookie header is taken as-is.
func openCodeConsoleCookieHeader(value string) string {
	if len(value) > len("cookie:") && strings.EqualFold(value[:len("cookie:")], "cookie:") {
		value = strings.TrimSpace(value[len("cookie:"):])
	}
	if strings.Contains(value, "__Host-console_session=") || strings.Contains(value, ";") {
		return value
	}
	return "__Host-console_session=" + value
}

func IsOpenCodeAuthError(err error) bool {
	return errors.Is(err, ErrOpenCodeUnauthorized) || errors.Is(err, ErrOpenCodeForbidden)
}
