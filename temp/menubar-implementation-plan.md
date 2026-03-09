# onWatch Menubar Implementation Plan

Version: 1.0
Created: 2026-03-09
Status: Planning Complete

---

## Executive Summary

Add a rich, interactive menubar to onWatch for macOS with:
- Two binary variants: `onwatch` (full) and `onwatch-lite` (minimal)
- Three preset views: Minimal, Standard, Detailed
- Cross-provider aggregation
- Browser-testable via Playwright
- Shared settings persistence

---

## Architecture

### Binary Strategy

| Binary | Platform | Menubar | Size | Build Tag |
|--------|----------|---------|------|-----------|
| onwatch | darwin-arm64 | Yes | ~25-30MB | menubar |
| onwatch | darwin-amd64 | Yes | ~25-30MB | menubar |
| onwatch-lite | darwin-arm64 | No | ~14MB | !menubar |
| onwatch-lite | darwin-amd64 | No | ~14MB | !menubar |
| onwatch | windows-amd64 | No | ~14MB | !menubar |
| onwatch | linux-amd64 | No | ~14MB | !menubar |

### Code Structure

```
internal/
├── menubar/
│   ├── menubar.go              # Build tag: menubar - Wails init, systray
│   ├── menubar_stub.go         # Build tag: !menubar - No-op stubs
│   ├── config.go               # Shared types (no build tag)
│   ├── views/
│   │   ├── minimal.go          # Compact view component
│   │   ├── standard.go         # Default view component
│   │   └── detailed.go         # Full view component
│   ├── components/
│   │   ├── provider_card.go    # Single provider display
│   │   ├── aggregate_bar.go    # Cross-provider summary
│   │   ├── sparkline.go        # Trend mini-chart
│   │   └── footer.go           # GitHub, Support, onllm.dev links
│   └── frontend/               # Wails embedded web UI
│       ├── index.html
│       ├── menubar.js
│       ├── menubar.css
│       └── assets/
│           └── icon.png
├── web/
│   ├── handlers.go             # Add /api/capabilities, /api/menubar/test
│   ├── static/
│   │   └── app.js              # Add menubar settings conditional
│   └── templates/
│       └── settings.html       # Add menubar section
```

### Testing Strategy: Browser-Based Playwright

The menubar UI is web-based (Wails). To enable Playwright testing:

1. Add endpoint: `GET /api/menubar/test?view={minimal|standard|detailed}`
2. This endpoint renders the menubar HTML in a full page (not popover)
3. Playwright navigates to this endpoint and validates UI
4. Same code renders in both test endpoint and actual menubar popover
5. Endpoint only available when `ONWATCH_TEST_MODE=1` env var is set

```
Test Flow:
  1. Start onwatch with ONWATCH_TEST_MODE=1
  2. Playwright navigates to localhost:PORT/api/menubar/test?view=standard
  3. Playwright validates:
     - Provider cards render
     - Percentages display correctly
     - Colors match thresholds
     - Footer links present
     - Click handlers work
  4. Repeat for all three views
```

---

## Design Specifications

### Visual Style (onclean inspiration)

```
design_tokens:
  colors:
    background: #1a1a2e (dark blue-gray)
    surface: #16213e (slightly lighter)
    primary: #0f3460 (accent blue)
    success: #4ade80 (green for healthy)
    warning: #fbbf24 (yellow for warning)
    critical: #ef4444 (red for critical)
    text_primary: #e2e8f0
    text_secondary: #94a3b8
    border: #334155

  typography:
    font_family: -apple-system, BlinkMacSystemFont, "SF Pro", system-ui
    font_size_xs: 10px
    font_size_sm: 12px
    font_size_base: 14px
    font_size_lg: 16px
    font_weight_normal: 400
    font_weight_medium: 500
    font_weight_bold: 600

  spacing:
    xs: 4px
    sm: 8px
    md: 12px
    lg: 16px
    xl: 24px

  border_radius:
    sm: 4px
    md: 8px
    lg: 12px

  shadows:
    popover: 0 4px 24px rgba(0,0,0,0.4)
```

### Popover Dimensions

```
view_sizes:
  minimal:
    width: 240px
    height: 120px

  standard:
    width: 320px
    height: 400px

  detailed:
    width: 400px
    height: 600px
```

### Footer Component

```
footer:
  height: 32px
  background: slightly darker than surface
  border_top: 1px solid border color

  links[3]:
    - icon: GitHub logo (16x16)
      url: https://github.com/onllm-dev/onwatch
      tooltip: "View on GitHub"

    - icon: Question mark / help icon (16x16)
      url: https://github.com/onllm-dev/onwatch/issues
      tooltip: "Get Support"

    - icon: onLLM logo or globe (16x16)
      url: https://onllm.dev
      tooltip: "onLLM.dev"

  layout: Centered, icons spaced evenly with subtle hover effect
  hover: opacity 0.7 -> 1.0, slight scale
```

### Provider Card Component

```
provider_card:
  height: 64px (collapsed), expandable
  padding: 12px

  layout:
    row_1:
      - Provider icon (16x16)
      - Provider name (bold)
      - Status dot (green/yellow/red)

    row_2:
      - Progress bar (full width)
      - Percentage text (right aligned)

    row_3 (expanded):
      - "Used: X / Y requests"
      - "Resets: Mar 15, 2026 at 9:00 AM"
      - Mini sparkline (7-day trend)

  colors:
    progress_bar_bg: #334155
    progress_fill: gradient based on percentage
      0-70%: success (green)
      70-90%: warning (yellow)
      90-100%: critical (red)
```

### Aggregate Header

```
aggregate_header:
  height: 80px

  layout:
    title: "onWatch" (left)
    status_badge: "All Good" | "1 Warning" | "2 Critical" (right)

    aggregate_bar:
      - Stacked horizontal bar showing all providers
      - Each segment colored by provider + opacity by health
      - Hover shows provider name + percentage

    last_updated: "Updated 30s ago" (subtle, bottom right)
```

---

## Phase 1: Foundation

### 1.1 Create Build Tag Scaffold

**Files to create/modify:**

