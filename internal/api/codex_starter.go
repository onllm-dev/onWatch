package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// codexResponsesURL is the ChatGPT-backed Codex Responses endpoint. Sending a
// minimal generation request here consumes a tiny amount of quota, which is what
// "starts" a Codex limit window after a reset (a usage GET does not start it).
const codexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

// defaultCodexStarterModel is the model used for the auto quota-starter ping.
// ChatGPT-account Codex access only supports a small set of models (currently
// gpt-5.5, gpt-5.4, gpt-5.4-mini); codex-specific slugs are rejected. The model
// is overridable via CODEX_STARTER_MODEL without a rebuild (this feature is Beta).
const defaultCodexStarterModel = "gpt-5.5"

// CodexStarterModel returns the model id used for the auto quota-starter ping,
// allowing a CODEX_STARTER_MODEL env override for the (Beta) feature.
func CodexStarterModel() string {
	if m := strings.TrimSpace(os.Getenv("CODEX_STARTER_MODEL")); m != "" {
		return m
	}
	return defaultCodexStarterModel
}

// codexStarterBody builds a minimal Responses API payload for a starter ping.
// It resembles a basic Codex request and asks the model to reply with the short
// string "Quota Resumed" so generation stays tiny while still being a real turn
// that starts the quota window.
func codexStarterBody() map[string]any {
	// Field set mirrors the always-serialized fields of the Codex CLI's
	// ResponsesApiRequest (codex-rs/codex-api/src/common.rs). tools is empty and
	// tool_choice is "none" since the starter never needs tools.
	return map[string]any{
		"model":        CodexStarterModel(),
		"instructions": "You are Codex, a coding assistant. Follow the user's instruction exactly and keep the reply minimal.",
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": `Reply with exactly the string "Quota Resumed" and nothing else.`},
				},
			},
		},
		"tools":               []any{},
		"tool_choice":         "none",
		"parallel_tool_calls": false,
		"reasoning":           nil,
		"include":             []any{},
		"store":               false,
		"stream":              true,
	}
}

// buildCodexStarterRequest constructs the POST request for a starter ping.
// Kept separate from sending so request shape is unit-testable.
func buildCodexStarterRequest(ctx context.Context, responsesURL, token, accountID string) (*http.Request, error) {
	raw, err := json.Marshal(codexStarterBody())
	if err != nil {
		return nil, fmt.Errorf("codex starter: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("codex starter: creating request: %w", err)
	}

	// Header set mirrors the real Codex CLI's HTTP Responses request
	// (codex-rs/model-provider bearer auth + default "originator" header).
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "onwatch/1.0")
	req.Header.Set("originator", "codex_cli_rs")
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	return req, nil
}

// SendStarterPing sends a minimal Codex generation request to start a limit
// window after a reset. On success it reads the SSE stream to completion so the
// turn actually commits server-side (a turn that is cancelled mid-stream is NOT
// counted and does not anchor/start the window). Errors are returned (never
// retried here) so callers can log without spamming the API.
//
// The read is bounded by the request context deadline (set by the caller), not
// the shared usage client's short timeout, so it uses a dedicated client that
// reuses the configured transport.
func (c *CodexClient) SendStarterPing(ctx context.Context, accountID string) error {
	token := c.getToken()
	if token == "" {
		return fmt.Errorf("codex starter: no token available")
	}

	url := c.starterURL
	if url == "" {
		url = codexResponsesURL
	}

	req, err := buildCodexStarterRequest(ctx, url, token, accountID)
	if err != nil {
		return err
	}

	// Dedicated client with no client-level timeout: a streamed completion must
	// be read to the end, bounded by the caller's context, not the 10s usage cap.
	pingClient := &http.Client{Transport: c.httpClient.Transport}
	resp, err := pingClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrCodexNetworkError, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// Read the whole SSE stream (through response.completed) so the turn
		// commits and the quota window is anchored. Bounded by ctx.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrCodexUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return ErrCodexForbidden
	case resp.StatusCode >= 500:
		return ErrCodexServerError
	default:
		return fmt.Errorf("codex starter: unexpected status code %d", resp.StatusCode)
	}
}
