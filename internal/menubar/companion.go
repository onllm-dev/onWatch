//go:build menubar && (darwin || linux || windows)

package menubar

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/pkg/browser"
)

var (
	quitOnce sync.Once
	quitFn   func()
)

// providerMenuPoolSize bounds the per-provider rows in the tray menu. Rows
// are created up front (systray appends new items at the bottom) and shown
// or hidden as the snapshot changes.
const providerMenuPoolSize = 24

type trayController struct {
	cfg     *Config
	popover menubarPopover

	menuMu         sync.Mutex
	providerItems  []*systray.MenuItem
	providerStatus map[*systray.MenuItem]string

	mistralMu      sync.RWMutex
	mistralEnabled bool
	grantFlow      browserGrantFlow

	// refreshWorker serializes snapshot fetches, caches the last good snapshot
	// and runs every refresh on one goroutine. See tray_refresh.go.
	refresh *refreshWorker

	// cachedSettings and lastVisual are the last successfully applied tray
	// preferences and visual. They are read and written only by the refresh
	// worker's apply callback, which is the only goroutine that runs a refresh,
	// so they need no separate lock. On a failed preferences fetch the
	// controller reuses them instead of DefaultSettings, so a daemon outage
	// cannot blank the title or badge.
	cachedSettings *Settings
	lastVisual     trayVisual
}

func (c *trayController) setMistralEnabled(enabled bool) {
	c.mistralMu.Lock()
	c.mistralEnabled = enabled
	c.mistralMu.Unlock()
}

func (c *trayController) isMistralEnabled() bool {
	c.mistralMu.RLock()
	defer c.mistralMu.RUnlock()
	return c.mistralEnabled
}

func runCompanion(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	quitOnce = sync.Once{}
	quitFn = nil

	controller := &trayController{cfg: cfg}
	controller.refresh = newRefreshWorker(controller.fetchSnapshot, controller.applyRefresh)
	stop := make(chan struct{})
	defer close(stop)
	// One worker serves every refresh for the process lifetime: the startup
	// fetch, menu clicks, daemon refresh requests and the periodic tick all
	// publish to the same channel, so they run one at a time in order.
	go controller.refresh.RunWorker(stop)
	go watchRefreshRequests(stop, cfg.TestMode, controller.refresh.Request)

	prepareTrayProcess()
	slog.Default().Debug("Initializing systray")
	systray.Run(controller.onReady, controller.onExit)
	return nil
}

func stopCompanion() error {
	quitOnce.Do(func() {
		if quitFn != nil {
			quitFn()
			return
		}
		systray.Quit()
	})
	return nil
}

func (c *trayController) onReady() {
	logger := slog.Default()
	logger.Info("Systray initialized, setting icon")

	setupTrayIcon()
	setTrayTooltip("onWatch")
	systray.SetOnTapped(func() {
		c.toggleMenubar()
	})

	if popover, err := newMenubarPopover(menubarPopoverWidth, menubarPopoverHeight); err != nil {
		logger.Warn("native menubar host unavailable, using browser fallback", "error", err)
	} else {
		c.popover = popover
		if host, ok := popover.(interface{ SetBrowserGrantHandler(func() string) }); ok {
			host.SetBrowserGrantHandler(c.grantBrowserAccess)
		}
		// Warm the WebView so the first tray click does not flash a blank page
		// while /menubar navigates. Subsequent opens reuse the loaded document.
		if err := popover.Preload(c.menubarURL()); err != nil {
			logger.Debug("menubar popover preload failed", "error", err)
		}
	}

	// Provider rows first so they stay above the actions.
	c.providerItems = make([]*systray.MenuItem, 0, providerMenuPoolSize)
	for i := 0; i < providerMenuPoolSize; i++ {
		item := systray.AddMenuItem("", "Open the quick view")
		item.Hide()
		c.providerItems = append(c.providerItems, item)
	}
	systray.AddSeparator()
	quickViewItem := systray.AddMenuItem("Open Quick View", "Show the onWatch quick view")
	dashboardItem := systray.AddMenuItem("Open Dashboard", "Open the local onWatch dashboard")
	refreshItem := systray.AddMenuItem("Refresh Now", "Fetch the latest quota data")
	// Only offered while a browser data directory is actually unreadable. The
	// daemon cannot present this itself: it has no user interface, and the
	// grant is only created when a person chooses the folder.
	grantItem := systray.AddMenuItem("Grant Browser Access...", "Allow onWatch to read browser cookies for Mistral")
	grantItem.Hide()
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Menubar", "Quit the menubar companion")

	quitFn = func() {
		systray.Quit()
	}

	// The first refresh can block up to the fetch timeout; the worker runs it
	// off the systray thread so the menu lays out immediately (a blocking first
	// refresh delayed the first menu layout on Linux).
	c.refresh.Request()
	logger.Info("Menubar ready and visible")

	go c.watchMenu(quickViewItem, dashboardItem, refreshItem, quitItem)
	go c.watchBrowserAccess(grantItem)
	go c.watchProviderItems()
	go c.refreshLoop()
}

