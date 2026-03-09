//go:build menubar && darwin

package menubar

import (
	"fmt"
	"log/slog"
	"math"
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
	cfg *Config
}

func runCompanion(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	quitOnce = sync.Once{}
	quitFn = nil

	controller := &trayController{cfg: cfg}
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
		_ = browser.OpenURL(c.menubarURL())
	})

	openItem := systray.AddMenuItem("Open Menubar", "Open the local menubar page")
	refreshItem := systray.AddMenuItem("Refresh Status", "Refresh the current menubar status")
	dashboardItem := systray.AddMenuItem("Open Dashboard", "Open the local onWatch dashboard")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Menubar", "Quit the menubar companion")

	quitFn = func() {
		systray.Quit()
	}

	c.refreshStatus()
	logger.Info("Menubar ready and visible")

	go c.watchMenu(openItem, refreshItem, dashboardItem, quitItem)
	go c.refreshLoop()
}

func (c *trayController) onExit() {
	quitFn = nil
	slog.Default().Info("Menubar shutting down")
}

func (c *trayController) watchMenu(openItem, refreshItem, dashboardItem, quitItem *systray.MenuItem) {
	for {
		select {
		case <-openItem.ClickedCh:
			_ = browser.OpenURL(c.menubarURL())
		case <-refreshItem.ClickedCh:
			c.refreshStatus()
		case <-dashboardItem.ClickedCh:
			_ = browser.OpenURL(c.dashboardURL())
		case <-quitItem.ClickedCh:
			_ = stopCompanion()
			return
		}
	}
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

	title := trayTitle(snapshot)
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

func trayTitle(snapshot *Snapshot) string {
	if snapshot == nil || snapshot.Aggregate.ProviderCount == 0 {
		return "onWatch"
	}
	return fmt.Sprintf("%d%%", int(math.Round(snapshot.Aggregate.HighestPercent)))
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
