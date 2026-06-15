package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrGrokUnauthorized    = errors.New("grok: unauthorized")
	ErrGrokForbidden       = errors.New("grok: forbidden")
	ErrGrokServerError     = errors.New("grok: server error")
	ErrGrokNetworkError    = errors.New("grok: network error")
	ErrGrokInvalidResponse = errors.New("grok: invalid response")
	ErrGrokBinaryNotFound  = errors.New("grok: binary not found")
	ErrGrokRPCTimeout      = errors.New("grok: rpc timeout")
	ErrGrokParseFailed     = errors.New("grok: parse failed")
)

// GrokClient handles fetching Grok credits usage via local auth + optional
// `grok agent stdio` RPC and/or grok.com gRPC-web bearer probe.
// No browser cookie import (portable daemon); bearer from auth.json is sufficient for web path.
type GrokClient struct {
	httpClient *http.Client
	logger     *slog.Logger

	token   string
	tokenMu sync.RWMutex

	// Test hooks
	rpcBinaryOverride string
	httpTransport     http.RoundTripper // for httptest
}

// GrokClientOption configures the client (primarily for tests).
type GrokClientOption func(*GrokClient)

// WithGrokBinary overrides the executable name/path for the RPC path (testing).
func WithGrokBinary(bin string) GrokClientOption {
	return func(c *GrokClient) { c.rpcBinaryOverride = bin }
}

// WithGrokHTTPTransport overrides the RoundTripper (for httptest in web path).
func WithGrokHTTPTransport(rt http.RoundTripper) GrokClientOption {
	return func(c *GrokClient) { c.httpTransport = rt }
}

// NewGrokClient creates a Grok client. If token == "", it will still attempt
// to load fresh credentials from the auth file on each fetch (recommended for
// local grok login flows).
func NewGrokClient(token string, logger *slog.Logger, opts ...GrokClientOption) *GrokClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &GrokClient{
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				ResponseHeaderTimeout: 15 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		logger: logger,
		token:  strings.TrimSpace(token),
	}
	for _, o := range opts {
		o(c)
	}
	if c.httpTransport != nil {
		c.httpClient.Transport = c.httpTransport
	}
	return c
}

func (c *GrokClient) getToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *GrokClient) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = strings.TrimSpace(token)
}

// resolveCredentials prefers an explicit token (for Docker/manual) but always
// consults the on-disk auth.json for the freshest identity fields and a possible
// updated bearer. File is authoritative per the spec.
func (c *GrokClient) resolveCredentials() *GrokCredentials {
	// Always try file first for complete identity + current key.
	if creds := DetectGrokCredentials(c.logger); creds != nil && creds.AccessToken != "" {
		return creds
	}
	// Fall back to any token passed at construction (or via SetToken) for bearer probe.
	if tok := c.getToken(); tok != "" {
		return &GrokCredentials{AccessToken: tok}
	}
	return nil
}