| File | Action | Description |
|------|--------|-------------|
| internal/menubar/menubar.go | Create | Main menubar init (build tag: menubar) |
| internal/menubar/menubar_stub.go | Create | No-op stubs (build tag: !menubar) |
| internal/menubar/config.go | Create | Shared types, settings struct |
| app.sh | Modify | Add build targets for full/lite |

**Step 1.1.1: Create menubar directory structure**

```bash
mkdir -p internal/menubar/views
mkdir -p internal/menubar/components
mkdir -p internal/menubar/frontend/assets
```

Definition of Done:
- [ ] Directory structure exists
- [ ] `ls internal/menubar/` shows views/, components/, frontend/

**Step 1.1.2: Create menubar_stub.go**

```go
// internal/menubar/menubar_stub.go
//go:build !menubar

package menubar

// Init is a no-op when menubar is not compiled in
func Init(cfg *Config) error { return nil }

// Stop is a no-op when menubar is not compiled in
func Stop() error { return nil }

// IsSupported returns false when menubar is not compiled in
func IsSupported() bool { return false }

// IsRunning returns false when menubar is not compiled in
func IsRunning() bool { return false }
```

Definition of Done:
- [ ] File exists at internal/menubar/menubar_stub.go
- [ ] Build tag `//go:build !menubar` is first line
- [ ] All four functions defined: Init, Stop, IsSupported, IsRunning
- [ ] `go build ./...` succeeds (uses stub by default)

**Step 1.1.3: Create config.go (shared types)**

```go
// internal/menubar/config.go
package menubar

// Config holds menubar configuration
type Config struct {
    Port            int      // Dashboard port to connect to
    Enabled         bool     // Whether menubar should start
    DefaultView     string   // "minimal", "standard", "detailed"
    RefreshSeconds  int      // Polling interval
    ProvidersOrder  []string // Display order
    WarningPercent  int      // Yellow threshold
    CriticalPercent int      // Red threshold
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
    return &Config{
        Port:            8080,
        Enabled:         true,
        DefaultView:     "standard",
        RefreshSeconds:  60,
        ProvidersOrder:  nil, // nil = alphabetical
        WarningPercent:  70,
        CriticalPercent: 90,
    }
}

// ViewType enum
type ViewType string

const (
    ViewMinimal  ViewType = "minimal"
    ViewStandard ViewType = "standard"
    ViewDetailed ViewType = "detailed"
)
```

Definition of Done:
- [ ] File exists at internal/menubar/config.go
- [ ] No build tag (shared between both variants)
- [ ] Config struct has all fields
- [ ] DefaultConfig() returns valid defaults
- [ ] ViewType constants defined
- [ ] `go build ./...` succeeds

**Step 1.1.4: Create placeholder menubar.go**

```go
// internal/menubar/menubar.go
//go:build menubar

package menubar

import (
    "fmt"
)

var running bool

// Init initializes the menubar (placeholder)
func Init(cfg *Config) error {
    if cfg == nil {
        cfg = DefaultConfig()
    }
    // TODO: Phase 2 - Wails initialization
    fmt.Println("[menubar] Initialized with view:", cfg.DefaultView)
    running = true
    return nil
}

// Stop shuts down the menubar
func Stop() error {
    running = false
    return nil
}

// IsSupported returns true on darwin with menubar build tag
func IsSupported() bool { return true }

// IsRunning returns current state
func IsRunning() bool { return running }
```

Definition of Done:
- [ ] File exists at internal/menubar/menubar.go
- [ ] Build tag `//go:build menubar` is first line
- [ ] All four functions implemented
- [ ] `go build -tags menubar ./...` succeeds

**Step 1.1.5: Update app.sh build script**

Add new build targets:

```bash
# In app.sh, add to build section:

build_darwin_full() {
    echo "Building darwin full (with menubar)..."
    CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -tags menubar -o dist/onwatch-darwin-arm64 .
    CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -tags menubar -o dist/onwatch-darwin-amd64 .
}

build_darwin_lite() {
    echo "Building darwin lite (no menubar)..."
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/onwatch-lite-darwin-arm64 .
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/onwatch-lite-darwin-amd64 .
}

# Update --release to call both
```

Definition of Done:
- [ ] `./app.sh --build-darwin-full` produces onwatch-darwin-arm64
- [ ] `./app.sh --build-darwin-lite` produces onwatch-lite-darwin-arm64
- [ ] Full binary is larger than lite binary
- [ ] `./dist/onwatch-darwin-arm64 --version` works
- [ ] `./dist/onwatch-lite-darwin-arm64 --version` works

---

### 1.2 Add Capabilities API Endpoint

**Step 1.2.1: Add /api/capabilities endpoint**

Modify `internal/web/handlers.go`:

```go
// Capabilities returns runtime capabilities
func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
    caps := map[string]interface{}{
        "version":           version,
        "menubar_supported": menubar.IsSupported(),
        "menubar_running":   menubar.IsRunning(),
        "variant":           getVariant(), // "full" or "lite"
        "platform":          runtime.GOOS,
    }
    respondJSON(w, http.StatusOK, caps)
}

func getVariant() string {
    if menubar.IsSupported() {
        return "full"
    }
    return "lite"
}
```

Add route in `internal/web/server.go`:
```go
mux.HandleFunc("/api/capabilities", handler.Capabilities)
```

Definition of Done:
- [ ] Endpoint exists at /api/capabilities
- [ ] Returns JSON with menubar_supported field
- [ ] Full binary returns `menubar_supported: true`
- [ ] Lite binary returns `menubar_supported: false`
- [ ] Test: `curl localhost:8080/api/capabilities | jq .`

**Step 1.2.2: Add menubar test endpoint (for Playwright)**

```go
// MenubarTest renders menubar UI in full page for testing
// Only available when ONWATCH_TEST_MODE=1
func (h *Handler) MenubarTest(w http.ResponseWriter, r *http.Request) {
    if os.Getenv("ONWATCH_TEST_MODE") != "1" {
        http.NotFound(w, r)
        return
    }

    view := r.URL.Query().Get("view")
    if view == "" {
        view = "standard"
    }

    // Render menubar HTML as full page
    h.renderMenubarTestPage(w, view)
}
```

Add route:
```go
mux.HandleFunc("/api/menubar/test", handler.MenubarTest)
```

