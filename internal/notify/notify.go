package notify

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// NotificationEngine evaluates quota statuses and sends alerts via email and push.
type NotificationEngine struct {
	store               *store.Store
	logger              *slog.Logger
	mailer              *SMTPMailer
	pushSender          *PushSender
	vapidPublicKey      string
	mu                  sync.RWMutex
	cfg                 NotificationConfig
	encryptionKey       string // current hex-encoded key for decrypting SMTP passwords
	legacyEncryptionKey string // fallback hex-encoded key for legacy SMTP password migration
}

// NotificationConfig holds threshold and delivery settings.
type NotificationConfig struct {
	Warning   float64                      // global warning threshold (default 80)
	Critical  float64                      // global critical threshold (default 95)
	Overrides map[string]ThresholdOverride // per provider+quota overrides (legacy key: quota only)
	Cooldown  time.Duration                // minimum time between notifications
	Types     NotificationTypes            // which notification types are enabled
	Channels  NotificationChannels         // which delivery channels are enabled
}

// NotificationChannels controls which delivery channels are active.
type NotificationChannels struct {
	Email bool `json:"email"`
	Push  bool `json:"push"`
}

// ThresholdOverride allows per-quota threshold customization.
type ThresholdOverride struct {
	Warning        float64 `json:"warning"`
	Critical       float64 `json:"critical"`
	IsAbsolute     bool    `json:"is_absolute"`
	DisableReset   bool    `json:"disable_reset"`
	DisableWarning bool    `json:"disable_warning"`
	DisableCrit    bool    `json:"disable_critical"`
}

// NotificationTypes controls which notification types are enabled.
type NotificationTypes struct {
	Warning   bool `json:"warning"`
	Critical  bool `json:"critical"`
	Reset     bool `json:"reset"`
	AuthError bool `json:"auth_error"` // Auth failure notifications
}

// QuotaStatus represents the current state of a quota for notification evaluation.
type QuotaStatus struct {
	Provider      string
	QuotaKey      string
	AccountID     string // For multi-account providers (e.g., Codex)
	Utilization   float64
	Limit         float64
	ResetOccurred bool
}

// New creates a new NotificationEngine with default configuration.
func New(s *store.Store, logger *slog.Logger) *NotificationEngine {
	return &NotificationEngine{
		store:  s,
		logger: logger,
		cfg: NotificationConfig{
			Warning:   80,
			Critical:  95,
			Overrides: make(map[string]ThresholdOverride),
			Cooldown:  30 * time.Minute,
			Types:     NotificationTypes{Warning: true, Critical: true, Reset: false},
			Channels:  NotificationChannels{Email: true, Push: true},
		},
	}
}

// SetEncryptionKey sets the encryption key for decrypting sensitive data like SMTP passwords.
// The key should be a hex-encoded 32-byte string suitable for AES-256-GCM.
func (e *NotificationEngine) SetEncryptionKey(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.encryptionKey = key
}

// SetLegacyEncryptionKey sets an optional fallback key used only for
// one-time migration of legacy encrypted SMTP passwords.
func (e *NotificationEngine) SetLegacyEncryptionKey(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.legacyEncryptionKey = key
}

// Config returns a copy of the current notification config.
func (e *NotificationEngine) Config() NotificationConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cfg := e.cfg
	// Copy the map to prevent mutation
	overrides := make(map[string]ThresholdOverride, len(e.cfg.Overrides))
	for k, v := range e.cfg.Overrides {
		overrides[k] = v
	}
	cfg.Overrides = overrides
	return cfg
}

// notificationSettingsJSON matches the JSON shape saved by the handler's UpdateSettings.
type notificationSettingsJSON struct {
	WarningThreshold  float64               `json:"warning_threshold"`
	CriticalThreshold float64               `json:"critical_threshold"`
	NotifyWarning     bool                  `json:"notify_warning"`
	NotifyCritical    bool                  `json:"notify_critical"`
	NotifyReset       bool                  `json:"notify_reset"`
	NotifyAuthError   bool                  `json:"notify_auth_error"`
	CooldownMinutes   int                   `json:"cooldown_minutes"`
	Channels          *NotificationChannels `json:"channels,omitempty"`
	Overrides         []struct {
		QuotaKey       string  `json:"quota_key"`
		Provider       string  `json:"provider"`
		Warning        float64 `json:"warning"`
		Critical       float64 `json:"critical"`
		IsAbsolute     bool    `json:"is_absolute"`
		DisableReset   bool    `json:"disable_reset"`
		DisableWarning bool    `json:"disable_warning"`
		DisableCrit    bool    `json:"disable_critical"`
	} `json:"overrides"`
}