// FetchSnapshot attempts RPC then web fallback and returns a normalized snapshot
// (with identity from creds). Local sessions are attached when available.
// Never returns nil, nil on auth failure; returns a best-effort identity snapshot or error.
func (c *GrokClient) FetchSnapshot(ctx context.Context) (*GrokSnapshot, error) {
	creds := c.resolveCredentials()
	if creds == nil || creds.AccessToken == "" {
		return nil, ErrGrokUnauthorized
	}

	var billing *GrokBillingResponse
	var web *GrokWebBillingSnapshot

	// 1. Try RPC (best effort; often "method not found" today)
	if b, err := c.tryRPC(ctx, creds); err == nil && b != nil {
		billing = b
	} else if err != nil {
		c.logger.Debug("grok rpc attempt failed or not available", "error", err)
	}

	// 2. Web gRPC-web bearer probe (primary reliable path for daemon)
	if w, err := c.tryWeb(ctx, creds); err == nil && w != nil {
		web = w
	} else if err != nil {
		c.logger.Debug("grok web billing probe failed", "error", err)
	}

	// 3. Build snapshot
	var snap *GrokSnapshot
	if billing != nil {
		snap = billing.ToSnapshot(time.Now().UTC(), creds.Email, creds.TeamID, creds.LoginMethod(), web)
	} else if web != nil {
		snap = FromWebBilling(web, time.Now().UTC(), creds.Email, creds.TeamID, creds.LoginMethod())
	} else {
		// Identity only (no usage data this cycle)
		snap = &GrokSnapshot{
			CapturedAt:  time.Now().UTC(),
			AccountID:   1,
			Email:       creds.Email,
			TeamID:      creds.TeamID,
			LoginMethod: creds.LoginMethod(),
			RawJSON:     "{}",
		}
	}

	// Attach informational local sessions (never fails the fetch)
	if ls := c.scanLocalSessions(); ls != nil && (ls.SessionCount > 0 || ls.TotalTokens > 0) {
		snap.LocalSessions = ls
	}

	if snap.RawJSON == "" {
		snap.RawJSON = "{}"
	}
	return snap, nil
}

// --- RPC via `grok agent stdio` (ACP JSON-RPC) ---

func (c *GrokClient) tryRPC(ctx context.Context, creds *GrokCredentials) (*GrokBillingResponse, error) {
	// We do not actually need the creds for the stdio surface (it uses its own login state),
	// but we keep the signature for symmetry and future bearer injection if required.
	_ = creds

	bin := c.rpcBinaryOverride
	if bin == "" {
		bin = "grok"
	}

	// Resolve via PATH (user may have custom install). Use /usr/bin/env for PATH hygiene like CodexBar.
	resolved, err := exec.LookPath(bin)
	if err != nil {
		// Try common install locations quickly
		for _, cand := range []string{
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "grok"),
			"/usr/local/bin/grok",
			"/opt/homebrew/bin/grok",
		} {
			if _, statErr := os.Stat(cand); statErr == nil {
				resolved = cand
				break
			}
		}
	}
	if resolved == "" {
		return nil, ErrGrokBinaryNotFound
	}

	cmd := exec.CommandContext(ctx, resolved, "agent", "stdio")
	// Inherit a clean PATH; do not leak unrelated env that might prompt keychain etc.
	cmd.Env = append([]string{}, os.Environ()...)
	// Ensure we can find it if grok is in a non-standard place already resolved above.
	// (env already has PATH)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("grok rpc stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("grok rpc stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("grok rpc stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("grok rpc start: %w", err)
	}
	defer func() {
		// Best effort cleanup
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Drain stderr (non-blocking log)
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			c.logger.Debug("grok rpc stderr", "line", s.Text())
		}
	}()

	// initialize (per spec + Swift)
	initParams := map[string]interface{}{
		"protocolVersion": "1",
		"clientCapabilities": map[string]interface{}{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}
	if err := c.rpcSend(stdin, 1, "initialize", initParams); err != nil {
		_ = stdin.Close()
		return nil, err
	}

	// Read init response (ignore content, just ack)
	if _, err := c.rpcRead(stdout, 1, 8*time.Second); err != nil {
		_ = stdin.Close()
		return nil, err
	}

	// billing request
	if err := c.rpcSend(stdin, 2, "x.ai/billing", map[string]interface{}{}); err != nil {
		_ = stdin.Close()
		return nil, err
	}

	msg, err := c.rpcRead(stdout, 2, 12*time.Second)
	if err != nil {
		return nil, err
	}
	if errMsg, ok := msg["error"].(map[string]interface{}); ok {
		if m, _ := errMsg["message"].(string); m != "" {
			if strings.Contains(strings.ToLower(m), "authentication") || strings.Contains(strings.ToLower(m), "grok login") {
				return nil, ErrGrokUnauthorized
			}
			return nil, fmt.Errorf("%w: %s", ErrGrokInvalidResponse, m)
		}
	}
	result, ok := msg["result"]
	if !ok {
		return nil, ErrGrokInvalidResponse
	}
	// Re-marshal result to struct
	resBytes, _ := json.Marshal(result)
	var billing GrokBillingResponse
	if err := json.Unmarshal(resBytes, &billing); err != nil {
		return nil, fmt.Errorf("grok rpc decode: %w", err)
	}
	return &billing, nil
}

