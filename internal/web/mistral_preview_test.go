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
	"testing"
	"time"
)

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
	mux := http.NewServeMux()
	assets, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(assets))))
	for path, fn := range map[string]http.HandlerFunc{"/": h.Dashboard, "/menubar": h.MenubarPage, "/api/current": h.Current, "/api/history": h.History, "/api/insights": h.Insights, "/api/summary": h.Summary, "/api/cycles": h.Cycles, "/api/cycle-overview": h.CycleOverview, "/api/logging-history": h.LoggingHistory, "/api/menubar/summary": h.MenubarSummary, "/api/menubar/preferences": h.MenubarPreferences, "/api/menubar/tray-title": h.MenubarTrayTitle, "/api/settings": h.GetSettings, "/api/providers": h.Providers, "/api/providers/status": h.ProvidersStatus, "/api/sessions": h.Sessions, "/api/capabilities": h.Capabilities} {
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
