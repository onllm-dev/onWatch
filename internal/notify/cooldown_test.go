package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// setupRepeatEngine wires a webhook-only engine with the given repeat policy and
// returns the engine plus a delivery counter.
func setupRepeatEngine(t *testing.T, repeat bool, cooldownMinutes int) (*NotificationEngine, *store.Store, func() int) {
	t.Helper()

	s := newTestStore(t)
	engine := newTestEngine(t, s)
	capture := newWebhookCapture(t)

	storeWebhookConfig(t, s, WebhookConfig{URL: capture.srv.URL, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyWarning:     true,
		NotifyCritical:    true,
		NotifyReset:       true,
		NotifyRepeat:      repeat,
		CooldownMinutes:   cooldownMinutes,
		Channels:          &NotificationChannels{Webhook: true},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	return engine, s, capture.count
}

// Default behaviour is unchanged: an alert fires once per quota cycle.
func TestCheckWithoutRepeatAlertsOncePerCycle(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, false, 30)
	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(status)
	// Far beyond any cooldown - repeat is off, so it must still stay silent.
	now = now.Add(24 * time.Hour)
	engine.Check(status)

	if got := count(); got != 1 {
		t.Errorf("delivered %d alerts, want 1 (repeat disabled means once per cycle)", got)
	}
}

func TestCheckWithRepeatSuppressesWithinCooldown(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 30)
	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(status)
	now = now.Add(29 * time.Minute)
	engine.Check(status)

	if got := count(); got != 1 {
		t.Errorf("delivered %d alerts, want 1 (still inside the 30 minute cooldown)", got)
	}
}

func TestCheckWithRepeatResendsAfterCooldown(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 30)
	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(status)
	now = now.Add(31 * time.Minute)
	engine.Check(status)
	// The resend restarts the cooldown rather than opening a window.
	now = now.Add(2 * time.Minute)
	engine.Check(status)

	if got := count(); got != 2 {
		t.Errorf("delivered %d alerts, want 2 (one resend after the cooldown elapsed)", got)
	}
}

// The cooldown is tracked per provider+quota+type, so one quota's resend does
// not suppress another's.
func TestCheckRepeatCooldownIsPerQuota(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 30)

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100})
	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "seven_day", Utilization: 97, Limit: 100})
	engine.Check(QuotaStatus{Provider: "codex", QuotaKey: "five_hour", Utilization: 97, Limit: 100})

	if got := count(); got != 3 {
		t.Fatalf("delivered %d alerts, want 3 distinct quotas", got)
	}

	now = now.Add(31 * time.Minute)
	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100})

	if got := count(); got != 4 {
		t.Errorf("delivered %d alerts, want 4 (only the elapsed quota resends)", got)
	}
}

// Escalation must not be blocked by a warning's cooldown: warning and critical
// are separate notification types.
func TestCheckRepeatCooldownDoesNotBlockEscalation(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 30)

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 85, Limit: 100})
	now = now.Add(1 * time.Minute)
	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100})

	if got := count(); got != 2 {
		t.Errorf("delivered %d alerts, want 2 (warning then critical)", got)
	}
}

// A reset clears the log, so the next threshold crossing alerts immediately
// regardless of the cooldown.
func TestCheckRepeatCooldownClearedByReset(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 30)
	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(status)
	now = now.Add(1 * time.Minute)
	engine.Check(QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", ResetOccurred: true})
	now = now.Add(1 * time.Minute)
	engine.Check(status)

	// critical, reset, critical
	if got := count(); got != 3 {
		t.Errorf("delivered %d alerts, want 3 (reset clears the cooldown)", got)
	}
}

// A zero or missing cooldown must not turn repeat into a per-poll firehose.
func TestCheckRepeatFallsBackToDefaultCooldown(t *testing.T) {
	engine, _, count := setupRepeatEngine(t, true, 0)
	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}

	now := time.Now()
	engine.now = func() time.Time { return now }

	engine.Check(status)
	now = now.Add(1 * time.Minute)
	engine.Check(status)

	if got := count(); got != 1 {
		t.Errorf("delivered %d alerts, want 1 (default cooldown applies when unset)", got)
	}
	if cd := engine.Config().Cooldown; cd != 30*time.Minute {
		t.Errorf("Cooldown = %v, want the 30 minute default", cd)
	}
}