// Reload reads notification configuration from the settings table.
// The handler stores notifications as a single JSON blob under key "notifications".
func (e *NotificationEngine) Reload() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	v, err := e.store.GetSetting("notifications")
	if err != nil || v == "" {
		return nil // no notification settings saved yet, keep defaults
	}

	var notif notificationSettingsJSON
	if err := json.Unmarshal([]byte(v), &notif); err != nil {
		return fmt.Errorf("notify.Reload: invalid notifications JSON: %w", err)
	}

	if notif.WarningThreshold > 0 {
		e.cfg.Warning = notif.WarningThreshold
	}
	if notif.CriticalThreshold > 0 {
		e.cfg.Critical = notif.CriticalThreshold
	}
	if notif.CooldownMinutes > 0 {
		e.cfg.Cooldown = time.Duration(notif.CooldownMinutes) * time.Minute
	}
	e.cfg.Types = NotificationTypes{
		Warning:   notif.NotifyWarning,
		Critical:  notif.NotifyCritical,
		Reset:     notif.NotifyReset,
		AuthError: notif.NotifyAuthError,
	}

	overrides := make(map[string]ThresholdOverride, len(notif.Overrides))
	for _, o := range notif.Overrides {
		key := strings.TrimSpace(o.QuotaKey)
		if key == "" {
			continue
		}
		if provider := normalizeNotificationProvider(o.Provider); provider != "legacy" {
			key = notificationOverrideKey(provider, key)
		}
		overrides[key] = ThresholdOverride{
			Warning:        o.Warning,
			Critical:       o.Critical,
			IsAbsolute:     o.IsAbsolute,
			DisableReset:   o.DisableReset,
			DisableWarning: o.DisableWarning,
			DisableCrit:    o.DisableCrit,
		}
	}
	e.cfg.Overrides = overrides

	if notif.Channels != nil {
		e.cfg.Channels = *notif.Channels
	} else {
		// Default: both channels enabled
		e.cfg.Channels = NotificationChannels{Email: true, Push: true}
	}

	return nil
}

// smtpSettingsJSON matches the JSON shape saved by the handler's UpdateSettings.
type smtpSettingsJSON struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name"`
	To          string `json:"to"`
}