func (c *trayController) onExit() {
	if c.popover != nil {
		c.popover.Destroy()
		c.popover = nil
	}
	quitFn = nil
	slog.Default().Info("Menubar shutting down")
}

// watchBrowserAccess shows the grant action only while a browser data
// directory exists but cannot be read, and re-checks after each attempt so the
// item disappears once access has been granted.
func (c *trayController) grantBrowserAccess() string {
	if !c.isMistralEnabled() {
		return "unavailable"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "unavailable"
	}
	return c.grantFlow.run(func() string { return blockedBrowserRoot(browserDataRoots(home)) },
		func(path string) (bool, string) {
			return requestFolderAccess(path, "Select this folder to let onWatch read your browser cookies for Mistral. onWatch reads only Mistral session cookies from it.")
		}, func(path string) error { _, err := os.ReadDir(path); return err }, c.cfg.MistralRetry)
}

func (c *trayController) watchBrowserAccess(grantItem *systray.MenuItem) {
	logger := slog.Default()
	previous := "\x00"
	refresh := func() string {
		if !c.isMistralEnabled() {
			grantItem.Hide()
			return ""
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		blocked := blockedBrowserRoot(browserDataRoots(home))
		if blocked == "" {
			grantItem.Hide()
		} else {
			grantItem.Show()
		}
		if blocked != previous {
			previous = blocked
			logger.Info("Browser access check", "blocked", filepath.Base(blocked), "actionVisible", blocked != "")
		}
		return blocked
	}
	blocked := refresh()
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			blocked = refresh()
		case _, ok := <-grantItem.ClickedCh:
			if !ok {
				return
			}
			if blocked == "" {
				if blocked = refresh(); blocked == "" {
					continue
				}
			}
			logger.Info("Browser access request finished", "result", c.grantBrowserAccess())
			blocked = refresh()
		}
	}
}

func (c *trayController) watchMenu(quickViewItem, dashboardItem, refreshItem, quitItem *systray.MenuItem) {
	for {
		select {
		case <-quickViewItem.ClickedCh:
			c.showMenubar()
		case <-dashboardItem.ClickedCh:
			_ = browser.OpenURL(c.dashboardURL())
		case <-refreshItem.ClickedCh:
			// The worker runs the fetch on its own goroutine, so a slow daemon
			// cannot block the other menu clicks systray hands over.
			c.refresh.Request()
		case <-quitItem.ClickedCh:
			_ = stopCompanion()
			return
		}
	}
}

func (c *trayController) watchProviderItems() {
	for _, item := range c.providerItems {
		go func(item *systray.MenuItem) {
			for range item.ClickedCh {
				c.showMenubar()
			}
		}(item)
	}
}

func (c *trayController) updateMenu(lines []MenuLine) {
	c.menuMu.Lock()
	defer c.menuMu.Unlock()
	if c.providerStatus == nil {
		c.providerStatus = make(map[*systray.MenuItem]string, len(c.providerItems))
	}
	for i, item := range c.providerItems {
		if i < len(lines) {
			item.SetTitle(lines[i].Text)
			// Re-sending an unchanged icon would rebuild the menu bitmap on
			// every refresh.
			if status, ok := c.providerStatus[item]; !ok || status != lines[i].Status {
				setMenuLineStatus(item, lines[i].Status)
				c.providerStatus[item] = lines[i].Status
			}
			item.Show()
			continue
		}
		item.Hide()
	}
}

func (c *trayController) toggleMenubar() {
	url := c.menubarURL()
	if c.popover != nil {
		if err := c.popover.ToggleURL(url); err == nil {
			return
		} else {
			slog.Default().Warn("failed to toggle native menubar host, opening browser fallback", "error", err)
		}
	}
	_ = browser.OpenURL(url)
}

