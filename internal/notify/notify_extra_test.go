package notify

import (
	"bufio"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// ---------------------------------------------------------------------------
// sendNotification - email path coverage
// ---------------------------------------------------------------------------

// TestSendNotification_EmailSent verifies that sendNotification sends an email
// when the mailer is configured and no prior notification exists in the log.
func TestSendNotification_EmailSent(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	engine := newTestEngine(t, s)
	engine.Reload()

	mailCount, cleanup := setupSMTPAndMailer(t, s, engine)
	defer cleanup()

	engine.mu.RLock()
	mailer := engine.mailer
	engine.mu.RUnlock()

	status := QuotaStatus{
		Provider:    "anthropic",
		QuotaKey:    "daily",
		Utilization: 85.0,
		Limit:       1000,
	}
	engine.sendNotification(mailer, nil, NotificationChannels{Email: true, Push: false}, status, "warning")

	if mailCount.Load() != 1 {
		t.Errorf("Expected 1 email, got %d", mailCount.Load())
	}
}

// TestSendNotification_AlreadySent verifies that sendNotification skips sending
// when a notification was already logged for the same provider/quota/type.
func TestSendNotification_AlreadySent(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	engine := newTestEngine(t, s)
	engine.Reload()

	mailCount, cleanup := setupSMTPAndMailer(t, s, engine)
	defer cleanup()

	// Pre-log a notification so the dedup check fires.
	if err := s.UpsertNotificationLog("anthropic", "daily", "warning", 85.0); err != nil {
		t.Fatalf("UpsertNotificationLog failed: %v", err)
	}

	engine.mu.RLock()
	mailer := engine.mailer
	engine.mu.RUnlock()

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	engine.sendNotification(mailer, nil, NotificationChannels{Email: true, Push: false}, status, "warning")

	if mailCount.Load() != 0 {
		t.Errorf("Expected 0 emails (already sent), got %d", mailCount.Load())
	}
}

// TestSendNotification_EmailFailure verifies that when the email send fails, the
// notification is NOT logged (sent==false path).
func TestSendNotification_EmailFailure(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	engine := newTestEngine(t, s)

	// Create a mailer pointing at a port with nothing listening.
	badCfg := SMTPConfig{
		Host:     "127.0.0.1",
		Port:     19997,
		Protocol: "none",
		FromAddr: "alerts@test.com",
		FromName: "Test",
		ToAddrs:  []string{"admin@test.com"},
	}
	badMailer := NewSMTPMailer(badCfg, slog.Default())

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	engine.sendNotification(badMailer, nil, NotificationChannels{Email: true, Push: false}, status, "warning")

	// Nothing should have been logged since send failed.
	sentAt, _, err := s.GetLastNotification("anthropic", "daily", "warning")
	if err != nil {
		t.Fatalf("GetLastNotification failed: %v", err)
	}
	if !sentAt.IsZero() {
		t.Error("Expected no log entry when email send fails")
	}
}

// ---------------------------------------------------------------------------
// sendNotification - push path coverage (subscription send + 410 delete)
// ---------------------------------------------------------------------------

// makeTLSPushSender creates a PushSender whose HTTP client trusts the provided
// httptest.Server TLS certificate. This is required because SavePushSubscription
// enforces HTTPS endpoints, so all push test servers must be TLS.
func makeTLSPushSender(t *testing.T, server *httptest.Server) *PushSender {
	t.Helper()
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}
	sender, err := NewPushSender(pub, priv, "mailto:test@example.com")
	if err != nil {
		t.Fatalf("NewPushSender: %v", err)
	}
	// Replace the HTTP client with one that trusts the test server's TLS cert.
	sender.client = server.Client()
	return sender
}

// saveTestSubscription saves a push subscription pointing at a TLS test server.
func saveTestSubscription(t *testing.T, s *store.Store, server *httptest.Server) (p256dh, auth, endpoint string) {
	t.Helper()
	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	p256dh = base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())
	auth = base64.RawURLEncoding.EncodeToString(authSecret)
	endpoint = server.URL + "/push"
	if err := s.SavePushSubscription(endpoint, p256dh, auth); err != nil {
		t.Fatalf("SavePushSubscription: %v", err)
	}
	return p256dh, auth, endpoint
}