// ConfigureSMTP initializes or updates the SMTP mailer from DB settings.
// The handler stores SMTP config as a single JSON blob under key "smtp".
func (e *NotificationEngine) ConfigureSMTP() error {
	smtpJSON, err := e.store.GetSetting("smtp")
	if err != nil {
		return fmt.Errorf("notify.ConfigureSMTP: %w", err)
	}
	if smtpJSON == "" {
		e.mu.Lock()
		e.mailer = nil
		e.mu.Unlock()
		return nil
	}

	var s smtpSettingsJSON
	if err := json.Unmarshal([]byte(smtpJSON), &s); err != nil {
		return fmt.Errorf("notify.ConfigureSMTP: invalid smtp JSON: %w", err)
	}

	if s.Host == "" {
		e.mu.Lock()
		e.mailer = nil
		e.mu.Unlock()
		return nil
	}

	port := s.Port
	if port == 0 {
		port = 587
	}

	// Parse comma-separated recipients
	var toAddrs []string
	for _, addr := range strings.Split(s.To, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			toAddrs = append(toAddrs, addr)
		}
	}

	// Decrypt SMTP password if encrypted.
	// Migration path: if current key fails, try legacy key and re-encrypt with current key.
	password := s.Password
	e.mu.RLock()
	key := e.encryptionKey
	legacyKey := e.legacyEncryptionKey
	e.mu.RUnlock()

	if key != "" && password != "" && len(password) > 24 {
		// Try current key first.
		if decrypted, err := Decrypt(password, key); err == nil {
			password = decrypted
		} else {
			migrated := false
			if legacyKey != "" && legacyKey != key {
				if legacyPlaintext, legacyErr := Decrypt(password, legacyKey); legacyErr == nil {
					if reEncrypted, reEncErr := Encrypt(legacyPlaintext, key); reEncErr == nil {
						s.Password = reEncrypted
						smtpJSONUpdated, marshalErr := json.Marshal(s)
						if marshalErr == nil {
							if saveErr := e.store.SetSetting("smtp", string(smtpJSONUpdated)); saveErr != nil {
								e.logger.Warn("failed to persist migrated SMTP password", "error", saveErr)
							} else {
								e.logger.Info("migrated legacy SMTP password encryption to current key")
								migrated = true
							}
						} else {
							e.logger.Warn("failed to marshal SMTP settings during migration", "error", marshalErr)
						}
					} else {
						e.logger.Warn("failed to re-encrypt SMTP password during migration", "error", reEncErr)
					}
					password = legacyPlaintext
				}
			}
			if !migrated {
				// Decrypt failure can still mean plaintext or invalid ciphertext.
				e.logger.Debug("SMTP password decryption failed (may be plaintext)", "error", err)
			}
		}
	}

	cfg := SMTPConfig{
		Host:     s.Host,
		Port:     port,
		Username: s.Username,
		Password: password,
		Protocol: s.Protocol,
		FromAddr: s.FromAddress,
		FromName: s.FromName,
		ToAddrs:  toAddrs,
	}

	e.mu.Lock()
	e.mailer = NewSMTPMailer(cfg, e.logger)
	e.mu.Unlock()

	return nil
}

// ConfigurePush initializes the push notification sender.
// Loads or generates VAPID keys, stored in the settings table as "vapid_keys".
func (e *NotificationEngine) ConfigurePush() error {
	keysJSON, err := e.store.GetSetting("vapid_keys")
	if err != nil {
		return fmt.Errorf("notify.ConfigurePush: %w", err)
	}

	var pub, priv string
	if keysJSON != "" {
		var keys struct {
			Public  string `json:"public"`
			Private string `json:"private"`
		}
		if err := json.Unmarshal([]byte(keysJSON), &keys); err != nil {
			return fmt.Errorf("notify.ConfigurePush: invalid vapid_keys JSON: %w", err)
		}
		pub = keys.Public
		priv = keys.Private
	}

	// Generate new keys if not present
	if pub == "" || priv == "" {
		pub, priv, err = GenerateVAPIDKeys()
		if err != nil {
			return fmt.Errorf("notify.ConfigurePush: %w", err)
		}
		keysData, _ := json.Marshal(map[string]string{"public": pub, "private": priv})
		if err := e.store.SetSetting("vapid_keys", string(keysData)); err != nil {
			return fmt.Errorf("notify.ConfigurePush: failed to save VAPID keys: %w", err)
		}
		e.logger.Info("Generated new VAPID key pair for push notifications")
	}

	sender, err := NewPushSender(pub, priv, "mailto:onwatch@localhost")
	if err != nil {
		return fmt.Errorf("notify.ConfigurePush: %w", err)
	}

	e.mu.Lock()
	e.pushSender = sender
	e.vapidPublicKey = pub
	e.mu.Unlock()

	return nil
}

// GetVAPIDPublicKey returns the VAPID public key for client-side push subscription.
func (e *NotificationEngine) GetVAPIDPublicKey() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.vapidPublicKey
}

// SendTestPush sends a test push notification to all subscribed devices.
func (e *NotificationEngine) SendTestPush() error {
	e.mu.RLock()
	sender := e.pushSender
	e.mu.RUnlock()

	if sender == nil {
		return fmt.Errorf("push notifications not configured")
	}

	subs, err := e.store.GetPushSubscriptions()
	if err != nil {
		return fmt.Errorf("failed to get subscriptions: %w", err)
	}

	if len(subs) == 0 {
		return fmt.Errorf("no push subscriptions found")
	}

	var lastErr error
	sent := 0
	for _, sub := range subs {
		ps := PushSubscription{Endpoint: sub.Endpoint}
		ps.Keys.P256dh = sub.P256dh
		ps.Keys.Auth = sub.Auth
		if err := sender.Send(ps, "[onWatch] Test Push", "Push notifications are working correctly."); err != nil {
			lastErr = err
			e.logger.Error("test push failed", "endpoint", sub.Endpoint, "error", err)
		} else {
			sent++
		}
	}

	if sent == 0 && lastErr != nil {
		return lastErr
	}

	return nil
}