Definition of Done:
- [ ] Endpoint exists at /api/menubar/test
- [ ] Returns 404 when ONWATCH_TEST_MODE is not set
- [ ] Returns HTML when ONWATCH_TEST_MODE=1
- [ ] Accepts ?view=minimal|standard|detailed query param
- [ ] Test: `ONWATCH_TEST_MODE=1 ./onwatch & curl localhost:8080/api/menubar/test?view=standard`

---

### 1.3 Add Menubar Settings to Store

**Step 1.3.1: Update settings schema**

Modify `internal/store/store.go` to handle menubar settings:

```go
// MenubarSettings holds menubar-specific settings
type MenubarSettings struct {
    Enabled         bool     `json:"enabled"`
    DefaultView     string   `json:"default_view"`
    RefreshSeconds  int      `json:"refresh_seconds"`
    ProvidersOrder  []string `json:"providers_order"`
    WarningPercent  int      `json:"warning_percent"`
    CriticalPercent int      `json:"critical_percent"`
}

// Default values
func DefaultMenubarSettings() *MenubarSettings {
    return &MenubarSettings{
        Enabled:         true,
        DefaultView:     "standard",
        RefreshSeconds:  60,
        ProvidersOrder:  nil,
        WarningPercent:  70,
        CriticalPercent: 90,
    }
}
```

Definition of Done:
- [ ] MenubarSettings struct defined
- [ ] GetMenubarSettings() method on Store
- [ ] SetMenubarSettings() method on Store
- [ ] Settings persist across restarts
- [ ] Test: Set setting, restart, verify setting retained

---

### 1.4 Phase 1 Integration Tests

**Step 1.4.1: Create menubar package tests**

Create `internal/menubar/menubar_test.go`:

```go
package menubar

import "testing"

func TestDefaultConfig(t *testing.T) {
    cfg := DefaultConfig()
    if cfg.DefaultView != "standard" {
        t.Errorf("expected standard, got %s", cfg.DefaultView)
    }
    if cfg.RefreshSeconds != 60 {
        t.Errorf("expected 60, got %d", cfg.RefreshSeconds)
    }
}

func TestIsSupported(t *testing.T) {
    // This test behaves differently based on build tag
    supported := IsSupported()
    // Just verify it doesn't panic
    t.Logf("IsSupported: %v", supported)
}
```

Definition of Done:
- [ ] Test file exists
- [ ] `go test ./internal/menubar/...` passes
- [ ] `go test -tags menubar ./internal/menubar/...` passes

**Step 1.4.2: Create capabilities endpoint test**

Add to `internal/web/handlers_test.go`:

```go
func TestCapabilities(t *testing.T) {
    h := setupTestHandler(t)
    req := httptest.NewRequest("GET", "/api/capabilities", nil)
    w := httptest.NewRecorder()

    h.Capabilities(w, req)

    if w.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", w.Code)
    }

    var caps map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &caps)

    if _, ok := caps["menubar_supported"]; !ok {
        t.Error("missing menubar_supported field")
    }
    if _, ok := caps["version"]; !ok {
        t.Error("missing version field")
    }
}
```

Definition of Done:
- [ ] Test exists in handlers_test.go
- [ ] `go test ./internal/web/...` passes
- [ ] Test validates response structure

---

## Phase 2: Menubar Core

### 2.1 Wails Integration

**Step 2.1.1: Add Wails dependency**

```bash
go get github.com/wailsapp/wails/v2
```

Definition of Done:
- [ ] go.mod includes wails/v2
- [ ] `go mod tidy` succeeds
- [ ] No import errors

**Step 2.1.2: Implement menubar.go with Wails**

Replace placeholder with full implementation:

```go
// internal/menubar/menubar.go
//go:build menubar

package menubar

import (
    "context"
    "embed"
    "fmt"
    "net/http"
    "time"

    "github.com/wailsapp/wails/v2"
    "github.com/wailsapp/wails/v2/pkg/options"
    "github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed frontend/*
var assets embed.FS

var (
    app     *wails.App
    running bool
    cfg     *Config
)

// App exposes methods to frontend
type App struct {
    ctx context.Context
    cfg *Config
}

func (a *App) GetSummary() (map[string]interface{}, error) {
    // Fetch from local API
    resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/summary?provider=both", a.cfg.Port))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    // Parse and return
    // ...
}

func (a *App) GetConfig() *Config {
    return a.cfg
}

// Init initializes the menubar with Wails
func Init(config *Config) error {
    if config == nil {
        config = DefaultConfig()
    }
    cfg = config

    app := &App{cfg: config}

    err := wails.Run(&options.App{
        Title:  "onWatch",
        Width:  320,
        Height: 400,
        AssetServer: &assetserver.Options{
            Assets: assets,
        },
        OnStartup: func(ctx context.Context) {
            app.ctx = ctx
        },
        Bind: []interface{}{
            app,
        },
        // Menubar-specific options
        Frameless:        true,
        StartHidden:      true,
        HideWindowOnClose: true,
    })

    if err != nil {
        return err
    }

    running = true
    return nil
}
```

Definition of Done:
- [ ] File compiles with `-tags menubar`
- [ ] Wails app initializes without error
- [ ] App.GetSummary() returns data from local API
- [ ] Window appears when triggered

**Step 2.1.3: Create frontend HTML/JS/CSS**

Create `internal/menubar/frontend/index.html`:

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>onWatch</title>
    <link rel="stylesheet" href="menubar.css">
</head>
<body>
    <div id="app">
        <header id="header"></header>
        <main id="content"></main>
        <footer id="footer">
            <a href="https://github.com/onllm-dev/onwatch" target="_blank" title="GitHub">
                <svg><!-- GitHub icon --></svg>
            </a>
            <a href="https://github.com/onllm-dev/onwatch/issues" target="_blank" title="Support">
                <svg><!-- Help icon --></svg>
            </a>
            <a href="https://onllm.dev" target="_blank" title="onLLM.dev">
                <svg><!-- Globe icon --></svg>
            </a>
        </footer>
    </div>
    <script src="menubar.js"></script>