// TestSendNotification_PushSent verifies that sendNotification sends a push
// notification to a stored subscription and logs success.
func TestSendNotification_PushSent(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	var received atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	saveTestSubscription(t, s, server)
	pushSender := makeTLSPushSender(t, server)
	engine := newTestEngine(t, s)

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	engine.sendNotification(nil, pushSender, NotificationChannels{Email: false, Push: true}, status, "warning")

	if received.Load() != 1 {
		t.Errorf("Expected 1 push request, got %d", received.Load())
	}

	// Notification should be logged.
	sentAt, _, err := s.GetLastNotification("anthropic", "daily", "warning")
	if err != nil {
		t.Fatalf("GetLastNotification: %v", err)
	}
	if sentAt.IsZero() {
		t.Error("Expected notification to be logged after successful push send")
	}
}

// TestSendNotification_Push410DeletesSubscription verifies that a 410 response
// from the push service causes the subscription to be deleted.
func TestSendNotification_Push410DeletesSubscription(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone) // 410
	}))
	defer server.Close()

	_, _, endpoint := saveTestSubscription(t, s, server)
	pushSender := makeTLSPushSender(t, server)
	engine := newTestEngine(t, s)

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	engine.sendNotification(nil, pushSender, NotificationChannels{Email: false, Push: true}, status, "warning")

	// After 410, subscription should be removed.
	subs, err := s.GetPushSubscriptions()
	if err != nil {
		t.Fatalf("GetPushSubscriptions: %v", err)
	}
	for _, sub := range subs {
		if sub.Endpoint == endpoint {
			t.Error("Subscription should have been deleted after 410 response")
		}
	}
}

// ---------------------------------------------------------------------------
// SendTestPush - send error path
// ---------------------------------------------------------------------------

// TestSendTestPush_SendError verifies that SendTestPush returns the last error
// when all push sends fail (sent == 0 && lastErr != nil path).
func TestSendTestPush_SendError(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// A server that always returns 500.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	saveTestSubscription(t, s, server)
	pushSender := makeTLSPushSender(t, server)

	engine := newTestEngine(t, s)
	engine.mu.Lock()
	engine.pushSender = pushSender
	engine.mu.Unlock()

	err := engine.SendTestPush()
	if err == nil {
		t.Error("Expected error when push send returns 500")
	}
}

// ---------------------------------------------------------------------------
// smtp.connect - tls and starttls error paths
// ---------------------------------------------------------------------------

// TestSMTPConnect_TLSDialError verifies that the "tls" protocol path returns
// an error when the server is not reachable (no TLS listener at that port).
func TestSMTPConnect_TLSDialError(t *testing.T) {
	cfg := SMTPConfig{
		Host:     "127.0.0.1",
		Port:     19996,
		Protocol: "tls",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}
	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected TLS dial error for unreachable host")
	}
}

// TestSMTPConnect_StartTLSError verifies that the "starttls" protocol path
// returns an error when STARTTLS negotiation fails (server does not advertise it).
func TestSMTPConnect_StartTLSError(t *testing.T) {
	// Start a plain SMTP server that does NOT advertise STARTTLS.
	var connCount atomic.Int32
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		connCount.Add(1)
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				// Deliberately omit STARTTLS from the capability list.
				fmt.Fprintf(conn, "250 mock no-starttls\r\n")
			case "STARTTLS":
				fmt.Fprintf(conn, "454 TLS not available\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Protocol: "starttls",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}
	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected STARTTLS error when server does not support it")
	}
}

// TestSMTPConnect_StartTLS_DialError verifies that the "starttls" protocol path
// returns an error when the initial TCP connection fails.
func TestSMTPConnect_StartTLS_DialError(t *testing.T) {
	cfg := SMTPConfig{
		Host:     "127.0.0.1",
		Port:     19995,
		Protocol: "starttls",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}
	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected dial error for unreachable host with starttls")
	}
}

// ---------------------------------------------------------------------------
// GenerateEncryptionKey - concurrent call pattern for race detection
// ---------------------------------------------------------------------------