// Check evaluates a quota status against thresholds and sends notifications if needed.
// Runs synchronously -- no goroutines spawned.
func (e *NotificationEngine) Check(status QuotaStatus) {
	e.mu.RLock()
	cfg := e.cfg
	mailer := e.mailer
	pushSender := e.pushSender
	e.mu.RUnlock()

	// Need at least one channel configured
	if mailer == nil && pushSender == nil {
		return
	}

	// Handle reset: clear notification log so alerts can fire again in the new cycle
	provider := normalizeNotificationProvider(status.Provider)
	quotaKey := notificationQuotaKey(status)
	overrideKey := notificationOverrideKey(provider, status.QuotaKey)
	override, hasOverride := cfg.Overrides[overrideKey]
	if !hasOverride {
		// Backward compatibility: legacy settings keyed by quota only.
		override, hasOverride = cfg.Overrides[status.QuotaKey]
	}
	if status.ResetOccurred {
		if err := e.store.ClearNotificationLog(provider, quotaKey); err != nil {
			e.logger.Error("failed to clear notification log on reset", "error", err)
		}
		if cfg.Types.Reset && !(hasOverride && override.DisableReset) {
			e.sendNotification(mailer, pushSender, cfg.Channels, status, "reset")
		}
		return
	}

	// Resolve thresholds
	warningThreshold := cfg.Warning
	criticalThreshold := cfg.Critical
	if hasOverride {
		if override.IsAbsolute && status.Limit > 0 {
			if override.Warning > 0 {
				warningThreshold = (override.Warning / status.Limit) * 100
			}
			if override.Critical > 0 {
				criticalThreshold = (override.Critical / status.Limit) * 100
			}
		} else {
			if override.Warning > 0 {
				warningThreshold = override.Warning
			}
			if override.Critical > 0 {
				criticalThreshold = override.Critical
			}
		}
	}

	// Check critical first (higher priority)
	if status.Utilization >= criticalThreshold && cfg.Types.Critical && !(hasOverride && override.DisableCrit) {
		e.sendNotification(mailer, pushSender, cfg.Channels, status, "critical")
		return
	}

	// Check warning
	if status.Utilization >= warningThreshold && cfg.Types.Warning && !(hasOverride && override.DisableWarning) {
		e.sendNotification(mailer, pushSender, cfg.Channels, status, "warning")
		return
	}
}

// SendTestEmail sends a test email to verify SMTP configuration.
func (e *NotificationEngine) SendTestEmail() error {
	e.mu.RLock()
	mailer := e.mailer
	e.mu.RUnlock()

	if mailer == nil {
		return fmt.Errorf("SMTP not configured")
	}

	subject := "[onWatch] Test Email"
	body := "This is a test email from onWatch.\n\nIf you received this, your SMTP settings are configured correctly.\n\n-- Sent by onWatch"
	return mailer.Send(subject, body)
}

// TestSMTPDiag sends a test email and returns diagnostics from the connection.
// Uses a single SMTP connection for both diagnostics and delivery.
func (e *NotificationEngine) TestSMTPDiag() (string, error) {
	e.mu.RLock()
	mailer := e.mailer
	e.mu.RUnlock()

	if mailer == nil {
		return "", fmt.Errorf("SMTP not configured")
	}

	subject := "[onWatch] Test Email"
	body := "This is a test email from onWatch.\n\nIf you received this, your SMTP settings are configured correctly.\n\n-- Sent by onWatch"
	res := mailer.SendWithDiag(subject, body)
	return res.Diagnostics, res.Error
}

