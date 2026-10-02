package web

import (
	"context"
	"fmt"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type mistralPreviewRecovery struct {
	mu       sync.Mutex
	started  time.Time
	db       *store.Store
	snapshot *api.MistralSnapshot
}

func (p *mistralPreviewRecovery) Start(string) error    { return nil }
func (p *mistralPreviewRecovery) Stop(string)           {}
func (p *mistralPreviewRecovery) IsRunning(string) bool { return true }
func (p *mistralPreviewRecovery) RequestRetry(string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = time.Now()
	p.snapshot.CapturedAt = time.Now().UTC()
	for i := range p.snapshot.Quotas {
		p.snapshot.Quotas[i].CapturedAt = p.snapshot.CapturedAt
	}
	if err := p.db.SaveMistral(context.Background(), p.snapshot); err != nil {
		return err
	}
	return p.db.SetSetting("mistral_status", "ok")
}
func (p *mistralPreviewRecovery) ConnectionState(string) (api.ProviderConnection, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started.IsZero() {
		c := (&api.MistralConnectionError{Reason: "browser_access_denied", Browser: "chrome"}).Connection()
		c.CanRetry = true
		return c, true
	}
	return api.ProviderConnection{Retrying: time.Since(p.started) < 3*time.Second, CanRetry: time.Since(p.started) >= 3*time.Second}, true
}

// Opt-in local preview uses synthetic data only and never reads browser cookies.
func TestMistralPreview(t *testing.T) {
	if os.Getenv("ONWATCH_MISTRAL_PREVIEW") != "1" {
		t.Skip("local browser QA only")
	}
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now().UTC()
	reset := now.Add(7 * 24 * time.Hour)
	snap := &api.MistralSnapshot{Identity: "preview", CapturedAt: now, Status: "partial", Quotas: []api.MistralQuota{{Name: "api_included", Used: 2.5, Limit: 10, Utilization: 25, Currency: "EUR", CapturedAt: now, ResetsAt: &reset}, {Name: "vibe_included", Used: 50, Limit: 100, Utilization: 50, Currency: "EUR", CapturedAt: now, ResetsAt: &reset}}}
	if e = db.SaveMistral(context.Background(), snap); e != nil {
		t.Fatal(e)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true, PollInterval: 120 * time.Second})
	if os.Getenv("ONWATCH_MISTRAL_PREVIEW_RECOVERY") == "1" {
		h.SetAgentManager(&mistralPreviewRecovery{db: db, snapshot: snap})
		if err := db.SetSetting("mistral_status", "reconnect"); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mistral/retry", h.RetryMistral)
	mux.HandleFunc("/api/menubar/mistral/retry", h.RetryMistral)
	assets, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(assets))))
	for path, fn := range map[string]http.HandlerFunc{"/": h.Dashboard, "/menubar": h.MenubarPage, "/api/current": h.Current, "/api/history": h.History, "/api/insights": h.Insights, "/api/summary": h.Summary, "/api/cycles": h.Cycles, "/api/cycle-overview": h.CycleOverview, "/api/logging-history": h.LoggingHistory, "/api/menubar/summary": h.MenubarSummary, "/api/menubar/preferences": h.MenubarPreferences, "/api/menubar/tray-title": h.MenubarTrayTitle, "/api/settings": h.GetSettings, "/api/providers": h.Providers, "/api/providers/status": h.ProvidersStatus, "/api/sessions": h.Sessions, "/api/capabilities": h.Capabilities} {
		if path == "/menubar" && os.Getenv("ONWATCH_MISTRAL_PREVIEW_GRANT") == "1" {
			mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
				recorder := httptest.NewRecorder()
				h.MenubarPage(recorder, r)
				// Fake the native host in this opt-in, synthetic-only visual fixture.
				const script = `<script>window.__onwatchCanGrantBrowserAccess=true;window.webkit={messageHandlers:{onwatchAction:{postMessage(action){if(action==='grant_browser_access'){window.__onwatchBrowserGrantResult('pending');setTimeout(()=>window.__onwatchBrowserGrantResult('cancelled'),1500)}}}}};</script>`
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, strings.Replace(recorder.Body.String(), "<head>", "<head>"+script, 1))
			})
			continue
		}
		mux.HandleFunc(path, fn)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	fmt.Printf("MISTRAL_PREVIEW_URL=%s\n", server.URL)
	if e := os.WriteFile("/tmp/onwatch-mistral-preview-url", []byte(server.URL), 0600); e != nil {
		t.Fatal(e)
	}
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	<-timer.C
}
