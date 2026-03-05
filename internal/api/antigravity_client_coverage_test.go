package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWithAntigravityTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityTimeout(5*time.Second))

	if client.httpClient.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", client.httpClient.Timeout)
	}
}

func TestParseUnixProcessLine_Valid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	// ps aux format: USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND...
	line := "user  12345  0.0  0.1  123456  7890  ??  S  10:00AM  0:01.00  /path/to/antigravity language-server --csrf_token=abc123 --extension_server_port=42100"
	info, err := client.parseUnixProcessLine(line)
	if err != nil {
		t.Fatalf("parseUnixProcessLine failed: %v", err)
	}
	if info.PID != 12345 {
		t.Errorf("PID = %d, want 12345", info.PID)
	}
	if info.CSRFToken != "abc123" {
		t.Errorf("CSRFToken = %q, want abc123", info.CSRFToken)
	}
	if info.ExtensionServerPort != 42100 {
		t.Errorf("ExtensionServerPort = %d, want 42100", info.ExtensionServerPort)
	}
}

func TestParseUnixProcessLine_TooFewFields(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	line := "user 12345 0.0 0.1"
	_, err := client.parseUnixProcessLine(line)
	if err == nil {
		t.Fatal("expected error for short line")
	}
}

func TestParseUnixProcessLine_InvalidPID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	line := "user  notapid  0.0  0.1  123456  7890  ??  S  10:00AM  0:01.00  /path/to/binary"
	_, err := client.parseUnixProcessLine(line)
	if err == nil {
		t.Fatal("expected error for invalid PID")
	}
}

func TestParseWMICOutput_ValidOutput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	output := `Node,CommandLine,ProcessId
HOST,antigravity language_server --csrf_token=token123 --extension_server_port=42100,5678
`
	info := client.parseWMICOutput(output)
	if info == nil {
		t.Fatal("parseWMICOutput returned nil")
	}
	if info.PID != 5678 {
		t.Errorf("PID = %d, want 5678", info.PID)
	}
	if info.CSRFToken != "token123" {
		t.Errorf("CSRFToken = %q, want token123", info.CSRFToken)
	}
	if info.ExtensionServerPort != 42100 {
		t.Errorf("ExtensionServerPort = %d, want 42100", info.ExtensionServerPort)
	}
}

func TestParseWMICOutput_NoMatches(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	output := `Node,CommandLine,ProcessId
HOST,notepad.exe,1234
`
	info := client.parseWMICOutput(output)
	if info != nil {
		t.Errorf("expected nil for non-matching output, got %v", info)
	}
}

func TestParseWMICOutput_EmptyOutput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	info := client.parseWMICOutput("")
	if info != nil {
		t.Errorf("expected nil for empty output, got %v", info)
	}
}

func TestParseWMICOutput_InvalidPID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	output := `Node,CommandLine,ProcessId
HOST,antigravity language_server,notanumber
`
	info := client.parseWMICOutput(output)
	if info != nil {
		t.Errorf("expected nil for invalid PID, got %v", info)
	}
}

func TestParseWMICOutput_BestCandidate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	// Two candidates: one with language_server and csrf, one without
	output := `Node,CommandLine,ProcessId
HOST,antigravity something,1111
HOST,antigravity language_server --csrf_token=best --extension_server_port=42100 --lsp,2222
`
	info := client.parseWMICOutput(output)
	if info == nil {
		t.Fatal("parseWMICOutput returned nil")
	}
	// Should pick the one with the higher score
	if info.PID != 2222 {
		t.Errorf("PID = %d, want 2222 (best candidate)", info.PID)
	}
}

func TestParsePortsFromSS(t *testing.T) {
	output := `State  Recv-Q  Send-Q  Local Address:Port  Peer Address:Port
LISTEN 0       128     *:42100                *:*    users:(("antigravity",pid=1234,fd=10))
LISTEN 0       128     *:42101                *:*    users:(("antigravity",pid=1234,fd=11))
LISTEN 0       128     *:8080                 *:*    users:(("nginx",pid=5678,fd=5))
`
	ports := parsePortsFromSS(output, 1234)
	if len(ports) != 2 {
		t.Errorf("expected 2 ports, got %d: %v", len(ports), ports)
	}
}

func TestParsePortsFromSS_NoMatch(t *testing.T) {
	output := `State  Recv-Q  Send-Q  Local Address:Port  Peer Address:Port
LISTEN 0       128     *:8080                 *:*    users:(("nginx",pid=5678,fd=5))
`
	ports := parsePortsFromSS(output, 1234)
	if len(ports) != 0 {
		t.Errorf("expected 0 ports for non-matching PID, got %d", len(ports))
	}
}

func TestParsePortsFromNetstat(t *testing.T) {
	output := `Proto Recv-Q Send-Q Local Address           Foreign Address         State       PID/Program name
tcp        0      0 0.0.0.0:42100           0.0.0.0:*               LISTEN      1234/antigravity
tcp        0      0 0.0.0.0:42101           0.0.0.0:*               LISTEN      1234/antigravity
tcp        0      0 0.0.0.0:80              0.0.0.0:*               LISTEN      5678/nginx
`
	ports := parsePortsFromNetstat(output, 1234)
	if len(ports) != 2 {
		t.Errorf("expected 2 ports, got %d: %v", len(ports), ports)
	}
}

func TestParsePortsFromNetstat_NoMatch(t *testing.T) {
	output := `Proto Recv-Q Send-Q Local Address           Foreign Address         State       PID/Program name
tcp        0      0 0.0.0.0:80              0.0.0.0:*               LISTEN      5678/nginx
`
	ports := parsePortsFromNetstat(output, 1234)
	if len(ports) != 0 {
		t.Errorf("expected 0 ports for non-matching PID, got %d", len(ports))
	}
}