// sendNotification sends notifications via enabled channels.
// Each provider+quota+type combination fires at most once per cycle.
// The notification_log entry is cleared on quota reset (see Check/resetOccurred).
func (e *NotificationEngine) sendNotification(mailer *SMTPMailer, pushSender *PushSender, channels NotificationChannels, status QuotaStatus, notifType string) {
	provider := normalizeNotificationProvider(status.Provider)
	quotaKey := notificationQuotaKey(status)
	sentAt, _, err := e.store.GetLastNotification(provider, quotaKey, notifType)
	if err != nil {
		e.logger.Error("failed to check notification log", "error", err)
		return
	}
	// Already sent for this cycle - skip (log is cleared on reset)
	if !sentAt.IsZero() {
		e.logger.Debug("notification already sent for this cycle",
			"quota", quotaKey, "type", notifType,
			"sent_at", sentAt)
		return
	}

	subject := e.buildSubject(status, notifType)
	body := e.buildBody(status, notifType)
	sent := false

	// Send via email if enabled and configured
	if channels.Email && mailer != nil {
		if err := mailer.Send(subject, body); err != nil {
			e.logger.Error("failed to send email notification", "error", err,
				"quota", quotaKey, "type", notifType)
		} else {
			sent = true
		}
	}

	// Send via push if enabled and configured
	if channels.Push && pushSender != nil {
		subs, err := e.store.GetPushSubscriptions()
		if err != nil {
			e.logger.Error("failed to get push subscriptions", "error", err)
		} else {
			for _, sub := range subs {
				ps := PushSubscription{Endpoint: sub.Endpoint}
				ps.Keys.P256dh = sub.P256dh
				ps.Keys.Auth = sub.Auth
				if err := pushSender.Send(ps, subject, body); err != nil {
					e.logger.Error("failed to send push notification", "error", err,
						"endpoint", sub.Endpoint)
					// If subscription is gone (410), remove it
					if strings.Contains(err.Error(), "410") {
						e.store.DeletePushSubscription(sub.Endpoint)
					}
				} else {
					sent = true
				}
			}
		}
	}

	// Log the notification only if at least one channel succeeded
	if sent {
		if err := e.store.UpsertNotificationLog(provider, quotaKey, notifType, status.Utilization); err != nil {
			e.logger.Error("failed to log notification", "error", err)
		}
	}
}

func normalizeNotificationProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		return "legacy"
	}
	return p
}

func notificationOverrideKey(provider, quotaKey string) string {
	return normalizeNotificationProvider(provider) + ":" + strings.TrimSpace(quotaKey)
}

// notificationQuotaKey generates a unique key for notification tracking.
// For multi-account providers like Codex, the key includes the account ID.
func notificationQuotaKey(status QuotaStatus) string {
	key := status.QuotaKey
	// Include account ID for multi-account providers (Codex)
	// AccountID "1" is the default account, so we only prefix for other accounts
	if status.AccountID != "" && status.AccountID != "1" {
		key = status.AccountID + ":" + key
	}
	return key
}

// titleCase capitalizes the first letter of a string.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// buildSubject creates the email subject line.
func (e *NotificationEngine) buildSubject(status QuotaStatus, notifType string) string {
	switch notifType {
	case "critical":
		return fmt.Sprintf("[CRITICAL] %s quota %s at %.1f%%",
			titleCase(status.Provider), status.QuotaKey, status.Utilization)
	case "warning":
		return fmt.Sprintf("[WARNING] %s quota %s at %.1f%%",
			titleCase(status.Provider), status.QuotaKey, status.Utilization)
	case "reset":
		return fmt.Sprintf("[RESET] %s quota %s has been reset",
			titleCase(status.Provider), status.QuotaKey)
	default:
		return fmt.Sprintf("[%s] %s quota %s", notifType, status.Provider, status.QuotaKey)
	}
}