func (c *GrokClient) rpcSend(stdin io.Writer, id int, method string, params interface{}) error {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// Critical: Grok ACP does not unescape "\/" in method names. Re-encode without escapes.
	s := strings.ReplaceAll(string(raw), `\/`, `/`)
	data := []byte(s)
	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return err
	}
	c.logger.Debug("grok rpc ->", "preview", string(data[:minInt(len(data), 180)]))
	return nil
}

func (c *GrokClient) rpcRead(stdout io.Reader, wantID int, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)
	scanner := bufio.NewScanner(stdout)
	for time.Now().Before(deadline) {
		// Non-blocking-ish read with small deadline on the scanner isn't direct;
		// use a simple loop + overall timeout via select on a done chan isn't trivial with bufio.
		// Instead we rely on the cmd ctx + explicit kill in caller for the two calls.
		// For the read loop we use a short internal scan timeout.
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, err
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		c.logger.Debug("grok rpc <-", "preview", string(line[:minInt(len(line), 200)]))
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		// Skip notifications (no id) and unrelated replies
		if msg["id"] == nil {
			continue
		}
		if idf, ok := msg["id"].(float64); ok && int(idf) == wantID {
			return msg, nil
		}
	}
	return nil, ErrGrokRPCTimeout
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Web gRPC-web probe (ported scanner logic from CodexBar GrokWebBillingFetcher.swift) ---

func (c *GrokClient) tryWeb(ctx context.Context, creds *GrokCredentials) (*GrokWebBillingSnapshot, error) {
	if creds == nil || creds.AccessToken == "" || creds.IsExpired() {
		return nil, ErrGrokUnauthorized
	}

	endpoint := "https://grok.com/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x00, 0x00}))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("Origin", "https://grok.com")
	req.Header.Set("Referer", "https://grok.com/?_s=usage")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	req.Header.Set("x-user-agent", "connect-es/2.1.1")
	req.Header.Set("User-Agent", "onwatch/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGrokNetworkError, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("%w: http %d %s", ErrGrokServerError, resp.StatusCode, string(body))
	}

	// Read full for frame/trailer parsing (small payload)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Validate grpc status trailers/headers
	if err := validateGRPCWebTrailersGo(body); err != nil {
		return nil, err
	}

	return parseGRPCWebResponseGo(body)
}

// --- Exact port of the Swift gRPC-web + protobuf heuristic scanner ---

func validateGRPCWebTrailersGo(data []byte) error {
	fields := grpcWebTrailerFieldsGo(data)
	if raw := fields["grpc-status"]; raw != "" {
		if st, err := strconv.Atoi(raw); err == nil && st != 0 {
			return fmt.Errorf("%w: grpc-status=%d %s", ErrGrokInvalidResponse, st, fields["grpc-message"])
		}
	}
	return nil
}

func grpcWebTrailerFieldsGo(data []byte) map[string]string {
	fields := map[string]string{}
	b := data
	for len(b) >= 5 {
		flags := b[0]
		length := int(b[1])<<24 | int(b[2])<<16 | int(b[3])<<8 | int(b[4])
		if length < 0 || 5+length > len(b) {
			break
		}
		if flags&0x80 != 0 {
			text := string(b[5 : 5+length])
			for _, line := range strings.Split(text, "\n") {
				if line == "" {
					continue
				}
				if i := strings.Index(line, ":"); i > 0 {
					k := strings.ToLower(strings.TrimSpace(line[:i]))
					v := strings.TrimSpace(line[i+1:])
					fields[k] = v
				}
			}
		}
		b = b[5+length:]
	}
	return fields
}