</body>
</html>
```

Create `internal/menubar/frontend/menubar.css`:

```css
:root {
    --bg: #1a1a2e;
    --surface: #16213e;
    --primary: #0f3460;
    --success: #4ade80;
    --warning: #fbbf24;
    --critical: #ef4444;
    --text-primary: #e2e8f0;
    --text-secondary: #94a3b8;
    --border: #334155;
}

* {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
}

body {
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro", system-ui, sans-serif;
    background: var(--bg);
    color: var(--text-primary);
    font-size: 14px;
}

#app {
    display: flex;
    flex-direction: column;
    height: 100vh;
}

#header {
    padding: 16px;
    border-bottom: 1px solid var(--border);
}

#content {
    flex: 1;
    overflow-y: auto;
    padding: 12px;
}

#footer {
    height: 32px;
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 24px;
    background: rgba(0,0,0,0.2);
    border-top: 1px solid var(--border);
}

#footer a {
    color: var(--text-secondary);
    opacity: 0.7;
    transition: opacity 0.2s, transform 0.2s;
}

#footer a:hover {
    opacity: 1;
    transform: scale(1.1);
}

#footer svg {
    width: 16px;
    height: 16px;
}

/* Provider card */
.provider-card {
    background: var(--surface);
    border-radius: 8px;
    padding: 12px;
    margin-bottom: 8px;
}

.provider-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
}

.provider-name {
    font-weight: 600;
}

.status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
}

.status-dot.healthy { background: var(--success); }
.status-dot.warning { background: var(--warning); }
.status-dot.critical { background: var(--critical); }

.progress-bar {
    height: 6px;
    background: var(--border);
    border-radius: 3px;
    overflow: hidden;
}

.progress-fill {
    height: 100%;
    border-radius: 3px;
    transition: width 0.3s ease;
}

.progress-fill.healthy { background: var(--success); }
.progress-fill.warning { background: var(--warning); }
.progress-fill.critical { background: var(--critical); }
```

Create `internal/menubar/frontend/menubar.js`:

```javascript
// Wails runtime
const { GetSummary, GetConfig } = window.go.menubar.App;

let currentView = 'standard';
let refreshInterval = null;

async function init() {
    const config = await GetConfig();
    currentView = config.DefaultView;
    await refresh();
    startPolling(config.RefreshSeconds);
}

async function refresh() {
    try {
        const summary = await GetSummary();
        renderView(summary);
    } catch (err) {
        renderError(err);
    }
}

function startPolling(seconds) {
    if (refreshInterval) clearInterval(refreshInterval);
    refreshInterval = setInterval(refresh, seconds * 1000);
}

function renderView(summary) {
    const content = document.getElementById('content');
    switch (currentView) {
        case 'minimal':
            content.innerHTML = renderMinimalView(summary);
            break;
        case 'standard':
            content.innerHTML = renderStandardView(summary);
            break;
        case 'detailed':
            content.innerHTML = renderDetailedView(summary);
            break;
    }
}

function renderStandardView(summary) {
    // Render provider cards
    let html = '';
    for (const [provider, data] of Object.entries(summary)) {
        const percent = calculatePercent(data);
        const status = getStatus(percent);
        html += `
            <div class="provider-card">
                <div class="provider-header">
                    <span class="provider-name">${provider}</span>
                    <span class="status-dot ${status}"></span>
                </div>
                <div class="progress-bar">
                    <div class="progress-fill ${status}" style="width: ${percent}%"></div>
                </div>
                <div class="provider-percent">${percent.toFixed(1)}%</div>
            </div>
        `;
    }
    return html;
}

function calculatePercent(data) {
    if (!data.limit || data.limit === 0) return 0;
    return (data.used / data.limit) * 100;
}

function getStatus(percent) {
    if (percent >= 90) return 'critical';
    if (percent >= 70) return 'warning';
    return 'healthy';
}

// Initialize on load
document.addEventListener('DOMContentLoaded', init);
```

Definition of Done:
- [ ] All three files exist in internal/menubar/frontend/
- [ ] CSS follows design tokens from spec
- [ ] Footer has all three links (GitHub, Support, onllm.dev)
- [ ] JS fetches data via Wails bindings
- [ ] Polling works at configured interval
- [ ] `go build -tags menubar ./...` embeds assets

---

### 2.2 System Tray Integration

**Step 2.2.1: Add fyne-io/systray for tray icon**

```bash
go get fyne.io/systray
```

**Step 2.2.2: Integrate systray with Wails**

Update menubar.go to show tray icon that opens Wails window:

```go
import "fyne.io/systray"

func initSystray() {
    systray.Run(onSystrayReady, onSystrayExit)
}

func onSystrayReady() {
    systray.SetIcon(iconBytes) // Embed icon
    systray.SetTitle("onWatch")
    systray.SetTooltip("AI Quota Monitor")

    mOpen := systray.AddMenuItem("Open", "Open onWatch")
    mRefresh := systray.AddMenuItem("Refresh", "Refresh now")
    systray.AddSeparator()
    mQuit := systray.AddMenuItem("Quit", "Quit onWatch")

    go func() {
        for {
            select {
            case <-mOpen.ClickedCh:
                showWindow()
            case <-mRefresh.ClickedCh:
                triggerRefresh()
            case <-mQuit.ClickedCh:
                systray.Quit()
            }
        }
    }()
}
```

Definition of Done:
- [ ] Tray icon appears in macOS menu bar
- [ ] Click opens popover window
- [ ] Right-click shows context menu
- [ ] Quit menu item exits cleanly
- [ ] Icon displays correctly on both light/dark mode

---

### 2.3 Main.go Integration

**Step 2.3.1: Call menubar.Init() from main.go**

Modify `main.go` to start menubar when supported:

```go
import "github.com/onllm-dev/onwatch/v2/internal/menubar"