func TestParsePortsFromLsof_Empty(t *testing.T) {
	ports := parsePortsFromLsof("")
	if len(ports) != 0 {
		t.Errorf("expected 0 ports for empty input, got %d", len(ports))
	}
}

func TestParsePortsFromWindowsNetstat_NoListening(t *testing.T) {
	output := `  TCP    0.0.0.0:42100         0.0.0.0:0              ESTABLISHED     1234
`
	ports := parsePortsFromWindowsNetstat(output, 1234)
	if len(ports) != 0 {
		t.Errorf("expected 0 ports for non-LISTENING entries, got %d", len(ports))
	}
}

func TestProbePort_Success200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the probe request
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/GetUnleashData") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Error("missing Connect-Protocol-Version header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	// Extract port from test server URL
	parts := strings.Split(server.URL, ":")
	port := 0
	for _, p := range parts {
		if n, err := parseInt(p); err == nil && n > 0 {
			port = n
		}
	}

	conn := client.probePort(context.Background(), port, "http", "test-csrf")
	if conn == nil {
		t.Fatal("probePort should return connection for 200 OK")
	}
	if conn.CSRFToken != "test-csrf" {
		t.Errorf("CSRFToken = %q, want test-csrf", conn.CSRFToken)
	}
	if conn.Protocol != "http" {
		t.Errorf("Protocol = %q, want http", conn.Protocol)
	}
}

func TestProbePort_Success401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	parts := strings.Split(server.URL, ":")
	port := 0
	for _, p := range parts {
		if n, err := parseInt(p); err == nil && n > 0 {
			port = n
		}
	}

	conn := client.probePort(context.Background(), port, "http", "")
	if conn == nil {
		t.Fatal("probePort should return connection for 401 (valid Connect API)")
	}
}

func TestProbePort_FailsOn404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	parts := strings.Split(server.URL, ":")
	port := 0
	for _, p := range parts {
		if n, err := parseInt(p); err == nil && n > 0 {
			port = n
		}
	}

	conn := client.probePort(context.Background(), port, "http", "")
	if conn != nil {
		t.Error("probePort should return nil for 404")
	}
}

func TestProbeForConnectAPI_FindsValidPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	parts := strings.Split(server.URL, ":")
	port := 0
	for _, p := range parts {
		if n, err := parseInt(p); err == nil && n > 0 {
			port = n
		}
	}

	conn, err := client.probeForConnectAPI(context.Background(), []int{port}, "csrf-token")
	if err != nil {
		t.Fatalf("probeForConnectAPI failed: %v", err)
	}
	if conn == nil {
		t.Fatal("expected connection, got nil")
	}
	if conn.CSRFToken != "csrf-token" {
		t.Errorf("CSRFToken = %q, want csrf-token", conn.CSRFToken)
	}
}

func TestProbeForConnectAPI_NoValidPorts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger)

	// Use a port that's not listening
	_, err := client.probeForConnectAPI(context.Background(), []int{1}, "csrf-token")
	if err == nil {
		t.Fatal("expected error when no valid ports")
	}
	if err != ErrAntigravityConnectionFailed {
		t.Errorf("expected ErrAntigravityConnectionFailed, got %v", err)
	}
}

func TestAntigravityClient_Detect_WithPreConfiguredConnection(t *testing.T) {
	conn := &AntigravityConnection{
		BaseURL:   "https://127.0.0.1:42100",
		CSRFToken: "pre-configured-token",
		Port:      42100,
		Protocol:  "https",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	result, err := client.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect with pre-configured connection failed: %v", err)
	}
	if result != conn {
		t.Error("Detect should return pre-configured connection")
	}
}

func TestAntigravityClient_FetchQuotas_EmptyBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write nothing
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := &AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if !strings.Contains(err.Error(), "empty response body") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAntigravityClient_FetchQuotas_InvalidJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := &AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestAntigravityClient_FetchQuotas_NilUserStatusNoMessage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := &AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Fatal("expected error for nil userStatus")
	}
	if err != ErrAntigravityNotAuthenticated {
		t.Errorf("expected ErrAntigravityNotAuthenticated, got %v", err)
	}
}

func TestAntigravityClient_FetchQuotas_ContextCancelled(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := &AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.FetchQuotas(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestAntigravityClient_FetchQuotas_ResetsConnectionOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	conn := &AntigravityConnection{
		BaseURL:  server.URL,
		Protocol: "http",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	client := NewAntigravityClient(logger, WithAntigravityConnection(conn))

	if !client.IsConnected() {
		t.Fatal("expected connected before fetch")
	}

	_, err := client.FetchQuotas(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}

	if client.IsConnected() {
		t.Error("expected connection to be reset after 500 error")
	}
}

// parseInt is a small helper to extract port from URL parts.
func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrAntigravityPortNotFound
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 0, ErrAntigravityPortNotFound
	}
	return n, nil
}

func TestScoreWindowsCandidate_AllSignals(t *testing.T) {
	info := &AntigravityProcessInfo{
		CommandLine:         "antigravity language_server --lsp --exa.language_server_pb",
		CSRFToken:           "token",
		ExtensionServerPort: 42100,
	}
	score := scoreWindowsCandidate(info)
	// antigravity (1) + lsp (5) + extension_server_port (10) + csrf (20) + language_server (50) = 86
	if score < 80 {
		t.Errorf("score = %d, expected >= 80 for full signal candidate", score)
	}
}

func TestScoreWindowsCandidate_NoSignals(t *testing.T) {
	info := &AntigravityProcessInfo{
		CommandLine: "notepad.exe",
	}
	score := scoreWindowsCandidate(info)
	if score != 0 {
		t.Errorf("score = %d, expected 0 for non-matching candidate", score)
	}
}