func TestReloadParsesRepeatSetting(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	if engine.Config().Repeat {
		t.Error("Repeat defaults to true, want false so existing installs keep one alert per cycle")
	}

	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyRepeat:      true,
		CooldownMinutes:   45,
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	cfg := engine.Config()
	if !cfg.Repeat {
		t.Error("Repeat = false, want the saved value")
	}
	if cfg.Cooldown != 45*time.Minute {
		t.Errorf("Cooldown = %v, want 45m", cfg.Cooldown)
	}
}

// Repeat also governs resends on the email channel, not just webhooks.
func TestCheckRepeatAppliesToEmailChannel(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	mailCount, cleanup := setupSMTPAndMailer(t, s, engine)
	defer cleanup()

	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyCritical:    true,
		NotifyRepeat:      true,
		CooldownMinutes:   30,
		Channels:          &NotificationChannels{Email: true},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	now := time.Now()
	engine.now = func() time.Time { return now }

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}
	engine.Check(status)
	now = now.Add(31 * time.Minute)
	engine.Check(status)

	if got := mailCount.Load(); got != 2 {
		t.Errorf("sent %d emails, want 2 (resend after the cooldown)", got)
	}
}

// The settings blob round-trips the flag so a save does not silently clear it.
func TestNotificationSettingsJSONCarriesRepeat(t *testing.T) {
	raw, err := json.Marshal(notificationSettingsJSON{NotifyRepeat: true, CooldownMinutes: 30})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(raw); !strings.Contains(got, `"notify_repeat":true`) {
		t.Errorf("marshalled settings = %s, want a notify_repeat field", got)
	}

	var back notificationSettingsJSON
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !back.NotifyRepeat {
		t.Error("NotifyRepeat did not survive a round trip")
	}
}

// A repeat resend must still reach a webhook whose event toggle is on.
func TestCheckRepeatResendReachesWebhookPayload(t *testing.T) {
	s := newTestStore(t)
	engine := newTestEngine(t, s)

	var payloads []WebhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p WebhookPayload
		json.NewDecoder(r.Body).Decode(&p)
		payloads = append(payloads, p)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	storeWebhookConfig(t, s, WebhookConfig{URL: srv.URL, TimeoutSeconds: 2, Events: allWebhookEvents()})
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}
	storeNotificationConfig(t, s, notificationSettingsJSON{
		WarningThreshold:  80,
		CriticalThreshold: 95,
		NotifyCritical:    true,
		NotifyRepeat:      true,
		CooldownMinutes:   30,
		Channels:          &NotificationChannels{Webhook: true},
	})
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	now := time.Now()
	engine.now = func() time.Time { return now }

	status := QuotaStatus{Provider: "anthropic", QuotaKey: "five_hour", Utilization: 97, Limit: 100}
	engine.Check(status)
	now = now.Add(31 * time.Minute)
	engine.Check(status)

	if len(payloads) != 2 {
		t.Fatalf("delivered %d payloads, want 2", len(payloads))
	}
	for i, p := range payloads {
		if p.Event != EventCritical {
			t.Errorf("payload[%d].Event = %q, want %q", i, p.Event, EventCritical)
		}
	}
}

// Repeats must never degrade into a per-poll firehose, even if the stored
// cooldown is missing or nonsensical.
func TestNewRepeatPolicyAlwaysHasPositiveCooldown(t *testing.T) {
	cases := []struct {
		name string
		cfg  NotificationConfig
		want time.Duration
	}{
		{"zero falls back", NotificationConfig{Repeat: true}, defaultNotificationCooldown},
		{"negative falls back", NotificationConfig{Repeat: true, Cooldown: -5 * time.Minute}, defaultNotificationCooldown},
		{"configured value kept", NotificationConfig{Repeat: true, Cooldown: 45 * time.Minute}, 45 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newRepeatPolicy(tc.cfg)
			if got.cooldown != tc.want {
				t.Errorf("cooldown = %v, want %v", got.cooldown, tc.want)
			}
			if !got.enabled {
				t.Error("enabled = false, want true")
			}
		})
	}
}