func parseGRPCWebResponseGo(data []byte) (*GrokWebBillingSnapshot, error) {
	payloads := grpcWebDataFramesGo(data)
	if len(payloads) == 0 && looksLikeProtobufPayloadGo(data) {
		payloads = [][]byte{data}
	}
	if len(payloads) == 0 {
		return nil, ErrGrokParseFailed
	}

	scan := ProtobufScanGo{}
	for _, p := range payloads {
		sub, _ := scanProtobufGo(p, 0, nil, 0)
		scan.merge(sub)
	}

	now := time.Now()

	// Percent: fixed32 at path ending with field 1, 0-100
	var best *struct {
		val   float64
		depth int
	}
	for _, f := range scan.fixed32 {
		if len(f.path) == 0 {
			continue
		}
		if f.path[len(f.path)-1] != 1 {
			continue
		}
		if f.val < 0 || f.val > 100 {
			continue
		}
		depth := len(f.path)
		if best == nil || depth < best.depth {
			v := f.val
			best = &struct {
				val   float64
				depth int
			}{val: v, depth: depth}
		}
	}
	var parsedPercent *float64
	if best != nil {
		parsedPercent = &best.val
	}

	// Reset times from plausible unix seconds varints
	type ts struct {
		path []uint64
		t    time.Time
	}
	var resets []ts
	for _, v := range scan.varints {
		if v.val < 1_700_000_000 || v.val > 2_100_000_000 {
			continue
		}
		t := time.Unix(int64(v.val), 0).UTC()
		if t.After(now) {
			resets = append(resets, ts{path: v.path, t: t})
		}
	}
	var chosen *time.Time
	// Prefer exact [1,5,1]
	for _, r := range resets {
		if len(r.path) == 3 && r.path[0] == 1 && r.path[1] == 5 && r.path[2] == 1 {
			chosen = &r.t
			break
		}
	}
	if chosen == nil && len(resets) > 0 {
		// earliest future
		minT := resets[0].t
		for _, r := range resets[1:] {
			if r.t.Before(minT) {
				minT = r.t
			}
		}
		chosen = &minT
	}

	hasUsagePeriod := false
	for _, v := range scan.varints {
		if len(v.path) >= 2 && v.path[0] == 1 && v.path[1] == 6 {
			hasUsagePeriod = true
			break
		}
		if len(v.path) == 3 && v.path[0] == 1 && v.path[1] == 8 && v.path[2] == 1 {
			if v.val == 1 || v.val == 2 {
				hasUsagePeriod = true
				break
			}
		}
	}

	noUsageYet := parsedPercent == nil && len(scan.fixed32) == 0 && chosen != nil && hasUsagePeriod
	if parsedPercent == nil {
		if noUsageYet {
			z := 0.0
			parsedPercent = &z
		} else {
			return nil, ErrGrokParseFailed
		}
	}
	return &GrokWebBillingSnapshot{UsedPercent: *parsedPercent, ResetsAt: chosen}, nil
}

func looksLikeProtobufPayloadGo(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	first := data[0]
	field := first >> 3
	wire := first & 0x07
	return field > 0 && (wire == 0 || wire == 1 || wire == 2 || wire == 5)
}

func grpcWebDataFramesGo(data []byte) [][]byte {
	var frames [][]byte
	b := data
	for len(b) >= 5 {
		flags := b[0]
		length := int(b[1])<<24 | int(b[2])<<16 | int(b[3])<<8 | int(b[4])
		if length < 0 || 5+length > len(b) {
			return nil
		}
		if flags&0x80 == 0 {
			frames = append(frames, b[5:5+length])
		}
		b = b[5+length:]
	}
	return frames
}

type ProtobufScanGo struct {
	fixed32 []struct {
		path  []uint64
		val   float64
		order int
	}
	varints []struct {
		path  []uint64
		val   uint64
	}
}

