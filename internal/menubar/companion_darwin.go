//go:build menubar && darwin

package menubar

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/pkg/browser"
)

var (
	quitOnce sync.Once
	quitFn   func()
)

type trayController struct {
	cfg     *Config
	popover menubarPopover
}

func runCompanion(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	quitOnce = sync.Once{}
	quitFn = nil

	controller := &trayController{cfg: cfg}
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, refreshCompanionSignal)
	defer signal.Stop(signalChan)
	go func() {
		for range signalChan {
			controller.refreshStatus()
		}
	}()

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

	templateIcon, regularIcon := trayIcons()
	if len(templateIcon) > 0 && len(regularIcon) > 0 {
		systray.SetTemplateIcon(templateIcon, regularIcon)
		logger.Debug("Tray icon set successfully")
	}

	systray.SetTooltip("onWatch menubar companion")
	systray.SetOnTapped(func() {
		c.toggleMenubar()
	})

	if popover, err := newMenubarPopover(menubarPopoverWidth, menubarPopoverHeight); err != nil {
		logger.Warn("native macOS menubar host unavailable, using browser fallback", "error", err)
	} else {
		c.popover = popover
	}

	dashboardItem := systray.AddMenuItem("Open Dashboard", "Open the local onWatch dashboard")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Menubar", "Quit the menubar companion")

	quitFn = func() {
		systray.Quit()
	}

	c.refreshStatus()
	logger.Info("Menubar ready and visible")

	go c.watchMenu(dashboardItem, quitItem)
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

func (c *trayController) watchMenu(dashboardItem, quitItem *systray.MenuItem) {
	for {
		select {
		case <-dashboardItem.ClickedCh:
			_ = browser.OpenURL(c.dashboardURL())
		case <-quitItem.ClickedCh:
			_ = stopCompanion()
			return
		}
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
		systray.SetTitle("onWatch")
		systray.SetTooltip("onWatch menubar companion")
		return
	}

	snapshot, err := c.cfg.SnapshotProvider()
	if err != nil {
		logger.Error("failed to refresh menubar snapshot", "error", err)
		systray.SetTitle("--")
		systray.SetTooltip("onWatch menubar companion unavailable")
		return
	}
	if snapshot == nil {
		systray.SetTitle("--")
		systray.SetTooltip("onWatch menubar companion unavailable")
		return
	}

	settings, err := c.fetchPreferences()
	if err != nil {
		logger.Debug("failed to refresh menubar preferences, using defaults", "error", err)
	}
	title := TrayTitle(snapshot, settings)
	tooltip := trayTooltip(snapshot)
	systray.SetTitle(title)
	systray.SetTooltip(tooltip)
	logger.Debug("Tray icon set successfully", "title", title)
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

func trayTooltip(snapshot *Snapshot) string {
	if snapshot == nil {
		return "onWatch menubar companion"
	}
	aggregate := snapshot.Aggregate
	if aggregate.ProviderCount == 0 {
		return "onWatch menubar companion: no provider data available"
	}
	return fmt.Sprintf(
		"onWatch menubar companion: %s across %d providers, updated %s",
		aggregate.Label,
		aggregate.ProviderCount,
		snapshot.UpdatedAgo,
	)
}

func normalizeRefreshSeconds(value int) int {
	if value < 10 {
		return 60
	}
	return value
}