// buildBody creates the email body text.
func (e *NotificationEngine) buildBody(status QuotaStatus, notifType string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Provider: %s\n", status.Provider))
	sb.WriteString(fmt.Sprintf("Quota: %s\n", status.QuotaKey))
	sb.WriteString(fmt.Sprintf("Utilization: %.1f%%\n", status.Utilization))
	if status.Limit > 0 {
		sb.WriteString(fmt.Sprintf("Limit: %.0f\n", status.Limit))
	}
	sb.WriteString(fmt.Sprintf("Alert Type: %s\n", notifType))
	sb.WriteString(fmt.Sprintf("Time: %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString("\n-- Sent by onWatch")
	return sb.String()
}

// AuthErrorAlert represents an authentication error for notification purposes.
type AuthErrorAlert struct {
	Provider    string
	Title       string
	Message     string
	AccountID   string // For multi-account providers
	IsRecovable bool   // If false, requires manual re-authentication
}

// SendAuthErrorNotification sends an auth error alert via email and/or push.
// Also creates an in-dashboard system alert for when the user logs in.
// Returns true if at least one notification was sent successfully.
func (e *NotificationEngine) SendAuthErrorNotification(alert AuthErrorAlert) bool {
	e.mu.RLock()
	cfg := e.cfg
	mailer := e.mailer
	pushSender := e.pushSender
	e.mu.RUnlock()

	// Check if auth error notifications are enabled
	if !cfg.Types.AuthError {
		return false
	}

	// Build notification content
	subject := fmt.Sprintf("[AUTH ERROR] %s - %s", titleCase(alert.Provider), alert.Title)
	body := e.buildAuthErrorBody(alert)

	sent := false

	// Send via email if enabled and configured
	if cfg.Channels.Email && mailer != nil {
		if err := mailer.Send(subject, body); err != nil {
			e.logger.Error("failed to send auth error email", "error", err, "provider", alert.Provider)
		} else {
			sent = true
			e.logger.Info("sent auth error email notification", "provider", alert.Provider)
		}
	}

	// Send via push if enabled and configured
	if cfg.Channels.Push && pushSender != nil {
		subs, err := e.store.GetPushSubscriptions()
		if err != nil {
			e.logger.Error("failed to get push subscriptions", "error", err)
		} else {
			for _, sub := range subs {
				ps := PushSubscription{Endpoint: sub.Endpoint}
				ps.Keys.P256dh = sub.P256dh
				ps.Keys.Auth = sub.Auth
				if err := pushSender.Send(ps, subject, alert.Message); err != nil {
					e.logger.Error("failed to send auth error push", "error", err, "endpoint", sub.Endpoint)
					if strings.Contains(err.Error(), "410") {
						e.store.DeletePushSubscription(sub.Endpoint)
					}
				} else {
					sent = true
				}
			}
		}
	}

	// Create in-dashboard system alert (always, regardless of email/push success)
	severity := "warning"
	if !alert.IsRecovable {
		severity = "error"
	}
	alertType := "auth_error"
	if !alert.IsRecovable {
		alertType = "token_refresh_failed"
	}

	// Check if there's already an active alert of this type for this provider
	hasActive, err := e.store.HasActiveAlertOfType(alert.Provider, alertType)
	if err != nil {
		e.logger.Error("failed to check for existing alert", "error", err)
	}
	if !hasActive {
		metadata := ""
		if alert.AccountID != "" {
			metadata = fmt.Sprintf(`{"account_id":"%s"}`, alert.AccountID)
		}
		if _, err := e.store.CreateSystemAlert(
			alert.Provider, alertType, alert.Title, alert.Message, severity, metadata,
		); err != nil {
			e.logger.Error("failed to create system alert", "error", err)
		}
	}

	return sent
}

// buildAuthErrorBody creates the email body for auth error notifications.
func (e *NotificationEngine) buildAuthErrorBody(alert AuthErrorAlert) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Provider: %s\n", titleCase(alert.Provider)))
	sb.WriteString(fmt.Sprintf("Issue: %s\n", alert.Title))
	sb.WriteString(fmt.Sprintf("Details: %s\n", alert.Message))
	if alert.AccountID != "" {
		sb.WriteString(fmt.Sprintf("Account: %s\n", alert.AccountID))
	}
	sb.WriteString(fmt.Sprintf("Time: %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString("\n")
	if alert.IsRecovable {
		sb.WriteString("This issue may resolve automatically. If it persists, please check your credentials.\n")
	} else {
		sb.WriteString("ACTION REQUIRED: Please re-authenticate to resume quota tracking.\n")
	}
	sb.WriteString("\n-- Sent by onWatch")
	return sb.String()
}
