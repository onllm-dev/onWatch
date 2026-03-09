package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func performRequest(t *testing.T, handler http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestStandaloneServer_DefaultRoutesAndCounts(t *testing.T) {
	srv := newStandaloneServer("syn-key", "zai-key", "anth-token")

	syn := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, map[string]string{"Authorization": "Bearer syn-key"})
	if syn.Code != http.StatusOK {
		t.Fatalf("expected synthetic 200, got %d", syn.Code)
	}

	zai := performRequest(t, srv.mux, http.MethodGet, "/monitor/usage/quota/limit", nil, map[string]string{"Authorization": "zai-key"})
	if zai.Code != http.StatusOK {
		t.Fatalf("expected zai 200, got %d", zai.Code)
	}

	anth := performRequest(t, srv.mux, http.MethodGet, "/api/oauth/usage", nil, map[string]string{"Authorization": "Bearer anth-token"})
	if anth.Code != http.StatusOK {
		t.Fatalf("expected anthropic 200, got %d", anth.Code)
	}

	counts := performRequest(t, srv.mux, http.MethodGet, "/admin/requests", nil, nil)
	if counts.Code != http.StatusOK {
		t.Fatalf("expected counts 200, got %d", counts.Code)
	}

	var got map[string]int64
	if err := json.Unmarshal(counts.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal counts: %v", err)
	}
	if got["synthetic"] != 1 || got["zai"] != 1 || got["anthropic"] != 1 {
		t.Fatalf("unexpected counts: %+v", got)
	}
}

func TestStandaloneServer_AuthFailures(t *testing.T) {
	srv := newStandaloneServer("syn-key", "zai-key", "anth-token")

	syn := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, map[string]string{"Authorization": "Bearer wrong"})
	if syn.Code != http.StatusUnauthorized {
		t.Fatalf("expected synthetic 401, got %d", syn.Code)
	}

	zai := performRequest(t, srv.mux, http.MethodGet, "/monitor/usage/quota/limit", nil, map[string]string{"Authorization": "wrong"})
	if zai.Code != http.StatusOK {
		t.Fatalf("expected zai transport 200, got %d", zai.Code)
	}
	var zaiBody map[string]interface{}
	if err := json.Unmarshal(zai.Body.Bytes(), &zaiBody); err != nil {
		t.Fatalf("unmarshal zai auth error: %v", err)
	}
	if int(zaiBody["code"].(float64)) != 401 {
		t.Fatalf("expected zai body code 401, got %v", zaiBody["code"])
	}

	anth := performRequest(t, srv.mux, http.MethodGet, "/api/oauth/usage", nil, map[string]string{"Authorization": "Bearer wrong"})
	if anth.Code != http.StatusUnauthorized {
		t.Fatalf("expected anthropic 401, got %d", anth.Code)
	}
}

func TestStandaloneServer_AllowsRequestsWhenCredentialsAreEmpty(t *testing.T) {
	srv := newStandaloneServer("", "", "")

	syn := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, nil)
	if syn.Code != http.StatusOK {
		t.Fatalf("expected synthetic 200 without configured key, got %d", syn.Code)
	}

	zai := performRequest(t, srv.mux, http.MethodGet, "/monitor/usage/quota/limit", nil, nil)
	if zai.Code != http.StatusOK {
		t.Fatalf("expected zai 200 without configured key, got %d", zai.Code)
	}

	anth := performRequest(t, srv.mux, http.MethodGet, "/api/oauth/usage", nil, nil)
	if anth.Code != http.StatusOK {
		t.Fatalf("expected anthropic 200 without configured token, got %d", anth.Code)
	}
}

func TestStandaloneServer_AdminScenarioIsCaseInsensitiveAndResetsIndex(t *testing.T) {
	srv := newStandaloneServer("syn-key", "zai-key", "anth-token")

	payload := []byte(`{"provider":"SYNTHETIC","responses":["{\"step\":1}","{\"step\":2}"]}`)
	rr := performRequest(t, srv.mux, http.MethodPost, "/admin/scenario", bytes.NewReader(payload), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected scenario update 200, got %d", rr.Code)
	}

	first := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, map[string]string{"Authorization": "Bearer syn-key"})
	second := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, map[string]string{"Authorization": "Bearer syn-key"})
	third := performRequest(t, srv.mux, http.MethodGet, "/v2/quotas", nil, map[string]string{"Authorization": "Bearer syn-key"})

	if first.Body.String() != "{\"step\":1}" {
		t.Fatalf("expected first scenario response, got %s", first.Body.String())
	}
	if second.Body.String() != "{\"step\":2}" {
		t.Fatalf("expected second scenario response, got %s", second.Body.String())
	}
	if third.Body.String() != "{\"step\":1}" {
		t.Fatalf("expected index reset to cycle back, got %s", third.Body.String())
	}
}

