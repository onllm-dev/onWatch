package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

func setupReader(lines ...string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

// noAnthropicAutoDetect points HOME at an empty sandbox so the wizard takes
// the manual-entry path.
func noAnthropicAutoDetect(t *testing.T) {
	t.Helper()
	testhome.SetTestHome(t, t.TempDir())
	t.Setenv("PATH", "")
}

func TestCheckSetupCredential(t *testing.T) {
	rejected := func(err error) bool { return err == api.ErrUnauthorized }

	cases := []struct {
		name  string
		err   error
		input string
		want  credentialOutcome
	}{
		{"verified", nil, "", credentialKeep},
		{"rejected, default retries", api.ErrUnauthorized, "\n", credentialRetry},
		{"rejected, yes retries", api.ErrUnauthorized, "y\n", credentialRetry},
		{"rejected, user keeps it", api.ErrUnauthorized, "n\n", credentialKeep},
		{"network error keeps it", api.ErrNetworkError, "", credentialKeep},
		{"server error keeps it", api.ErrServerError, "", credentialKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSetupCredential(bufio.NewReader(strings.NewReader(tc.input)),
				"Synthetic", "api.synthetic.new", "hint", "syn_value_1234",
				func() error { return tc.err }, rejected)
			if got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRejectionClassifiers(t *testing.T) {
	wrapped := func(err error) error { return fmt.Errorf("outer: %w", err) }
	cases := []struct {
		name string
		fn   func(error) bool
		err  error
		want bool
	}{
		{"synthetic 401", isSyntheticRejection, api.ErrUnauthorized, true},
		{"synthetic network", isSyntheticRejection, wrapped(api.ErrNetworkError), false},
		{"zai 401", isZaiRejection, api.ErrZaiUnauthorized, true},
		{"zai api error", isZaiRejection, api.ErrZaiAPIError, false},
		{"anthropic 401", isAnthropicRejection, api.ErrAnthropicUnauthorized, true},
		{"anthropic 403", isAnthropicRejection, api.ErrAnthropicForbidden, true},
		{"anthropic 429 is not a rejection", isAnthropicRejection, api.ErrAnthropicRateLimited, false},
		{"anthropic network", isAnthropicRejection, wrapped(api.ErrAnthropicNetworkError), false},
		{"codex 401", isCodexRejection, api.ErrCodexUnauthorized, true},
		{"codex 403 is not a rejection", isCodexRejection, api.ErrCodexForbidden, false},
		{"codex blocked", isCodexRejection, api.ErrCodexAccessBlocked, false},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTokenShapeProblems(t *testing.T) {
	if anthropicTokenShapeProblem("sk-ant-api03-abcdef") == "" {
		t.Error("an Anthropic Console API key must be refused")
	}
	for _, ok := range []string{"sk-ant-oat01-abcdef", "opaque-token", ""} {
		if p := anthropicTokenShapeProblem(ok); p != "" {
			t.Errorf("anthropicTokenShapeProblem(%q) = %q, want none", ok, p)
		}
	}
	for _, bad := range []string{"sk-proj-abcdef", "sk-abcdef"} {
		if codexTokenShapeProblem(bad) == "" {
			t.Errorf("codexTokenShapeProblem(%q) should refuse an OpenAI API key", bad)
		}
	}
	for _, ok := range []string{"eyJhbGciOiJSUzI1NiJ9.e30.sig", "codex-manual"} {
		if p := codexTokenShapeProblem(ok); p != "" {
			t.Errorf("codexTokenShapeProblem(%q) = %q, want none", ok, p)
		}
	}
}

func TestCollectSyntheticKey_RejectedKeyIsReprompted(t *testing.T) {
	orig := verifySyntheticKey
	t.Cleanup(func() { verifySyntheticKey = orig })
	var seen []string
	verifySyntheticKey = func(key string) error {
		seen = append(seen, key)
		if key == "syn_badkey1" {
			return api.ErrUnauthorized
		}
		return nil
	}

	got := collectSyntheticKey(setupReader("syn_badkey1", "", "syn_goodkey1"))
	if got != "syn_goodkey1" {
		t.Fatalf("got %q, want the second key", got)
	}
	if strings.Join(seen, ",") != "syn_badkey1,syn_goodkey1" {
		t.Fatalf("verified %v", seen)
	}
}

func TestCollectSyntheticKey_OfflineKeepsKey(t *testing.T) {
	orig := verifySyntheticKey
	t.Cleanup(func() { verifySyntheticKey = orig })
	verifySyntheticKey = func(string) error { return api.ErrNetworkError }

	if got := collectSyntheticKey(setupReader("syn_offline1")); got != "syn_offline1" {
		t.Fatalf("got %q", got)
	}
}

func TestCollectZaiConfig_VerifiesAgainstChosenBaseURL(t *testing.T) {
	orig := verifyZaiKey
	t.Cleanup(func() { verifyZaiKey = orig })

	t.Run("default url, rejected then replaced", func(t *testing.T) {
		var seen []string
		verifyZaiKey = func(key, baseURL string) error {
			seen = append(seen, key+"@"+baseURL)
			if key == "bad" {
				return api.ErrZaiUnauthorized
			}
			return nil
		}
		key, url := collectZaiConfig(setupReader("bad", "", "", "good", ""))
		if key != "good" || url != "https://api.z.ai/api" {
			t.Fatalf("got (%q, %q)", key, url)
		}
		want := "bad@https://api.z.ai/api,good@https://api.z.ai/api"
		if strings.Join(seen, ",") != want {
			t.Fatalf("verified %v, want %s", seen, want)
		}
	})

	t.Run("custom url", func(t *testing.T) {
		var gotURL string
		verifyZaiKey = func(_, baseURL string) error { gotURL = baseURL; return nil }
		key, url := collectZaiConfig(setupReader("k", "n", "http://insecure", "https://open.bigmodel.cn/api"))
		if key != "k" || url != "https://open.bigmodel.cn/api" || gotURL != url {
			t.Fatalf("got (%q, %q), verified against %q", key, url, gotURL)
		}
	})

	// A key for the China region rejected at the global URL: after the
	// rejection the user can switch the URL, not only retype the key.
	t.Run("rejected, then region URL changed", func(t *testing.T) {
		var seen []string
		verifyZaiKey = func(key, baseURL string) error {
			seen = append(seen, key+"@"+baseURL)
			if baseURL != "https://open.bigmodel.cn/api" {
				return api.ErrZaiUnauthorized
			}
			return nil
		}
		key, url := collectZaiConfig(setupReader("cnkey", "", "", "cnkey", "n", "https://open.bigmodel.cn/api"))
		if key != "cnkey" || url != "https://open.bigmodel.cn/api" {
			t.Fatalf("got (%q, %q), want the key with the China URL", key, url)
		}
		want := "cnkey@https://api.z.ai/api,cnkey@https://open.bigmodel.cn/api"
		if strings.Join(seen, ",") != want {
			t.Fatalf("verified %v, want %s", seen, want)
		}
	})

	// The saved URL is the checked URL: no trailing slash for the daemon to
	// double, and a bare scheme is refused.
	t.Run("rejected with a custom URL: Enter keeps that URL", func(t *testing.T) {
		var seen []string
		calls := 0
		verifyZaiKey = func(key, baseURL string) error {
			seen = append(seen, key+"@"+baseURL)
			calls++
			if calls == 1 {
				return api.ErrZaiUnauthorized
			}
			return nil
		}
		// China URL chosen, key mistyped, retyped, Enter at the URL question.
		key, url := collectZaiConfig(setupReader("typo", "n", "", "", "cnkey", ""))
		if key != "cnkey" || url != "https://open.bigmodel.cn/api" {
			t.Fatalf("got (%q, %q), want the retyped key with the China URL kept", key, url)
		}
		want := "typo@https://open.bigmodel.cn/api,cnkey@https://open.bigmodel.cn/api"
		if strings.Join(seen, ",") != want {
			t.Fatalf("verified %v, want %s", seen, want)
		}
	})
	t.Run("trailing slash trimmed, bare scheme refused", func(t *testing.T) {
		var gotURL string
		verifyZaiKey = func(_, baseURL string) error { gotURL = baseURL; return nil }
		_, url := collectZaiConfig(setupReader("k", "n", "https://", "https:///", "https:///api", "https://:8443/api", "https://user@evil.example/api", "http://open.bigmodel.cn/api",
			"https://open.bigmodel.cn/api?x=1", "https://open.bigmodel.cn/api?", "https://open.bigmodel.cn/api#frag", "https://open.bigmodel.cn/api/"))
		if url != "https://open.bigmodel.cn/api" || gotURL != url {
			t.Fatalf("saved %q, verified %q, want both https://open.bigmodel.cn/api", url, gotURL)
		}
	})

	t.Run("scheme case and trailing slashes normalized", func(t *testing.T) {
		verifyZaiKey = func(string, string) error { return nil }
		_, url := collectZaiConfig(setupReader("k", "n", "HTTPS://proxy.example:8443/zai//"))
		if url != "https://proxy.example:8443/zai" {
			t.Fatalf("saved %q", url)
		}
	})

	t.Run("rejected but kept", func(t *testing.T) {
		verifyZaiKey = func(string, string) error { return api.ErrZaiUnauthorized }
		key, _ := collectZaiConfig(setupReader("keepme", "", "n"))
		if key != "keepme" {
			t.Fatalf("got %q", key)
		}
	})
}

// Issue #161: a Console API key typed where the OAuth token belongs is
// refused before any request, and a rejected token is asked for again.
func TestCollectAnthropicToken_ManualEntryChecks(t *testing.T) {
	noAnthropicAutoDetect(t)
	orig := verifyAnthropicToken
	t.Cleanup(func() { verifyAnthropicToken = orig })
	var seen []string
	verifyAnthropicToken = func(token string) error {
		seen = append(seen, token)
		if token == "sk-ant-oat01-expired" {
			return api.ErrAnthropicUnauthorized
		}
		return nil
	}

	got := collectAnthropicToken(setupReader("sk-ant-api03-console", "sk-ant-oat01-expired", "", "sk-ant-oat01-good"), testLogger())
	if got != "sk-ant-oat01-good" {
		t.Fatalf("got %q", got)
	}
	if strings.Join(seen, ",") != "sk-ant-oat01-expired,sk-ant-oat01-good" {
		t.Fatalf("verified %v; the Console API key must never be sent", seen)
	}
}

func TestCollectAnthropicToken_RateLimitedKeepsToken(t *testing.T) {
	noAnthropicAutoDetect(t)
	orig := verifyAnthropicToken
	t.Cleanup(func() { verifyAnthropicToken = orig })
	verifyAnthropicToken = func(string) error { return api.ErrAnthropicRateLimited }

	if got := collectAnthropicToken(setupReader("sk-ant-oat01-x"), testLogger()); got != "sk-ant-oat01-x" {
		t.Fatalf("got %q", got)
	}
}

// An auto-detected token may be expired but refreshable, so it is never
// checked (and never rejected) at setup time.
func TestCollectAnthropicToken_AutoDetectedTokenIsNotVerified(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"auto-tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := verifyAnthropicToken
	t.Cleanup(func() { verifyAnthropicToken = orig })
	calls := 0
	verifyAnthropicToken = func(string) error { calls++; return api.ErrAnthropicUnauthorized }

	if got := collectAnthropicToken(setupReader(""), testLogger()); got != "auto-tok" || calls != 0 {
		t.Fatalf("got %q after %d checks", got, calls)
	}
}

func TestCollectCodexToken_ManualEntryChecks(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "none"))
	orig := verifyCodexToken
	t.Cleanup(func() { verifyCodexToken = orig })
	var seen []string
	verifyCodexToken = func(token string) error {
		seen = append(seen, token)
		if token == "revoked" {
			return api.ErrCodexUnauthorized
		}
		return nil
	}

	got := collectCodexToken(setupReader("sk-proj-apikey", "revoked", "y", "eyJgood"), testLogger())
	if got != "eyJgood" {
		t.Fatalf("got %q", got)
	}
	if strings.Join(seen, ",") != "revoked,eyJgood" {
		t.Fatalf("verified %v; the OpenAI API key must never be sent", seen)
	}
}

func TestCollectCodexToken_ForbiddenIsNotTreatedAsRejection(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "none"))
	orig := verifyCodexToken
	t.Cleanup(func() { verifyCodexToken = orig })
	verifyCodexToken = func(string) error { return api.ErrCodexForbidden }

	if got := collectCodexToken(setupReader("eyJtoken"), testLogger()); got != "eyJtoken" {
		t.Fatalf("got %q", got)
	}
}

// The live Z.ai check calls the same quota path the daemon polls, under the
// base URL the user picked, and maps Z.ai's in-body 401 to a rejection.
func TestLiveVerifyZaiKey(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if gotAuth == "good" {
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"limits":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":401,"success":false,"msg":"token expired or incorrect"}`))
	}))
	defer srv.Close()

	if err := liveVerifyZaiKey("good", srv.URL+"/api/"); err != nil {
		t.Fatalf("good key: %v", err)
	}
	if gotPath != "/api/monitor/usage/quota/limit" {
		t.Fatalf("path = %q, want the daemon's quota path", gotPath)
	}
	err := liveVerifyZaiKey("bad", srv.URL+"/api")
	if !isZaiRejection(err) {
		t.Fatalf("bad key err = %v, want a rejection", err)
	}
	if err := liveVerifyZaiKey("k", "http://127.0.0.1:1/api"); err == nil || isZaiRejection(err) {
		t.Fatalf("unreachable host err = %v, want a non-rejection error", err)
	}
}

