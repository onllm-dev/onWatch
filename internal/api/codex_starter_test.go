package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildCodexStarterRequest(t *testing.T) {
	req, err := buildCodexStarterRequest(context.Background(), codexResponsesURL, "tok_abc", "acct_123")
	if err != nil {
		t.Fatalf("buildCodexStarterRequest error: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != codexResponsesURL {
		t.Errorf("url = %q, want %q", req.URL.String(), codexResponsesURL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok_abc" {
		t.Errorf("Authorization = %q, want Bearer tok_abc", got)
	}
	if got := req.Header.Get("ChatGPT-Account-ID"); got != "acct_123" {
		t.Errorf("ChatGPT-Account-ID = %q, want acct_123", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := req.Header.Get("User-Agent"); got != "onwatch/1.0" {
		t.Errorf("User-Agent = %q, want onwatch/1.0", got)
	}

	body, _ := io.ReadAll(req.Body)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if parsed["store"] != false {
		t.Errorf("store = %v, want false", parsed["store"])
	}
	if parsed["stream"] != true {
		t.Errorf("stream = %v, want true", parsed["stream"])
	}
	if _, ok := parsed["instructions"].(string); !ok {
		t.Errorf("instructions missing or not a string")
	}
	if parsed["model"] != defaultCodexStarterModel {
		t.Errorf("model = %v, want %q", parsed["model"], defaultCodexStarterModel)
	}
}

func TestBuildCodexStarterRequest_NoAccountIDOmitsHeader(t *testing.T) {
	req, err := buildCodexStarterRequest(context.Background(), codexResponsesURL, "tok", "")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if _, ok := req.Header["Chatgpt-Account-Id"]; ok {
		t.Errorf("ChatGPT-Account-ID header should be absent when account id empty")
	}
}

func TestCodexStarterModel_EnvOverride(t *testing.T) {
	t.Setenv("CODEX_STARTER_MODEL", "custom-model")
	if got := CodexStarterModel(); got != "custom-model" {
		t.Errorf("CodexStarterModel() = %q, want custom-model", got)
	}
	os.Unsetenv("CODEX_STARTER_MODEL")
	if got := CodexStarterModel(); got != defaultCodexStarterModel {
		t.Errorf("CodexStarterModel() = %q, want default", got)
	}
}

func TestSendStarterPing_Success(t *testing.T) {
	var gotAuth, gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	defer srv.Close()

	client := NewCodexClient("tok_live", nil, WithCodexStarterURL(srv.URL))
	if err := client.SendStarterPing(context.Background(), "acct_xyz"); err != nil {
		t.Fatalf("SendStarterPing error: %v", err)
	}
	if gotAuth != "Bearer tok_live" {
		t.Errorf("server saw Authorization = %q", gotAuth)
	}
	if gotAccount != "acct_xyz" {
		t.Errorf("server saw ChatGPT-Account-ID = %q", gotAccount)
	}
}

func TestSendStarterPing_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewCodexClient("tok", nil, WithCodexStarterURL(srv.URL))
	err := client.SendStarterPing(context.Background(), "acct")
	if !errors.Is(err, ErrCodexUnauthorized) {
		t.Errorf("err = %v, want ErrCodexUnauthorized", err)
	}
}

func TestSendStarterPing_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewCodexClient("tok", nil, WithCodexStarterURL(srv.URL))
	err := client.SendStarterPing(context.Background(), "acct")
	if !errors.Is(err, ErrCodexServerError) {
		t.Errorf("err = %v, want ErrCodexServerError", err)
	}
}

func TestSendStarterPing_NoToken(t *testing.T) {
	client := NewCodexClient("", nil)
	err := client.SendStarterPing(context.Background(), "acct")
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("err = %v, want no-token error", err)
	}
}

// TestSendStarterPing_Live performs a real call against the Codex backend.
// Gated behind ONWATCH_CODEX_STARTER_LIVE=1; uses CODEX_TOKEN/CODEX_ACCOUNT_ID
// when set, otherwise auto-detects local Codex credentials. Skipped in CI.
func TestSendStarterPing_Live(t *testing.T) {
	if os.Getenv("ONWATCH_CODEX_STARTER_LIVE") != "1" {
		t.Skip("set ONWATCH_CODEX_STARTER_LIVE=1 to run the live starter ping test")
	}

	token := os.Getenv("CODEX_TOKEN")
	accountID := os.Getenv("CODEX_ACCOUNT_ID")
	if token == "" {
		creds := DetectCodexCredentials(nil)
		if creds == nil || creds.AccessToken == "" {
			t.Skip("no Codex credentials found (set CODEX_TOKEN or log in via codex)")
		}
		token = creds.AccessToken
		if accountID == "" {
			accountID = creds.AccountID
		}
		t.Logf("using detected Codex credentials: source=%v account_id_prefix=%.6s", creds.Source, accountID)
	}

	client := NewCodexClient(token, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.SendStarterPing(ctx, accountID); err != nil {
		t.Fatalf("live SendStarterPing error: %v", err)
	}
	t.Logf("live starter ping accepted (model=%s)", CodexStarterModel())
}
