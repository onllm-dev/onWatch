package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestZenmuxBrowserSynthetic runs only when an isolated Chromium is supplied.
// The in-process server has no agent, so it cannot contact a provider.
func TestZenmuxBrowserSynthetic(t *testing.T) {
	browser := os.Getenv("ZENMUX_BROWSER_BIN")
	if browser == "" {
		t.Skip("isolated Chromium not configured")
	}
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	zero, cap := 0.0, 34560.0
	expiry := time.Date(2027, 4, 12, 8, 26, 56, 0, time.UTC)
	reset := time.Now().UTC().Add(time.Hour)
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Plan: &api.ZenmuxPlan{Tier: stringPointerZenmux("ultra"), ExpiresAt: &expiry}, Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero, UsedFlows: &zero, ResetsAt: &reset}, Quota7Day: &api.ZenmuxWindow{}, QuotaMonthly: &api.ZenmuxMonthly{MaxFlows: &cap}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, nil, nil, nil, &config.Config{ZenmuxManagementAPIKey: "synthetic-test-value", SyntheticAPIKey: "synthetic-provider", PollInterval: time.Minute})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page := httptest.NewRecorder()
		h.Dashboard(page, r)
		body := page.Body.String()
		// Chrome's virtual-time mode does not drive animation frames. The fixture
		// observes rendered controls, and advances Date.now only after the countdown
		// is visible. The existing dashboard interval then performs the expiry.
		// Fallback: the quota cards and result containers have no semantic names;
		// user actions below use their labels or visible button text.
		if os.Getenv("ZENMUX_BROWSER_RUNNER") == "" {
			body = strings.Replace(body, "</head>", `<script>
window.requestAnimationFrame = callback => setTimeout(callback, 0);
let fixtureNow = Date.now();
Date.now = () => fixtureNow;
document.addEventListener('DOMContentLoaded', () => {
  let submitted = false, viewSelected = false, before = false, finished = false, clockAdvanced = false;
  const root = document.documentElement;
  const inspect = () => {
    const countdown = document.querySelector('.quota-card[data-quota="five_hour"] .zenmux-countdown');
    const reset = countdown && countdown.closest('.quota-card').querySelector('.reset-time');
    if (!before && countdown && reset && (!document.getElementById('zenmux-usage-results') || viewSelected)) {
      before = true;
      root.dataset.zenmuxBeforeVisible = String(!countdown.hidden && !reset.hidden && getComputedStyle(countdown).display !== 'none' && getComputedStyle(reset).display !== 'none');
      root.dataset.zenmuxPath = countdown.closest('#quota-grid-zenmux') ? 'individual' : countdown.closest('.provider-card[data-provider="zenmux"] .provider-kpis') ? 'aggregate' : 'unexpected';
    }
    if (before && !clockAdvanced && document.readyState === 'complete' && countdown && reset) {
      clockAdvanced = true;
      fixtureNow = Date.parse(countdown.dataset.resetAt);
    }
    const expiredWithoutNodes = before && clockAdvanced && !countdown && !reset &&
      document.querySelector(root.dataset.zenmuxPath === 'individual' ? '#quota-grid-zenmux .quota-card[data-quota="five_hour"]' : '.provider-card[data-provider="zenmux"] .provider-kpis .quota-card[data-quota="five_hour"]');
    if (before && !finished && ((countdown && reset && countdown.hidden && reset.hidden) || expiredWithoutNodes)) {
      finished = true;
      root.dataset.zenmuxAfterHidden = String(expiredWithoutNodes || (getComputedStyle(countdown).display === 'none' && getComputedStyle(reset).display === 'none'));
      const ids = [...document.querySelectorAll('[id]')].map(el => el.id);
      root.dataset.zenmuxDuplicateIds = String(ids.length - new Set(ids).size);
      root.dataset.zenmuxCountdownIds = String(document.querySelectorAll('.zenmux-countdown[id]').length);
    }
    const results = document.getElementById('zenmux-usage-results');
    if (results && !submitted && results.textContent.includes('sample/month-model')) {
      submitted = true;
      const controls = [...document.querySelectorAll('label')];
      const labeled = name => controls.find(label => label.textContent.trim().startsWith(name))?.querySelector('input, select');
      const dimension = labeled('Period');
      dimension.value = 'BIZ_DT'; dimension.dispatchEvent(new Event('change'));
      labeled('Day').value = '2026-09-03';
      labeled('Billing').value = 'subscription';
      [...document.querySelectorAll('button')].find(button => button.textContent.trim() === 'Show usage').click();
    }
    if (results && submitted && !viewSelected && results.textContent.includes('sample/day-model')) {
      const label = [...results.querySelectorAll('label')].find(el => el.textContent.trim().startsWith('View'));
      const view = label && label.querySelector('select');
      if (view) {
        viewSelected = true;
        view.value = 'interval'; view.dispatchEvent(new Event('change', {bubbles:true}));
        [...results.querySelectorAll('button')].find(button => button.textContent.trim() === 'Show endpoints').click();
      }
    }
  };
  new MutationObserver(inspect).observe(document.documentElement, {subtree:true, childList:true, attributes:true});
  window.addEventListener('load', inspect);
  inspect();
});
</script></head>`, 1)
		}
		// The test never fetches external fonts or CDN scripts. Their availability
		// is unrelated to the local dashboard and can delay DOMContentLoaded.
		if dir := os.Getenv("ZENMUX_BROWSER_ARTIFACT_DIR"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "served-dashboard.html"), []byte(body), 0600)
		}
		for _, line := range strings.Split(body, "\n") {
			if !strings.Contains(line, "https://fonts.") && !strings.Contains(line, "https://cdn.jsdelivr.net") {
				_, _ = w.Write([]byte(line + "\n"))
			}
		}
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		body, err := staticFS.ReadFile("static/sw.js")
		if err != nil {
			http.Error(w, "asset unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/api/current", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		h.Current(rec, r)
		t.Logf("current response status: %d", rec.Code)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
	mux.HandleFunc("/api/zenmux/usage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("dimension") == "BIZ_DT" && r.URL.Query().Get("time") == "20260903" && r.URL.Query().Get("bill_type") == "subscription" && r.URL.Query().Get("model") == "sample/day-model" {
			_, _ = w.Write([]byte(`{"fetchedAt":"2026-09-03T13:01:00Z","data":{"endpointUsages":[{"endpointSlug":"sample-endpoint","requestCounts":"2","tokens":"0","medianLatency":"320","medianThroughput":null}]}}`))
			return
		}
		if r.URL.Query().Get("dimension") == "BIZ_DT" && r.URL.Query().Get("time") == "20260903" && r.URL.Query().Get("bill_type") == "subscription" {
			_, _ = w.Write([]byte(`{"fetchedAt":"2026-09-03T13:00:00Z","data":{"requestCounts":"2","tokensTotal":"0","tokensPrompt":null,"tokensCompletion":"0","tokensByModel":[{"bizTime":"2026090312","modelSlug":"sample/day-model","requestCounts":"2","tokens":"0"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"fetchedAt":"2026-09-03T13:00:00Z","data":{"requestCounts":"1","tokensTotal":"9","tokensPrompt":"5","tokensCompletion":"4","tokensByModel":[{"bizTime":"20260903","modelSlug":"sample/month-model","requestCounts":"1","tokens":"9"}]}}`))
	})
	mux.HandleFunc("/api/history", h.History)
	mux.HandleFunc("/api/providers/status", h.ProvidersStatus)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	staticDir, err := fs.Sub(staticFS, "static")
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticDir))))
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Run("submissions", func(t *testing.T) {
		if os.Getenv("ZENMUX_PLAYWRIGHT_MODULE") == "" {
			t.Skip("isolated Playwright module not configured")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "testdata/zenmux_submissions.cjs", server.URL, browser)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("submission browser scenarios: %v\n%s", err, out)
		}
		t.Logf("submission browser scenarios:\n%s", out)
	})
	if runner := os.Getenv("ZENMUX_BROWSER_RUNNER"); runner != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", runner, server.URL, os.Getenv("ZENMUX_BROWSER_ARTIFACT_DIR"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic browser runner failed: %v\n%s", err, out)
		}
		t.Logf("synthetic browser runner: %s", out)
		return
	}
	render := func(t *testing.T, provider string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-renderer-backgrounding", "--disable-background-timer-throttling", "--run-all-compositor-stages-before-draw", "--no-first-run", "--user-data-dir="+t.TempDir(), "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost, EXCLUDE 127.0.0.1", "--enable-begin-frame-control", "--virtual-time-budget=10000", "--dump-dom", server.URL+"/?provider="+provider)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s browser failed: %v", provider, err)
		}
		return out
	}
	checkCountdown := func(t *testing.T, provider, dom string) {
		t.Helper()
		for _, wanted := range []string{
			`data-zenmux-path="` + provider + `"`,
			`data-zenmux-before-visible="true"`,
			`data-zenmux-after-hidden="true"`,
			`data-zenmux-duplicate-ids="0"`,
			`data-zenmux-countdown-ids="0"`,
		} {
			if !strings.Contains(dom, wanted) {
				t.Errorf("%s countdown missing %s", provider, wanted)
			}
		}
	}
	t.Run("rendered usage", func(t *testing.T) {
		out := render(t, "zenmux")
		dom := string(out)
		mu.Lock()
		t.Logf("browser fixture requested paths: %v", seen)
		mu.Unlock()
		for _, wanted := range []string{"5-Hour Flows", "0.0%", "Usage unavailable", "34,560 flows", "Subscription expires:", "Model usage (aggregated)", "sample/day-model", "2026-09-03 12:00", "sample-endpoint"} {
			if !strings.Contains(dom, wanted) {
				t.Errorf("rendered dashboard missing %q", wanted)
			}
		}
		for _, unwanted := range []string{"Personal account aggregates by time bucket", "All billing combines", "Account usage fetched at", "model/time buckets", "Bucket times are Zenmux business times", "For the selected period and billing filter", "Requests: 2 | Tokens:"} {
			if strings.Contains(dom, unwanted) {
				t.Errorf("rendered dashboard contains unwanted text %q", unwanted)
			}
		}
		start := strings.Index(dom, `id="zenmux-plan-info"`)
		if start >= 0 {
			end := strings.Index(dom[start:], "</p>")
			if end < 0 || strings.Contains(dom[start:start+end], "Reset at") {
				t.Error("subscription expiry mislabeled as a reset")
			}
		}
		if dir := os.Getenv("ZENMUX_BROWSER_ARTIFACT_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "browser-dom.html"), out, 0600); err != nil {
				t.Error(err)
			}
		}
	})
	t.Run("individual countdown", func(t *testing.T) {
		checkCountdown(t, "individual", string(render(t, "zenmux")))
	})
	t.Run("aggregate countdown", func(t *testing.T) {
		out := render(t, "both")
		checkCountdown(t, "aggregate", string(out))
		if dir := os.Getenv("ZENMUX_BROWSER_ARTIFACT_DIR"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "browser-aggregate-dom.html"), out, 0600); err != nil {
				t.Error(err)
			}
		}
	})
}