func (c *trayController) showMenubar() {
	url := c.menubarURL()
	if c.popover != nil {
		if err := c.popover.ShowURL(url); err == nil {
			return
		} else {
			slog.Default().Warn("failed to show native menubar host, opening browser fallback", "error", err)
		}
	}
	_ = browser.OpenURL(url)
}

// refreshLoop publishes a periodic refresh. The tick and the on-demand
// requests share the worker's buffer-1 channel, so a tick that lands during a
// run coalesces into the same follow-up run instead of stacking.
func (c *trayController) refreshLoop() {
	interval := time.Duration(normalizeRefreshSeconds(c.cfg.RefreshSeconds)) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		c.refresh.Request()
	}
}

// fetchSnapshot is the worker's fetch step. A controller without a provider
// (tests, or a partial config) yields no data, and applyRefresh renders the
// offline state, so a nil function is never called.
func (c *trayController) fetchSnapshot() (*Snapshot, error) {
	if c == nil || c.cfg == nil || c.cfg.SnapshotProvider == nil {
		return nil, nil
	}
	return c.cfg.SnapshotProvider()
}

// applyRefresh is the worker's apply step: it fetches preferences, builds the
// plan and updates the tray. It runs on the single refresh worker goroutine,
// so it can touch the cached settings and last visual without extra locking.
func (c *trayController) applyRefresh(snapshot *Snapshot, stale bool, age time.Duration, fetchErr error) {
	logger := slog.Default()
	if c == nil || c.cfg == nil || c.cfg.SnapshotProvider == nil {
		updateTrayVisual(trayVisual{Title: "onWatch", Text: "-", Status: trayStatusOffline})
		setTrayTooltip("onWatch menubar companion")
		return
	}

	if stale {
		// Keep the last known numbers. A failure may recolor the icon and
		// update the tooltip, but must not wipe the title or the menu.
		logger.Warn("failed to refresh menubar snapshot, keeping last known values", "error", fetchErr)
	} else if fetchErr != nil {
		logger.Error("failed to refresh menubar snapshot", "error", fetchErr)
	}

	// Preferences are fetched even on a stale summary so a recovered daemon
	// picks up changed selections. A failed fetch returns nil and the plan
	// falls back to the last good preferences, never to DefaultSettings while
	// numbers are cached.
	var freshSettings *Settings
	if snapshot != nil {
		settings, err := c.fetchPreferences()
		if err != nil {
			logger.Debug("failed to refresh menubar preferences, keeping last known", "error", err)
		} else {
			freshSettings = settings
		}
	}

	plan := planTrayRefresh(snapshot, stale, age, freshSettings, c.cachedSettings, c.lastVisual, trayTooltipLimit)
	if plan.FreshSettings {
		c.cachedSettings = plan.Settings
	}
	if plan.Offline {
		updateTrayVisual(plan.Visual)
		setTrayTooltip(plan.Tooltip)
		c.updateMenu(nil)
		return
	}
	c.setMistralEnabled(plan.Snapshot.MistralEnabled)
	updateTrayVisual(plan.Visual)
	c.lastVisual = plan.Visual
	setTrayTooltip(plan.Tooltip)
	c.updateMenu(plan.MenuLines)
	logger.Debug("Tray icon set successfully", "title", plan.Visual.Title, "badge", plan.Visual.Text, "status", plan.Visual.Status, "stale", plan.Stale)
}

func (c *trayController) menubarURL() string {
	port := 9211
	if c != nil && c.cfg != nil && c.cfg.Port > 0 {
		port = c.cfg.Port
	}
	return fmt.Sprintf("http://localhost:%d/menubar", port)
}

func (c *trayController) dashboardURL() string {
	port := 9211
	if c != nil && c.cfg != nil && c.cfg.Port > 0 {
		port = c.cfg.Port
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

func (c *trayController) preferencesURL() string {
	port := 9211
	if c != nil && c.cfg != nil && c.cfg.Port > 0 {
		port = c.cfg.Port
	}
	return fmt.Sprintf("http://localhost:%d/api/menubar/preferences", port)
}

func (c *trayController) fetchPreferences() (*Settings, error) {
	req, err := http.NewRequest(http.MethodGet, c.preferencesURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("preferences request failed: %s", resp.Status)
	}
	var settings Settings
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, err
	}
	return settings.Normalize(), nil
}

func normalizeRefreshSeconds(value int) int {
	if value < 10 {
		return 60
	}
	return value
}