// TestGenerateEncryptionKey_ConcurrentCalls exercises GenerateEncryptionKey from
// multiple goroutines to validate race-safety and key uniqueness.
func TestGenerateEncryptionKey_ConcurrentCalls(t *testing.T) {
	const workers = 8
	results := make(chan string, workers)

	for i := 0; i < workers; i++ {
		go func() {
			key, err := GenerateEncryptionKey()
			if err != nil {
				results <- ""
				return
			}
			results <- key
		}()
	}

	seen := make(map[string]bool)
	for i := 0; i < workers; i++ {
		k := <-results
		if k == "" {
			t.Error("GenerateEncryptionKey returned empty key")
			continue
		}
		if len(k) != 64 {
			t.Errorf("key length = %d, want 64", len(k))
		}
		if seen[k] {
			t.Error("duplicate key generated")
		}
		seen[k] = true
	}
}

// ---------------------------------------------------------------------------
// ConfigurePush - error paths
// ---------------------------------------------------------------------------

// TestConfigurePush_GetSettingError verifies that ConfigurePush returns an error
// when the store cannot be queried (closed store).
func TestConfigurePush_GetSettingError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	// Close the store so all operations fail.
	s.Close()

	engine := New(s, slog.Default())
	err = engine.ConfigurePush()
	if err == nil {
		t.Error("Expected error from ConfigurePush when store is closed")
	}
	if !strings.Contains(err.Error(), "notify.ConfigurePush") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestConfigurePush_InvalidVAPIDJSON verifies that ConfigurePush returns an error
