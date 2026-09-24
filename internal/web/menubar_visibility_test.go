package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestMenubarNewProviderVisibilitySurvivesSavesAndRestart(t *testing.T) {
	h, s := newMenubarTestHandler(t)
	defer s.Close()
	h.config = &config.Config{SyntheticAPIKey: "synthetic", ZaiAPIKey: "zai", CommandCodeAPIKey: "commandcode"}
	settings := menubar.DefaultSettings()
	settings.VisibleProviders = []string{"synthetic"}
	settings.ProvidersOrder = []string{"synthetic", "zai", "commandcode", "grok"}
	if err := s.SetMenubarSettings(settings); err != nil {
		t.Fatal(err)
	}

	// Command Code is configured but has not returned data yet. Its first
	// appearance must not reveal the previously hidden Z.ai provider.
	assertMenubarVisibleKeys(t, h, []string{"synthetic", "commandcode"})
	insertTestCommandCodeSnapshot(t, s, time.Now().UTC())
	assertMenubarSummaryIDs(t, h, []string{"synthetic", "commandcode"})
	assertMenubarPreferencesVisible(t, h, []string{"synthetic", "commandcode"})

	// A user's explicit hide wins on both settings save paths and on restart.
	for _, path := range []string{"/api/menubar/preferences", "/api/settings"} {
		body := `{"visible_providers":["synthetic"]}`
		if path == "/api/settings" {
			body = `{"menubar":{"visible_providers":["synthetic"]}}`
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		if path == "/api/settings" {
			h.UpdateSettings(rr, req)
		} else {
			h.MenubarPreferences(rr, req)
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, rr.Code, rr.Body.String())
		}
		assertMenubarVisibleKeys(t, h, []string{"synthetic"})
	}
	restarted := NewHandler(s, tracker.New(s, nil), nil, nil, h.config)
	assertMenubarVisibleKeys(t, restarted, []string{"synthetic"})
	assertMenubarSummaryIDs(t, restarted, []string{"synthetic"})

	// A later provider is still new even when the dashboard had already put
	// its name in providers_order while it was unconfigured.
	restarted.config.GrokEnabled = true
	assertMenubarVisibleKeys(t, restarted, []string{"synthetic", "grok"})
}

func assertMenubarVisibleKeys(t *testing.T, h *Handler, want []string) {
	t.Helper()
	if _, err := h.BuildMenubarSnapshot(); err != nil {
		t.Fatal(err)
	}
	settings, err := h.store.GetMenubarSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings.VisibleProviders, want) {
		t.Fatalf("visible providers = %v, want %v", settings.VisibleProviders, want)
	}
}

func assertMenubarSummaryIDs(t *testing.T, h *Handler, want []string) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.MenubarSummary(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/summary", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("summary returned %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(body.Providers))
	for _, provider := range body.Providers {
		ids = append(ids, provider.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("summary providers = %v, want %v", ids, want)
	}
}

func assertMenubarPreferencesVisible(t *testing.T, h *Handler, want []string) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.MenubarPreferences(rr, httptest.NewRequest(http.MethodGet, "/api/menubar/preferences", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("preferences returned %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		VisibleProviders []string `json:"visible_providers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.VisibleProviders, want) {
		t.Fatalf("preferences visible providers = %v, want %v", body.VisibleProviders, want)
	}
}