// statusServer answers every request with status and a body that echoes the
// credential, so a test can check the credential never ends up in an error
// message the wizard prints.
func statusServer(t *testing.T, status int, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"bad credential %s"}}`, r.Header.Get("Authorization"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Each live check, run against a real HTTP server, must sort every kind of
// answer into "rejected" (ask again) or "not verified" (keep the credential),
// and must never print the credential back.
func TestLiveVerifiers_StatusClassification(t *testing.T) {
	type verifier struct {
		name     string
		run      func(url, secret string) error
		rejected func(error) bool
		rejects  map[int]bool
	}
	verifiers := []verifier{
		{
			name: "synthetic",
			run: func(url, secret string) error {
				orig := syntheticVerifyOpts
				syntheticVerifyOpts = []api.Option{api.WithBaseURL(url)}
				defer func() { syntheticVerifyOpts = orig }()
				return liveVerifySyntheticKey(secret)
			},
			rejected: isSyntheticRejection,
			rejects:  map[int]bool{401: true},
		},
		{
			name: "anthropic",
			run: func(url, secret string) error {
				orig := anthropicVerifyOpts
				anthropicVerifyOpts = []api.AnthropicOption{api.WithAnthropicBaseURL(url)}
				defer func() { anthropicVerifyOpts = orig }()
				return liveVerifyAnthropicToken(secret)
			},
			rejected: isAnthropicRejection,
			rejects:  map[int]bool{401: true, 403: true},
		},
		{
			name: "codex",
			run: func(url, secret string) error {
				orig := codexVerifyOpts
				codexVerifyOpts = []api.CodexOption{api.WithCodexBaseURL(url)}
				defer func() { codexVerifyOpts = orig }()
				return liveVerifyCodexToken(secret)
			},
			rejected: isCodexRejection,
			rejects:  map[int]bool{401: true},
		},
	}
	statuses := []int{400, 401, 403, 404, 429, 500, 502, 503}
	const secret = "secret-credential-value-1234567890"
	for _, v := range verifiers {
		for _, status := range statuses {
			t.Run(fmt.Sprintf("%s %d", v.name, status), func(t *testing.T) {
				srv := statusServer(t, status, 0)
				err := v.run(srv.URL, secret)
				if err == nil {
					t.Fatalf("status %d: got nil error, want a failure", status)
				}
				if got := v.rejected(err); got != v.rejects[status] {
					t.Fatalf("status %d: rejected = %v, want %v (err %v)", status, got, v.rejects[status], err)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("status %d: error message leaks the credential: %v", status, err)
				}
			})
		}
		t.Run(v.name+" unreachable", func(t *testing.T) {
			srv := httptest.NewServer(http.NotFoundHandler())
			url := srv.URL
			srv.Close()
			err := v.run(url, secret)
			if err == nil || v.rejected(err) {
				t.Fatalf("closed port: err = %v, want a non-rejection error", err)
			}
		})
	}
}

// A provider that does not answer in time costs one timeout, then the
// credential is kept with a warning instead of blocking setup.
func TestLiveVerifiers_TimeoutIsNotARejection(t *testing.T) {
	origTimeout := setupVerifyTimeout
	setupVerifyTimeout = 150 * time.Millisecond
	t.Cleanup(func() { setupVerifyTimeout = origTimeout })
	srv := statusServer(t, http.StatusOK, 2*time.Second)

	origSyn := syntheticVerifyOpts
	syntheticVerifyOpts = []api.Option{api.WithBaseURL(srv.URL)}
	t.Cleanup(func() { syntheticVerifyOpts = origSyn })

	start := time.Now()
	err := liveVerifySyntheticKey("syn_slow")
	if err == nil || isSyntheticRejection(err) {
		t.Fatalf("slow server: err = %v, want a non-rejection error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("check took %v, want it bounded by the setup timeout", elapsed)
	}
	if err := liveVerifyZaiKey("k", srv.URL); err == nil || isZaiRejection(err) {
		t.Fatalf("slow Z.ai server: err = %v, want a non-rejection error", err)
	}
}

// Z.ai answers HTTP 200 with its own code in the body. Code 401, its 1000-1004
// authentication codes, and an HTTP 401 are rejections; other failures,
// including a bare 403 or a non-JSON error page, keep the key.
func TestLiveVerifyZaiKey_ErrorShapes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		reject bool
	}{
		{"code 401 in body", 200, `{"code":401,"success":false,"msg":"token expired or incorrect"}`, true},
		{"auth code 1001 in body", 200, `{"code":1001,"success":false,"msg":"Authorization header missing"}`, true},
		{"auth code 1002 with HTTP 401", 401, `{"code":1002,"success":false,"msg":"invalid token"}`, true},
		{"auth code 1003 expired", 200, `{"code":1003,"success":false,"msg":"token expired"}`, true},
		{"HTTP 401, no body", 401, ``, true},
		{"HTTP 403 firewall page is not a rejection", 403, `<html>forbidden</html>`, false},
		{"HTTP 403 with auth code", 403, `{"code":1002,"success":false,"msg":"invalid token"}`, true},
		{"balance code 1113 is not a rejection", 200, `{"code":1113,"success":false,"msg":"insufficient balance"}`, false},
		{"other API error", 200, `{"code":500,"success":false,"msg":"busy"}`, false},
		{"HTTP 429", 429, `{"code":1302,"success":false,"msg":"rate limited"}`, false},
		{"HTML error page", 502, `<html>bad gateway</html>`, false},
		{"empty body", 200, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			err := liveVerifyZaiKey("zai-secret-key", srv.URL)
			if err == nil {
				t.Fatal("got nil error, want a failure")
			}
			if isZaiRejection(err) != tc.reject {
				t.Fatalf("rejected = %v, want %v (err %v)", isZaiRejection(err), tc.reject, err)
			}
			if strings.Contains(err.Error(), "zai-secret-key") {
				t.Fatalf("error leaks the key: %v", err)
			}
		})
	}
}

// When input ends after a rejection (stdin closed or piped), the wizard keeps
// the credential instead of looping on prompts nobody can answer.
func TestCheckSetupCredential_EndOfInputKeepsCredential(t *testing.T) {
	for _, input := range []string{"", "   "} {
		got := checkSetupCredential(bufio.NewReader(strings.NewReader(input)),
			"Synthetic", "api.synthetic.new", "", "syn_value_1234",
			func() error { return api.ErrUnauthorized }, isSyntheticRejection)
		if got != credentialKeep {
			t.Fatalf("input %q: outcome = %v, want keep at end of input", input, got)
		}
	}
	// A typed answer on the last line still counts, even without a newline.
	got := checkSetupCredential(bufio.NewReader(strings.NewReader("y")),
		"Synthetic", "api.synthetic.new", "", "syn_value_1234",
		func() error { return api.ErrUnauthorized }, isSyntheticRejection)
	if got != credentialRetry {
		t.Fatalf("outcome = %v, want retry for a final \"y\"", got)
	}
}

// End to end: a rejected Synthetic key with no more input returns that key
// rather than hanging.
func TestCollectSyntheticKey_RejectedAtEndOfInputDoesNotHang(t *testing.T) {
	orig := verifySyntheticKey
	t.Cleanup(func() { verifySyntheticKey = orig })
	verifySyntheticKey = func(string) error { return api.ErrUnauthorized }

	done := make(chan string, 1)
	go func() { done <- collectSyntheticKey(bufio.NewReader(strings.NewReader("syn_only_key_1234\n"))) }()
	select {
	case got := <-done:
		if got != "syn_only_key_1234" {
			t.Fatalf("key = %q, want the typed key kept", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("collectSyntheticKey hung at end of input")
	}
}

func TestTokenWhitespaceProblem(t *testing.T) {
	for _, bad := range []string{"Bearer sk-ant-oat01-abc", "bearer eyJabc", "sk-ant-oat01 abc", "eyJ\tabc"} {
		if anthropicTokenShapeProblem(bad) == "" || codexTokenShapeProblem(bad) == "" {
			t.Errorf("%q should be refused for containing a prefix or spaces", bad)
		}
	}
	for _, ok := range []string{"sk-ant-oat01-abc", "eyJhbGciOiJSUzI1NiJ9.e30.sig"} {
		if p := tokenWhitespaceProblem(ok); p != "" {
			t.Errorf("tokenWhitespaceProblem(%q) = %q, want none", ok, p)
		}
	}
}

// A pasted token with surrounding spaces or a trailing CR (Windows paste) is
// trimmed before it is checked and saved.
func TestCollectAnthropicToken_TrimsPastedWhitespace(t *testing.T) {
	noAnthropicAutoDetect(t)
	orig := verifyAnthropicToken
	t.Cleanup(func() { verifyAnthropicToken = orig })
	var seen string
	verifyAnthropicToken = func(tok string) error { seen = tok; return nil }

	got := collectAnthropicToken(bufio.NewReader(strings.NewReader("  sk-ant-oat01-padded \r\n")), setupQuietLogger())
	if got != "sk-ant-oat01-padded" || seen != got {
		t.Fatalf("token = %q, verified %q, want the trimmed token", got, seen)
	}
}

// A pasted token is confirmed once, after its checks, never with an "ok"
// that a refusal then contradicts.
func TestCollectTokens_NoOkBeforeTheChecks(t *testing.T) {
	noAnthropicAutoDetect(t)
	origA, origC := verifyAnthropicToken, verifyCodexToken
	t.Cleanup(func() { verifyAnthropicToken, verifyCodexToken = origA, origC })
	verifyAnthropicToken = func(string) error { return nil }
	verifyCodexToken = func(string) error { return nil }

	out := captureStdout(t, func() {
		collectAnthropicToken(setupReader("sk-ant-api03-console", "sk-ant-oat01-real-token"), setupQuietLogger())
	})
	if strings.Count(out, " ok ") != 1 {
		t.Fatalf("want exactly one ok line, got:\n%s", out)
	}
	if strings.Index(out, "Console API key") > strings.Index(out, " ok ") {
		t.Fatalf("the refusal must come before the only ok line:\n%s", out)
	}

	out = captureStdout(t, func() {
		collectCodexToken(setupReader("eyJcodex-token-value"), setupQuietLogger())
	})
	if strings.Count(out, " ok ") != 1 {
		t.Fatalf("want exactly one ok line for Codex, got:\n%s", out)
	}
}

// expectInputEnded runs fn on its own goroutine and fails unless it stops the
// wizard with errSetupInputEnded quickly, instead of looping on a prompt that
// can never be answered.
func expectInputEnded(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		_ = captureStdout(t, fn)
	}()
	select {
	case r := <-done:
		if r != errSetupInputEnded {
			t.Fatalf("%s: got %v, want errSetupInputEnded", name, r)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: still waiting for input after it ended", name)
	}
}

func TestSetup_RequiredPromptsStopAtEndOfInput(t *testing.T) {
	noAnthropicAutoDetect(t)
	cases := map[string]struct {
		input string
		fn    func(*bufio.Reader)
	}{
		"synthetic key, no input":       {"", func(r *bufio.Reader) { collectSyntheticKey(r) }},
		"synthetic key, empty lines":    {"\n\n", func(r *bufio.Reader) { collectSyntheticKey(r) }},
		"zai key":                       {"", func(r *bufio.Reader) { collectZaiConfig(r) }},
		"ollama key":                    {"", func(r *bufio.Reader) { collectOllamaKey(r) }},
		"zenmux key":                    {"", func(r *bufio.Reader) { collectZenmuxKey(r) }},
		"non-empty prompt":              {"", func(r *bufio.Reader) { promptNonEmpty(r, "Anthropic token") }},
		"anthropic token after refusal": {"Bearer abc\n", func(r *bufio.Reader) { collectAnthropicToken(r, slog.New(slog.NewTextHandler(io.Discard, nil))) }},
		"choice":                        {"99\n", func(r *bufio.Reader) { promptChoice(r, "Pick", []string{"a", "b"}) }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tc.input))
			expectInputEnded(t, name, func() { tc.fn(reader) })
		})
	}
}

func TestSetup_RequiredPromptsKeepLastLineWithoutNewline(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("value-without-newline"))
	var got string
	_ = captureStdout(t, func() { got = promptNonEmpty(reader, "Secret") })
	if got != "value-without-newline" {
		t.Fatalf("promptNonEmpty = %q", got)
	}
}

func TestRunSetup_EndOfInputReturnsAnError(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	t.Setenv("PATH", "")

	done := make(chan error, 1)
	withStdin(t, "1\n", func() { // Synthetic only, then the input ends
		go func() { done <- runSetup() }()
		select {
		case err := <-done:
			if !errors.Is(err, errSetupInputEnded) {
				t.Errorf("runSetup = %v, want errSetupInputEnded", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("runSetup still waiting for input after it ended")
		}
	})
	if _, err := os.Stat(filepath.Join(home, ".onwatch", ".env")); err == nil {
		t.Fatalf(".env written although setup never finished")
	}
}