func run() error {
    // ... existing setup ...

    // Start menubar if supported and enabled
    if menubar.IsSupported() {
        settings, _ := store.GetMenubarSettings()
        if settings == nil || settings.Enabled {
            go func() {
                cfg := &menubar.Config{
                    Port:           port,
                    DefaultView:    settings.DefaultView,
                    RefreshSeconds: settings.RefreshSeconds,
                }
                if err := menubar.Init(cfg); err != nil {
                    slog.Error("menubar init failed", "error", err)
                }
            }()
        }
    }

    // ... rest of main ...
}
```

Definition of Done:
- [ ] Full binary auto-starts menubar on launch
- [ ] Lite binary skips menubar (no error)
- [ ] Menubar settings from DB are respected
- [ ] `./onwatch` shows tray icon on macOS

---

## Phase 3: Dashboard Integration

### 3.1 Conditional Menubar Settings Section

**Step 3.1.1: Update settings.html template**

Add menubar section that only shows when supported:

```html
<!-- In settings.html, add after notifications section -->
<div id="menubar-settings" class="settings-section" style="display: none;">
    <h3>Menubar</h3>

    <div class="setting-row">
        <label>Default View</label>
        <select id="menubar-default-view">
            <option value="minimal">Minimal</option>
            <option value="standard">Standard</option>
            <option value="detailed">Detailed</option>
        </select>
    </div>

    <div class="setting-row">
        <label>Refresh Interval</label>
        <select id="menubar-refresh">
            <option value="30">30 seconds</option>
            <option value="60">1 minute</option>
            <option value="120">2 minutes</option>
            <option value="300">5 minutes</option>
        </select>
    </div>

    <div class="setting-row">
        <label>Warning Threshold</label>
        <input type="range" id="menubar-warning" min="50" max="90" value="70">
        <span id="menubar-warning-value">70%</span>
    </div>

    <div class="setting-row">
        <label>Critical Threshold</label>
        <input type="range" id="menubar-critical" min="80" max="100" value="90">
        <span id="menubar-critical-value">90%</span>
    </div>

    <div class="setting-row">
        <h4>Visible Providers</h4>
        <div id="menubar-providers-list">
            <!-- Dynamically populated -->
        </div>
    </div>

    <button id="save-menubar-settings" class="btn-primary">Save Menubar Settings</button>
</div>

<!-- Banner for lite version -->
<div id="menubar-upgrade-banner" class="upgrade-banner" style="display: none;">
    <p>Menubar is available in the full version of onWatch.</p>
    <p>Run: <code>curl -fsSL https://onllm.dev/install.sh | sh -s -- --full</code></p>
</div>
```

Definition of Done:
- [ ] Menubar settings section exists in template
- [ ] All controls present (view, refresh, thresholds, providers)
- [ ] Upgrade banner exists for lite version

**Step 3.1.2: Update app.js for conditional display**

```javascript
// In app.js, add:

async function checkCapabilities() {
    const resp = await fetch('/api/capabilities');
    const caps = await resp.json();

    const menubarSettings = document.getElementById('menubar-settings');
    const upgradeBanner = document.getElementById('menubar-upgrade-banner');

    if (caps.menubar_supported) {
        menubarSettings.style.display = 'block';
        upgradeBanner.style.display = 'none';
        loadMenubarSettings();
    } else {
        menubarSettings.style.display = 'none';
        upgradeBanner.style.display = 'block';
    }
}

async function loadMenubarSettings() {
    const resp = await fetch('/api/settings');
    const settings = await resp.json();

    if (settings.menubar) {
        document.getElementById('menubar-default-view').value = settings.menubar.default_view;
        document.getElementById('menubar-refresh').value = settings.menubar.refresh_seconds;
        document.getElementById('menubar-warning').value = settings.menubar.warning_percent;
        document.getElementById('menubar-critical').value = settings.menubar.critical_percent;
        // Update display values
        updateThresholdDisplays();
    }
}

async function saveMenubarSettings() {
    const settings = {
        menubar: {
            default_view: document.getElementById('menubar-default-view').value,
            refresh_seconds: parseInt(document.getElementById('menubar-refresh').value),
            warning_percent: parseInt(document.getElementById('menubar-warning').value),
            critical_percent: parseInt(document.getElementById('menubar-critical').value),
        }
    };

    await fetch('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settings)
    });

    showToast('Menubar settings saved');
}

// Call on page load
document.addEventListener('DOMContentLoaded', checkCapabilities);
```

Definition of Done:
- [ ] /api/capabilities is called on page load
- [ ] Full version shows menubar settings
- [ ] Lite version shows upgrade banner
- [ ] Settings load from server
- [ ] Save button persists to server
- [ ] Toast confirms save

**Step 3.1.3: Add menubar settings API endpoints**

Modify handlers.go:

```go
// In Settings handler, add menubar section:
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
    if r.Method == "GET" {
        // ... existing code ...

        // Add menubar settings
        menubarSettings, _ := h.store.GetMenubarSettings()
        response["menubar"] = menubarSettings

        respondJSON(w, http.StatusOK, response)
        return
    }

    if r.Method == "POST" {
        // ... existing code ...

        // Handle menubar settings
        if mb, ok := body["menubar"].(map[string]interface{}); ok {
            settings := &store.MenubarSettings{
                DefaultView:     mb["default_view"].(string),
                RefreshSeconds:  int(mb["refresh_seconds"].(float64)),
                WarningPercent:  int(mb["warning_percent"].(float64)),
                CriticalPercent: int(mb["critical_percent"].(float64)),
            }
            h.store.SetMenubarSettings(settings)
        }

        respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
    }
}
```

Definition of Done:
- [ ] GET /api/settings includes menubar section
- [ ] POST /api/settings accepts menubar section
- [ ] Settings persist to SQLite
- [ ] Test: Save settings, restart, verify persisted

---

### 3.2 Provider Visibility and Order

**Step 3.2.1: Add drag-and-drop provider ordering**

Add to settings.html:

```html
<div class="setting-row">
    <h4>Provider Order (drag to reorder)</h4>
    <ul id="menubar-provider-order" class="sortable-list">
        <!-- Dynamically populated with drag handles -->
    </ul>
</div>
```

Add to app.js:

```javascript
function initProviderSorting() {
    const list = document.getElementById('menubar-provider-order');

    // Simple drag-and-drop (no external library)
    let draggedItem = null;

    list.addEventListener('dragstart', (e) => {
        draggedItem = e.target;
        e.target.classList.add('dragging');
    });

    list.addEventListener('dragend', (e) => {
        e.target.classList.remove('dragging');
        saveProviderOrder();
    });

    list.addEventListener('dragover', (e) => {
        e.preventDefault();
        const afterElement = getDragAfterElement(list, e.clientY);
        if (afterElement == null) {
            list.appendChild(draggedItem);
        } else {
            list.insertBefore(draggedItem, afterElement);
        }
    });
}