func (s *ProtobufScanGo) merge(o ProtobufScanGo) {
	s.fixed32 = append(s.fixed32, o.fixed32...)
	s.varints = append(s.varints, o.varints...)
}

func scanProtobufGo(data []byte, depth int, path []uint64, order int) (ProtobufScanGo, int) {
	scan := ProtobufScanGo{}
	b := data
	idx := 0
	nextOrder := order

	for idx < len(b) {
		start := idx
		key, ok := readVarintGo(b, &idx)
		if !ok || key == 0 {
			idx = start + 1
			continue
		}
		fieldNum := key >> 3
		wire := key & 0x07
		fpath := append(append([]uint64{}, path...), fieldNum)

		switch wire {
		case 0:
			if v, ok := readVarintGo(b, &idx); ok {
				scan.varints = append(scan.varints, struct {
					path  []uint64
					val   uint64
				}{fpath, v})
			} else {
				idx = start + 1
			}
		case 1:
			if idx+8 <= len(b) {
				idx += 8
			} else {
				idx = start + 1
			}
		case 2:
			l, ok := readVarintGo(b, &idx)
			if !ok || int(l) > len(b)-idx {
				idx = start + 1
				continue
			}
			sub := b[idx : idx+int(l)]
			if depth < 4 {
				nested, no := scanProtobufGo(sub, depth+1, fpath, nextOrder)
				scan.merge(nested)
				nextOrder = no
			}
			idx += int(l)
		case 5:
			if idx+4 <= len(b) {
				bits := uint32(b[idx]) | uint32(b[idx+1])<<8 | uint32(b[idx+2])<<16 | uint32(b[idx+3])<<24
				f32 := math.Float32frombits(bits)
				scan.fixed32 = append(scan.fixed32, struct {
					path  []uint64
					val   float64
					order int
				}{fpath, float64(f32), nextOrder})
				nextOrder++
				idx += 4
			} else {
				idx = start + 1
			}
		default:
			idx = start + 1
		}
	}
	return scan, nextOrder
}

func readVarintGo(b []byte, idx *int) (uint64, bool) {
	var v uint64
	shift := uint(0)
	for *idx < len(b) && shift < 64 {
		x := b[*idx]
		*idx++
		v |= uint64(x&0x7F) << shift
		if x&0x80 == 0 {
			return v, true
		}
		shift += 7
	}
	return 0, false
}

// --- Local sessions scanner (informational) ---

func (c *GrokClient) scanLocalSessions() *GrokLocalSessionSummary {
	home := GrokHomeDir()
	if home == "" {
		return nil
	}
	root := filepath.Join(home, "sessions")
	return scanGrokSessionsDir(root, time.Now().AddDate(0, 0, -30))
}

func GrokHomeDir() string {
	if h := strings.TrimSpace(os.Getenv("GROK_HOME")); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".grok")
}

func scanGrokSessionsDir(root string, cutoff time.Time) *GrokLocalSessionSummary {
	sum := &GrokLocalSessionSummary{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "signals.json" {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var j map[string]interface{}
		if json.Unmarshal(data, &j) != nil {
			return nil
		}
		sum.SessionCount++
		before := 0
		if v, ok := j["totalTokensBeforeCompaction"].(float64); ok {
			before = int(v)
		}
		ctxUsed := 0
		if v, ok := j["contextTokensUsed"].(float64); ok {
			ctxUsed = int(v)
		}
		sum.TotalTokens += before + ctxUsed

		if sum.LastSessionAt == nil || info.ModTime().After(*sum.LastSessionAt) {
			t := info.ModTime()
			sum.LastSessionAt = &t
		}
		if pm, ok := j["primaryModelId"].(string); ok && pm != "" {
			sum.PrimaryModel = pm
		}
		if models, ok := j["modelsUsed"].([]interface{}); ok {
			for _, m := range models {
				if s, ok := m.(string); ok && s != "" {
					sum.Models = append(sum.Models, s)
				}
			}
		}
		return nil
	})
	return sum
}
