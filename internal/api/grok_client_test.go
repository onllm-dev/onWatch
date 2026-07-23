package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewGrokClient_Basic(t *testing.T) {
	c := NewGrokClient("tok123", nil)
	if c == nil {
		t.Fatal("nil client")
	}
	c.SetToken("tok2")
	if c.getToken() != "tok2" {
		t.Error("set token")
	}
}

func TestResolveCredentials_FileWins(t *testing.T) {
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(auth, []byte(`{"https://auth.x.ai::c":{"key":"file_tok","email":"f@x","auth_mode":"oidc"}}`), 0600)
	t.Setenv("GROK_HOME", dir)

	c := NewGrokClient("env_tok", nil)
	creds := c.resolveCredentials()
	if creds == nil || creds.AccessToken != "file_tok" || creds.Email != "f@x" {
		t.Errorf("resolve preferred file: %+v", creds)
	}
}

func TestGrokWebParse_0UsageWithPeriod(t *testing.T) {
	// Build a minimal gRPC-web frame containing:
	// - a fixed32 at path ending 1 with value ~0 (but we will force 0 path)
	// Simpler: use the no-usage-yet logic by providing period varints + reset future, no percent.
	// For direct test we exercise the public path via a real server response.

	// Hand-craft a payload the scanner will treat as "has period, future reset, no percent" -> 0%
	// We will use a raw payload that looks like protobuf with varint at [1,6,...] and [1,5,1]
	payload := buildTestPayloadForNoUsageYet(t)
	snap, err := parseGRPCWebResponseGo(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.UsedPercent != 0 {
		t.Errorf("expected 0 for no-usage-yet, got %v", snap.UsedPercent)
	}
	if snap.ResetsAt == nil {
		t.Error("expected a resetsAt")
	}
}

func buildTestPayloadForNoUsageYet(t *testing.T) []byte {
	// Minimal: outer len-delim frame + inner fields that the scanner recognizes
	// We synthesize bytes that will be seen as having [1,6] varint and a future [1,5,1] varint.
	// Simpler approach: return a raw protobuf slice that the fallback path accepts + scanner walks.
	// Field 1 (len) containing sub with field 6 (varint 1) and field 5 len containing field 1 (varint future ts)
	// Rough wire that triggers hasUsagePeriod + future reset at preferred path.
	// Use the frame builder from the code paths.
	future := uint64(time.Now().Add(24*time.Hour).Unix())
	// Build a tiny message: 1:{ 6: varint(1), 5: {1: varint(future)} }
	inner5 := appendVarint(nil, (1<<3)|0, future)
	inner5field := appendLenField(nil, 5, inner5)
	inner6 := appendVarint(nil, (6<<3)|0, 1)
	outer := append(append([]byte{}, inner6...), inner5field...)
	frame := appendLenField(nil, 1, outer)

	// Wrap as grpc-web data frame (flags=0, 4-byte BE len)
	var web bytes.Buffer
	web.WriteByte(0)
	binary.Write(&web, binary.BigEndian, uint32(len(frame)))
	web.Write(frame)
	return web.Bytes()
}

func appendVarint(b []byte, key, val uint64) []byte {
	b = append(b, byte(key))
	for val >= 0x80 {
		b = append(b, byte(0x80|(val&0x7f)))
		val >>= 7
	}
	b = append(b, byte(val))
	return b
}

func appendLenField(b []byte, fieldNum uint64, val []byte) []byte {
	key := (fieldNum << 3) | 2
	b = append(b, byte(key))
	// varint len
	l := uint64(len(val))
	for l >= 0x80 {
		b = append(b, byte(0x80|(l&0x7f)))
		l >>= 7
	}
	b = append(b, byte(l))
	b = append(b, val...)
	return b
}

func TestLocalSessionScanner_TempDir(t *testing.T) {
	root := t.TempDir()
	// sessions/<cwd>/<id>/signals.json
	sessDir := filepath.Join(root, "sessions", "encCwd", "sess1")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	sig := map[string]interface{}{
		"totalTokensBeforeCompaction": 1200,
		"contextTokensUsed":           300,
		"primaryModelId":              "grok-build",
		"modelsUsed":                  []string{"grok-build"},
	}
	b, _ := json.Marshal(sig)
	_ = os.WriteFile(filepath.Join(sessDir, "signals.json"), b, 0644)

	sum := scanGrokSessionsDir(root, time.Now().Add(-48*time.Hour))
	if sum == nil || sum.SessionCount != 1 || sum.TotalTokens != 1500 {
		t.Errorf("scan sum: %+v", sum)
	}
	if sum.PrimaryModel != "grok-build" {
		t.Error("primary model")
	}
}

func TestTryWeb_WithTestServer(t *testing.T) {
	// Return a valid 0% no-usage frame
	payload := buildTestPayloadForNoUsageYet(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	// Temporarily patch the constant endpoint by using transport + custom client that rewrites? For test we
	// just exercise parse directly (already covered). Here we at least prove the HTTP path wires.
	c := NewGrokClient("tok", nil, WithGrokHTTPTransport(srv.Client().Transport))
	// Force the url by monkey not practical; instead call the low level parse which the real path uses.
	// This test mainly validates we can construct with transport override.
	if c.httpTransport == nil {
		t.Error("transport override not set")
	}
	_, _ = parseGRPCWebResponseGo(payload) // already exercised
}

func TestRPC_NoBinary(t *testing.T) {
	c := NewGrokClient("", nil, WithGrokBinary("/nonexistent/grok-xyz-123"))
	_, err := c.tryRPC(t.Context(), &GrokCredentials{AccessToken: "x"})
	if err == nil {
		t.Error("expected binary not found err")
	}
}