function saveProviderOrder() {
    const items = document.querySelectorAll('#menubar-provider-order li');
    const order = Array.from(items).map(item => item.dataset.provider);
    // Save to settings
}
```

Definition of Done:
- [ ] Provider list is drag-and-drop sortable
- [ ] Order persists on save
- [ ] Menubar respects saved order
- [ ] No external JS library required

---

## Phase 4: Polish and Testing

### 4.1 Implement All Three Views

**Step 4.1.1: Minimal view**

```javascript
function renderMinimalView(summary) {
    const aggregate = calculateAggregate(summary);
    const status = getStatus(aggregate.percent);

    return `
        <div class="minimal-view">
            <div class="aggregate-circle ${status}">
                <span class="aggregate-percent">${aggregate.percent.toFixed(0)}%</span>
            </div>
            <div class="aggregate-label">${aggregate.providers} providers</div>
            <div class="aggregate-status">${getStatusLabel(status)}</div>
        </div>
    `;
}
```

CSS for minimal:
```css
.minimal-view {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    height: 100%;
    text-align: center;
}

.aggregate-circle {
    width: 64px;
    height: 64px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 20px;
    font-weight: 600;
}

.aggregate-circle.healthy {
    background: rgba(74, 222, 128, 0.2);
    border: 2px solid var(--success);
}

.aggregate-circle.warning {
    background: rgba(251, 191, 36, 0.2);
    border: 2px solid var(--warning);
}

.aggregate-circle.critical {
    background: rgba(239, 68, 68, 0.2);
    border: 2px solid var(--critical);
}
```

Definition of Done:
- [ ] Minimal view renders aggregate percentage
- [ ] Color coding matches status
- [ ] Fits in 240x120px
- [ ] Playwright test passes

**Step 4.1.2: Detailed view**

```javascript
function renderDetailedView(summary) {
    let html = '<div class="detailed-view">';

    // Aggregate chart
    html += renderAggregateChart(summary);

    // Provider cards with sparklines
    for (const [provider, data] of Object.entries(summary)) {
        html += renderDetailedProviderCard(provider, data);
    }

    html += '</div>';
    return html;
}

function renderDetailedProviderCard(provider, data) {
    const percent = calculatePercent(data);
    const status = getStatus(percent);

    return `
        <div class="provider-card detailed">
            <div class="provider-header">
                <span class="provider-name">${provider}</span>
                <span class="status-dot ${status}"></span>
            </div>
            <div class="progress-bar">
                <div class="progress-fill ${status}" style="width: ${percent}%"></div>
            </div>
            <div class="provider-stats">
                <span>Used: ${data.used} / ${data.limit}</span>
                <span>Resets: ${formatResetTime(data.resets_at)}</span>
            </div>
            <div class="sparkline" data-values="${data.history?.join(',') || ''}">
                <!-- SVG sparkline rendered here -->
            </div>
        </div>
    `;
}
```

Definition of Done:
- [ ] Detailed view shows all provider data
- [ ] Sparklines render 7-day history
- [ ] Reset times display in human-readable format
- [ ] Fits in 400x600px
- [ ] Playwright test passes

---

### 4.2 Playwright Test Suite

**Step 4.2.1: Create menubar test file**

Create `tests/e2e/test_menubar.py`:

```python
import pytest
import re
from playwright.sync_api import Page, expect
import subprocess
import time
import os