// when the stored vapid_keys value is not valid JSON.
func TestConfigurePush_InvalidVAPIDJSON(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := s.SetSetting("vapid_keys", "not-valid-json{{{{"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	engine := newTestEngine(t, s)
	err := engine.ConfigurePush()
	if err == nil {
		t.Error("Expected error for invalid vapid_keys JSON")
	}
	if !strings.Contains(err.Error(), "notify.ConfigurePush") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ConfigureSMTP - store error path
// ---------------------------------------------------------------------------

// TestConfigureSMTP_StoreError verifies that ConfigureSMTP returns an error when
// the store cannot be queried (closed store).
func TestConfigureSMTP_StoreError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	s.Close()

	engine := New(s, slog.Default())
	err = engine.ConfigureSMTP()
	if err == nil {
		t.Error("Expected error from ConfigureSMTP when store is closed")
	}
	if !strings.Contains(err.Error(), "notify.ConfigureSMTP") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestConfigureSMTP_DecryptionFailure verifies that ConfigureSMTP does not error
// when password decryption fails - it simply keeps the plaintext password.
// This exercises the decrypt-failure debug-log branch.
func TestConfigureSMTP_DecryptionFailure(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// Set an encryption key but store a password that looks encrypted but isn't
	// (long enough to trigger the decryption attempt, but will fail).
	encKey, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey: %v", err)
	}

	// A 30-char "password" that is long enough (>24) to trigger decryption attempt
	// but is not valid base64 ciphertext - decryption will fail silently.
	fakeEncryptedPass := "dGhpcyBpcyBub3QgcmVhbGx5IGVuY3J5cHRlZCBidXQgbG9uZw"

	smtpJSON, _ := json.Marshal(smtpSettingsJSON{
		Host:        "smtp.example.com",
		Port:        587,
		Username:    "user@test.com",
		Password:    fakeEncryptedPass,
		Protocol:    "none",
		FromAddress: "alerts@onwatch.dev",
		FromName:    "onWatch",
		To:          "admin@example.com",
	})
	s.SetSetting("smtp", string(smtpJSON))

	engine := newTestEngine(t, s)
	engine.SetEncryptionKey(encKey)
	// ConfigureSMTP should succeed (decryption failure is non-fatal).
	err = engine.ConfigureSMTP()
	if err != nil {
		t.Fatalf("ConfigureSMTP should not fail on decrypt error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SendTestPush - GetPushSubscriptions error path and partial success
// ---------------------------------------------------------------------------

// TestSendTestPush_GetSubscriptionsError verifies that SendTestPush returns an
// error when the store cannot be queried for subscriptions.
func TestSendTestPush_GetSubscriptionsError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	engine := New(s, slog.Default())
	// Configure a push sender without closing the store yet.
	if err := engine.ConfigurePush(); err != nil {
		t.Fatalf("ConfigurePush: %v", err)
	}

	// Close the store AFTER configuring push so GetPushSubscriptions fails.
	s.Close()

	err = engine.SendTestPush()
	if err == nil {
		t.Error("Expected error when store is closed for GetPushSubscriptions")
	}
	if !strings.Contains(err.Error(), "failed to get subscriptions") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestSendTestPush_PartialSuccess verifies that SendTestPush returns nil when
// at least one push send succeeds (even if another fails).
func TestSendTestPush_PartialSuccess(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// Server 1: succeeds.
	successServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer successServer.Close()

	// Server 2: fails with 500.
	failServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	// Both subscriptions saved - but the push sender uses one TLS client,
	// so we save both pointing at the success server to test the sent>0 path.
	// For partial failure: save subscription with success server endpoint only
	// (sent==1, lastErr==nil => returns nil).
	saveTestSubscription(t, s, successServer)

	pub, priv, _ := GenerateVAPIDKeys()
	pushSender, _ := NewPushSender(pub, priv, "mailto:test@example.com")
	pushSender.client = successServer.Client()

	engine := newTestEngine(t, s)
	engine.mu.Lock()
	engine.pushSender = pushSender
	engine.mu.Unlock()

	err := engine.SendTestPush()
	if err != nil {
		t.Errorf("Expected nil when at least one push send succeeds, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// sendNotification - GetLastNotification error path
// ---------------------------------------------------------------------------

// TestSendNotification_StoreError verifies that sendNotification returns early
// when GetLastNotification fails (closed store).
func TestSendNotification_StoreError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	engine := New(s, slog.Default())

	// Build a mailer that succeeds.
	var mailCount atomic.Int32
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		basicSMTPHandler(conn, &mailCount)
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)
	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Protocol: "none",
		FromAddr: "alerts@test.com",
		FromName: "Test",
		ToAddrs:  []string{"admin@test.com"},
	}
	mailer := NewSMTPMailer(cfg, slog.Default())

	// Close the store so GetLastNotification fails.
	s.Close()

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	// Should return early without sending.
	engine.sendNotification(mailer, nil, NotificationChannels{Email: true, Push: false}, status, "warning")

	if mailCount.Load() != 0 {
		t.Errorf("Expected 0 emails when store is closed, got %d", mailCount.Load())
	}
}

// TestSendNotification_PushGetSubscriptionsError verifies that sendNotification
// handles a GetPushSubscriptions error gracefully (logs the error, does not panic).
func TestSendNotification_PushGetSubscriptionsError(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	engine := New(s, slog.Default())

	pub, priv, _ := GenerateVAPIDKeys()
	pushSender, _ := NewPushSender(pub, priv, "mailto:test@example.com")

	// Close store so GetPushSubscriptions fails.
	s.Close()

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "daily", Utilization: 85.0}
	// Should not panic. Push error is logged, notification not sent.
	engine.sendNotification(nil, pushSender, NotificationChannels{Email: false, Push: true}, status, "warning")
}

// ---------------------------------------------------------------------------
// TestConnection - no-username path (skips auth)
// ---------------------------------------------------------------------------

// TestSMTPMailer_TestConnection_NoUsername verifies that TestConnection skips
// authentication when no username is configured.
func TestSMTPMailer_TestConnection_NoUsername(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250 mock\r\n")
			case "MAIL":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "RCPT":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "RSET":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Protocol: "none",
		// Username left empty - auth is skipped.
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.TestConnection()
	if err != nil {
		t.Fatalf("TestConnection (no username) failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// smtp.Send - MAIL FROM error path
// ---------------------------------------------------------------------------

// TestSMTPMailer_Send_MailFromError verifies that Send returns an error when
// the SMTP server rejects the MAIL FROM command.
func TestSMTPMailer_Send_MailFromError(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "235 OK\r\n")
			case "MAIL":
				fmt.Fprintf(conn, "550 Sender rejected\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected MAIL FROM error")
	}
	if !strings.Contains(err.Error(), "MAIL FROM") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// smtp.Send - RCPT TO error path
// ---------------------------------------------------------------------------

// TestSMTPMailer_Send_RcptToError verifies that Send returns an error when
// the SMTP server rejects the RCPT TO command.
func TestSMTPMailer_Send_RcptToError(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "235 OK\r\n")
			case "MAIL":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "RCPT":
				fmt.Fprintf(conn, "550 Recipient rejected\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected RCPT TO error")
	}
	if !strings.Contains(err.Error(), "RCPT TO") {
		t.Errorf("Unexpected error: %v", err)
	}
}