func TestStandaloneServer_AdminErrorAndReset(t *testing.T) {
	srv := newStandaloneServer("syn-key", "zai-key", "anth-token")

	errPayload := []byte(`{"provider":"anthropic","status_code":429}`)
	errResp := performRequest(t, srv.mux, http.MethodPost, "/admin/error", bytes.NewReader(errPayload), nil)
	if errResp.Code != http.StatusOK {
		t.Fatalf("expected admin error 200, got %d", errResp.Code)
	}

	anth := performRequest(t, srv.mux, http.MethodGet, "/api/oauth/usage", nil, map[string]string{"Authorization": "Bearer anth-token"})
	if anth.Code != http.StatusTooManyRequests {
		t.Fatalf("expected anthropic 429, got %d", anth.Code)
	}

	reset := performRequest(t, srv.mux, http.MethodPost, "/admin/reset", nil, nil)
	if reset.Code != http.StatusOK {
		t.Fatalf("expected reset 200, got %d", reset.Code)
	}

	anth = performRequest(t, srv.mux, http.MethodGet, "/api/oauth/usage", nil, map[string]string{"Authorization": "Bearer anth-token"})
	if anth.Code != http.StatusOK {
		t.Fatalf("expected anthropic 200 after reset, got %d", anth.Code)
	}

	counts := performRequest(t, srv.mux, http.MethodGet, "/admin/requests", nil, nil)
	var got map[string]int64
	if err := json.Unmarshal(counts.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal counts after reset: %v", err)
	}
	if got["synthetic"] != 0 || got["zai"] != 0 || got["anthropic"] != 1 {
		t.Fatalf("unexpected counts after reset and one request: %+v", got)
	}
}

func TestStandaloneServer_AdminEndpointsValidateMethodBodyAndProvider(t *testing.T) {
	srv := newStandaloneServer("syn-key", "zai-key", "anth-token")

	if got := performRequest(t, srv.mux, http.MethodGet, "/admin/scenario", nil, nil); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected scenario GET 405, got %d", got.Code)
	}
	if got := performRequest(t, srv.mux, http.MethodGet, "/admin/error", nil, nil); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected error GET 405, got %d", got.Code)
	}
	if got := performRequest(t, srv.mux, http.MethodGet, "/admin/reset", nil, nil); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected reset GET 405, got %d", got.Code)
	}

	badJSON := performRequest(t, srv.mux, http.MethodPost, "/admin/scenario", bytes.NewBufferString("{"), nil)
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected bad scenario json 400, got %d", badJSON.Code)
	}

	unknownScenario := performRequest(t, srv.mux, http.MethodPost, "/admin/scenario", bytes.NewBufferString(`{"provider":"unknown","responses":[]}`), nil)
	if unknownScenario.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown scenario provider 400, got %d", unknownScenario.Code)
	}

	badErrorJSON := performRequest(t, srv.mux, http.MethodPost, "/admin/error", bytes.NewBufferString("{"), nil)
	if badErrorJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected bad error json 400, got %d", badErrorJSON.Code)
	}

	unknownError := performRequest(t, srv.mux, http.MethodPost, "/admin/error", bytes.NewBufferString(`{"provider":"unknown","status_code":500}`), nil)
	if unknownError.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown error provider 400, got %d", unknownError.Code)
	}
}

func TestMain_StartsAndShutsDownOnSignal(t *testing.T) {
	if os.Getenv("ONWATCH_MOCKSERVER_MAIN_HELPER") == "1" {
		os.Args = []string{"mockserver", "-port=0", "-syn-key=helper-syn", "-zai-key=helper-zai", "-anth-token=helper-anth"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMain_StartsAndShutsDownOnSignal")
	cmd.Env = append(os.Environ(), "ONWATCH_MOCKSERVER_MAIN_HELPER=1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal helper process: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait helper process: %v", err)
	}
}