@pytest.fixture(scope="module")
def onwatch_server():
    """Start onwatch in test mode"""
    env = os.environ.copy()
    env["ONWATCH_TEST_MODE"] = "1"

    proc = subprocess.Popen(
        ["./onwatch"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE
    )
    time.sleep(2)  # Wait for startup
    yield proc
    proc.terminate()
    proc.wait()


class TestMenubarMinimalView:
    def test_renders_aggregate_circle(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=minimal")

        circle = page.locator(".aggregate-circle")
        expect(circle).to_be_visible()

    def test_shows_percentage(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=minimal")

        percent = page.locator(".aggregate-percent")
        expect(percent).to_have_text(re.compile(r"\d+%"))

    def test_has_correct_dimensions(self, page: Page, onwatch_server):
        page.set_viewport_size({"width": 240, "height": 120})
        page.goto("http://localhost:8080/api/menubar/test?view=minimal")

        # Verify no overflow
        body = page.locator("body")
        expect(body).to_have_css("overflow", "hidden")


class TestMenubarStandardView:
    def test_renders_provider_cards(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        cards = page.locator(".provider-card")
        expect(cards).to_have_count(greater_than=0)

    def test_progress_bars_visible(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        bars = page.locator(".progress-bar")
        expect(bars.first).to_be_visible()

    def test_status_dots_colored(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        dot = page.locator(".status-dot").first
        # Should have one of the status classes
        expect(dot).to_have_class(re.compile(r"healthy|warning|critical"))


class TestMenubarDetailedView:
    def test_renders_sparklines(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=detailed")

        sparklines = page.locator(".sparkline")
        expect(sparklines).to_have_count(greater_than=0)

    def test_shows_reset_times(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=detailed")

        resets = page.locator(".provider-stats")
        expect(resets.first).to_contain_text("Resets:")


class TestMenubarFooter:
    @pytest.mark.parametrize("view", ["minimal", "standard", "detailed"])
    def test_footer_visible_all_views(self, page: Page, onwatch_server, view):
        page.goto(f"http://localhost:8080/api/menubar/test?view={view}")

        footer = page.locator("#footer")
        expect(footer).to_be_visible()

    def test_github_link(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        github = page.locator("#footer a[href*='github.com']")
        expect(github).to_have_attribute("href", "https://github.com/onllm-dev/onwatch")

    def test_support_link(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        support = page.locator("#footer a[href*='issues']")
        expect(support).to_be_visible()

    def test_onllm_link(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        onllm = page.locator("#footer a[href*='onllm.dev']")
        expect(onllm).to_have_attribute("href", "https://onllm.dev")


class TestMenubarInteractivity:
    def test_provider_card_hover(self, page: Page, onwatch_server):
        page.goto("http://localhost:8080/api/menubar/test?view=standard")

        card = page.locator(".provider-card").first
        card.hover()
        # Verify hover state (could check for class change or visual)

    def test_view_switch(self, page: Page, onwatch_server):
        # Start with standard
        page.goto("http://localhost:8080/api/menubar/test?view=standard")
        expect(page.locator(".provider-card")).to_be_visible()

        # Switch to minimal
        page.goto("http://localhost:8080/api/menubar/test?view=minimal")
        expect(page.locator(".aggregate-circle")).to_be_visible()
```

Definition of Done:
- [ ] Test file exists at tests/e2e/test_menubar.py
- [ ] All test classes cover three views
- [ ] Footer tests verify all three links
- [ ] Tests pass: `pytest tests/e2e/test_menubar.py -v`

**Step 4.2.2: Add to CI workflow**

Update `.github/workflows/test.yml`:

```yaml
  menubar-tests:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Build full binary
        run: |
          CGO_ENABLED=1 go build -tags menubar -o onwatch .

      - name: Set up Python
        uses: actions/setup-python@v5
        with:
          python-version: '3.11'

      - name: Install Playwright
        run: |
          pip install pytest playwright
          playwright install chromium

      - name: Run menubar tests
        env:
          ONWATCH_TEST_MODE: "1"
        run: |
          ./onwatch &
          sleep 3
          pytest tests/e2e/test_menubar.py -v
```

Definition of Done:
- [ ] CI workflow includes menubar-tests job
- [ ] Job runs on macos-latest
- [ ] Playwright tests execute in CI
- [ ] Tests pass in CI

---

### 4.3 Installer Updates

**Step 4.3.1: Update install.sh**

```bash
#!/bin/bash
set -e

VERSION="${VERSION:-latest}"
VARIANT="${VARIANT:-full}"  # full or lite

# Parse args
while [[ $# -gt 0 ]]; do
    case $1 in
        --lite)
            VARIANT="lite"
            shift
            ;;
        --full)
            VARIANT="full"
            shift
            ;;
        --version)
            VERSION="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
esac

# Determine binary name
if [[ "$OS" == "darwin" && "$VARIANT" == "lite" ]]; then
    BINARY="onwatch-lite-darwin-${ARCH}"
elif [[ "$OS" == "darwin" ]]; then
    BINARY="onwatch-darwin-${ARCH}"
elif [[ "$OS" == "linux" ]]; then
    BINARY="onwatch-linux-${ARCH}"
else
    echo "Unsupported platform: $OS"
    exit 1
fi

# Download
DOWNLOAD_URL="https://github.com/onllm-dev/onwatch/releases/download/${VERSION}/${BINARY}"
echo "Downloading $BINARY..."
curl -fsSL -o /tmp/onwatch "$DOWNLOAD_URL"
chmod +x /tmp/onwatch

# Install
INSTALL_DIR="/usr/local/bin"
if [[ "$VARIANT" == "lite" ]]; then
    sudo mv /tmp/onwatch "$INSTALL_DIR/onwatch-lite"
    echo "Installed onwatch-lite to $INSTALL_DIR/onwatch-lite"
else
    sudo mv /tmp/onwatch "$INSTALL_DIR/onwatch"
    echo "Installed onwatch to $INSTALL_DIR/onwatch"
fi

echo "Done! Run 'onwatch --help' to get started."
```

Definition of Done:
- [ ] install.sh accepts --lite and --full flags
- [ ] Default is --full for macOS
- [ ] Correct binary downloaded for platform
- [ ] Binary placed in /usr/local/bin
- [ ] Test: `curl ... | sh -s -- --lite` works

---

### 4.4 Documentation

**Step 4.4.1: Update README**

Add menubar section to README.md:

```markdown
## Menubar (macOS)

The full version of onWatch (`onwatch`) includes a native macOS menubar app
that displays your AI quota status at a glance.

### Features

- **Three views**: Minimal, Standard, and Detailed
- **Real-time updates**: Configurable refresh interval
- **Cross-provider**: See all 7 providers in one place
- **Customizable**: Reorder providers, set thresholds

### Installation

```bash
# Full version with menubar (default)
curl -fsSL https://onllm.dev/install.sh | sh

# Lite version without menubar
curl -fsSL https://onllm.dev/install.sh | sh -s -- --lite
```

### Configuration

Menubar settings can be configured in the dashboard under Settings > Menubar,
or directly in the menubar popover.

| Setting | Default | Description |
|---------|---------|-------------|
| Default View | Standard | minimal, standard, or detailed |
| Refresh Interval | 60s | How often to poll for updates |
| Warning Threshold | 70% | Yellow status threshold |
| Critical Threshold | 90% | Red status threshold |
```

Definition of Done:
- [ ] README has Menubar section
- [ ] Installation commands documented
- [ ] Settings table complete
- [ ] Screenshots added (after implementation)

---

## Git Workflow

### Branch Strategy
- Feature branch: `feature/menubar`
- Base branch: `main`
- Merge strategy: PR with review

### Commit Convention
```
feat(menubar): <description>    # New feature
fix(menubar): <description>     # Bug fix
test(menubar): <description>    # Tests
docs(menubar): <description>    # Documentation
```

### Checkpoints
Push and request review at:
- End of Phase 1 (foundation complete)
- End of Phase 2 (menubar working)
- End of Phase 3 (dashboard integration)
- End of Phase 4 (ready for merge)

### PR Creation
After Phase 4, create PR:
```bash
gh pr create --title "feat: Add macOS menubar with three preset views" --body "$(cat <<'EOF'
## Summary
- Adds menubar support for macOS (full binary)
- Three views: minimal, standard, detailed
- Dashboard integration for settings
- Playwright test coverage

## Test Plan
- [ ] `go test -race ./...` passes
- [ ] `pytest tests/e2e/test_menubar.py` passes
- [ ] Manual test: tray icon appears, popover opens
- [ ] Manual test: settings persist across restart

## Binary Sizes
- onwatch (full): ~XX MB
- onwatch-lite: ~14 MB
EOF
)"
```

---

## Verification Checklist

### Phase 1 Complete When:
- [ ] `go build ./...` succeeds (uses stub)
- [ ] `go build -tags menubar ./...` succeeds
- [ ] `/api/capabilities` returns correct data
- [ ] `/api/menubar/test` returns 404 without ONWATCH_TEST_MODE
- [ ] `/api/menubar/test` returns HTML with ONWATCH_TEST_MODE=1
- [ ] Settings persist in SQLite
- [ ] All unit tests pass

### Phase 2 Complete When:
- [ ] Full binary shows tray icon on macOS
- [ ] Clicking tray icon opens popover
- [ ] Popover displays provider data
- [ ] Standard view renders correctly
- [ ] Footer shows all three links
- [ ] Refresh polling works
- [ ] Lite binary has no menubar code (verify with `strings` command)

### Phase 3 Complete When:
- [ ] Dashboard shows menubar settings (full version)
- [ ] Dashboard shows upgrade banner (lite version)
- [ ] Settings changes reflect in menubar
- [ ] Provider order drag-and-drop works
- [ ] Threshold sliders work

### Phase 4 Complete When:
- [ ] All three views implemented and visually correct
- [ ] Playwright tests pass locally
- [ ] Playwright tests pass in CI
- [ ] Installer supports --lite and --full flags
- [ ] Documentation complete
- [ ] Release includes all binary variants

---

## Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| Wails increases binary size beyond 30MB | Medium | Profile and strip unused features |
| CGO complicates CI cross-compilation | High | Use GitHub-hosted macOS runners |
| Playwright tests flaky in CI | Medium | Add retries, increase timeouts |
| Settings schema migration breaks existing installs | High | Add migration logic, test upgrade path |
| fyne-io/systray conflicts with Wails | Medium | Test integration early, have fallback plan |

---

## Skills for Executor

When implementing, use these skills:

| Phase | Skills |
|-------|--------|
| Phase 1 | feature-dev:feature-dev, verification-before-completion |
| Phase 2 | ui-ux-pro-max, frontend-design, feature-dev:feature-dev |
| Phase 3 | ui-ux-pro-max, frontend-implementation, verification-before-completion |
| Phase 4 | ui-ux-pro-max, systematic-debugging, commit-commands:commit-push-pr |

---

## Progress Tracking

Use this section to track implementation progress:

| Phase | Step | Status | Notes |
|-------|------|--------|-------|
| 1 | 1.1.1 Create directory structure | Complete | Added tracked `views/`, `components/`, and `frontend/assets/` scaffolding under `internal/menubar/`. |
| 1 | 1.1.2 Create menubar_stub.go | Complete | Implemented repo-aligned stub with `!menubar || !darwin` build tag. |
| 1 | 1.1.3 Create config.go | Complete | Added shared config, settings, snapshot contract, defaults, and normalization helpers. |
| 1 | 1.1.4 Create menubar.go placeholder | Complete | Implemented real darwin entrypoint in `menubar_darwin.go` plus companion runtime instead of a placeholder-only file. |
| 1 | 1.1.5 Update app.sh | Complete | Added `--build-darwin-full`, `--build-darwin-lite`, and release handling for macOS full/lite artifacts. |
| 1 | 1.2.1 Add /api/capabilities | Complete | Added `/api/capabilities` route and runtime variant/support reporting. |
| 1 | 1.2.2 Add /api/menubar/test | Complete | Added authenticated browser test page plus CSP override for inline bootstrap in test mode. |
| 1 | 1.3.1 Add MenubarSettings to store | Complete | Persisted menubar settings as a JSON blob under `settings.key="menubar"`. |
| 1 | 1.4.1 Create menubar package tests | Complete | Added config, normalization, inline HTML, and store round-trip tests. |
| 1 | 1.4.2 Create capabilities endpoint test | Complete | Added handler coverage for capabilities, settings, summary, and menubar test endpoints. |
| 2 | 2.1.1 Add Wails dependency | Complete | Added `github.com/wailsapp/wails/v2` and aligned builds with `desktop,production` tags. |
| 2 | 2.1.2 Implement menubar.go with Wails | Complete | Implemented Wails companion with same-binary hidden `menubar` mode and separate PID tracking. |
| 2 | 2.1.3 Create frontend HTML/JS/CSS | Complete | Added embedded frontend shell, multi-view rendering, footer links, and reduced-motion/focus handling. |
| 2 | 2.2.1 Add fyne-io/systray | Complete | Implemented tray support with `github.com/getlantern/systray` as the repo-aligned external tray library. |
| 2 | 2.2.2 Integrate systray with Wails | Complete | Added tray menu, generated tray icons, refresh/show actions, and clean quit handling. |
| 2 | 2.3.1 Call menubar.Init() from main.go | Complete | Added hidden `menubar` command, readiness-gated companion launch, and daemon shutdown cleanup. |
| 3 | 3.1.1 Update settings.html template | Complete | Added Menubar tab, full-build controls, provider ordering area, and lite upgrade banner. |
| 3 | 3.1.2 Update app.js for conditional display | Complete | Added capability loading, macOS conditional Menubar tab behavior, and menubar settings wiring. |
| 3 | 3.1.3 Add menubar settings API endpoints | Complete | Extended existing `GET/PUT /api/settings` flow with `menubar` round-tripping. |
| 3 | 3.2.1 Add drag-and-drop provider ordering | Complete | Added provider ordering UI with Codex per-account entries and persisted order handling. |
| 4 | 4.1.1 Minimal view | Complete | Added aggregate-only minimal view and browser coverage. |
| 4 | 4.1.2 Detailed view | Complete | Added expanded detailed view with trend rows and browser coverage. |
| 4 | 4.2.1 Create menubar test file | Complete | Added `tests/e2e/tests/test_menubar.py` and macOS-aware settings coverage. |
| 4 | 4.2.2 Add to CI workflow | Complete | Added macOS menubar CI job plus release workflow support for full/lite macOS artifacts. |
| 4 | 4.3.1 Update install.sh | Complete | Added `--full`, `--lite`, and `--version` installer options with macOS full-by-default behavior. |
| 4 | 4.4.1 Update README | Complete | Added menubar install/configuration/API docs; screenshots remain optional follow-up polish. |

Status Legend: Not started | In progress | Complete | Blocked
