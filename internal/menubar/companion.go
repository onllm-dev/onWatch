//go:build menubar && (darwin || linux || windows)

package menubar

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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

	menuMu        sync.Mutex
	providerItems []*systray.MenuItem
}

func runCompanion(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	quitOnce = sync.Once{}
	quitFn = nil

	controller := &trayController{cfg: cfg}
	stop := make(chan struct{})
	defer close(stop)
	go watchRefreshRequests(stop, cfg.TestMode, controller.refreshStatus)

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
	systray.SetTooltip("onWatch")
	systray.SetOnTapped(func() {
		c.toggleMenubar()
	})

	if popover, err := newMenubarPopover(menubarPopoverWidth, menubarPopoverHeight); err != nil {
		logger.Warn("native menubar host unavailable, using browser fallback", "error", err)
	} else {
		c.popover = popover
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
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Menubar", "Quit the menubar companion")

	quitFn = func() {
		systray.Quit()
	}

	c.refreshStatus()
	logger.Info("Menubar ready and visible")

	go c.watchMenu(quickViewItem, dashboardItem, refreshItem, quitItem)
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

func (c *trayController) watchMenu(quickViewItem, dashboardItem, refreshItem, quitItem *systray.MenuItem) {
	for {
		select {
		case <-quickViewItem.ClickedCh:
			c.showMenubar()
		case <-dashboardItem.ClickedCh:
			_ = browser.OpenURL(c.dashboardURL())
		case <-refreshItem.ClickedCh:
			c.refreshStatus()
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
	for i, item := range c.providerItems {
		if i < len(lines) {
			item.SetTitle(lines[i].Text)
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

func (c *trayController) refreshLoop() {
	interval := time.Duration(normalizeRefreshSeconds(c.cfg.RefreshSeconds)) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		c.refreshStatus()
	}
}

func (c *trayController) refreshStatus() {
	logger := slog.Default()
	if c == nil || c.cfg == nil || c.cfg.SnapshotProvider == nil {
		updateTrayVisual(trayVisual{Title: "onWatch", Text: "-", Status: trayStatusOffline})
		systray.SetTooltip("onWatch menubar companion")
		return
	}

	snapshot, err := c.cfg.SnapshotProvider()
	if err != nil {
		logger.Error("failed to refresh menubar snapshot", "error", err)
		snapshot = nil
	}
	if snapshot == nil {
		updateTrayVisual(trayVisual{Title: "--", Text: "-", Status: trayStatusOffline})
		systray.SetTooltip(TrayTooltipText(nil, trayTooltipLimit))
		c.updateMenu(nil)
		return
	}

	settings, err := c.fetchPreferences()
	if err != nil {
		logger.Debug("failed to refresh menubar preferences, using defaults", "error", err)
		settings = DefaultSettings()
	}
	segments := TraySegments(snapshot, settings)
	visual := trayVisual{
		Title:    TrayTitle(snapshot, settings),
		Text:     TrayIconText(segments, true),
		Status:   TrayBadgeStatus(snapshot, segments),
		Online:   true,
		IconOnly: settings.StatusDisplay.Mode == StatusDisplayIconOnly,
	}
	updateTrayVisual(visual)
	systray.SetTooltip(TrayTooltipText(snapshot, trayTooltipLimit))
	c.updateMenu(BuildMenuLines(snapshot))
	logger.Debug("Tray icon set successfully", "title", visual.Title, "badge", visual.Text, "status", visual.Status)
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
