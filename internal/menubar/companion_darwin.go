//go:build menubar && darwin

package menubar

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"fyne.io/systray"
	"github.com/pkg/browser"
	"github.com/wailsapp/wails/v2"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	macoptions "github.com/wailsapp/wails/v2/pkg/options/mac"
)

type appBridge struct {
	ctx context.Context
	cfg *Config
}

func (a *appBridge) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *appBridge) GetSnapshot() (*Snapshot, error) {
	if a.cfg == nil || a.cfg.SnapshotProvider == nil {
		return nil, errors.New("snapshot provider not configured")
	}
	return a.cfg.SnapshotProvider()
}

func (a *appBridge) GetSettings() (*Settings, error) {
	if a.cfg == nil {
		return DefaultSettings(), nil
	}
	settings := &Settings{
		Enabled:         a.cfg.Enabled,
		DefaultView:     a.cfg.DefaultView,
		RefreshSeconds:  a.cfg.RefreshSeconds,
		ProvidersOrder:  append([]string(nil), a.cfg.ProvidersOrder...),
		WarningPercent:  a.cfg.WarningPercent,
		CriticalPercent: a.cfg.CriticalPercent,
	}
	return settings.Normalize(), nil
}

func (a *appBridge) Refresh() (*Snapshot, error) {
	return a.GetSnapshot()
}

func (a *appBridge) OpenExternal(rawURL string) error {
	return browser.OpenURL(rawURL)
}

var (
	quitOnce sync.Once
	quitFn   func()
)

func runCompanion(cfg *Config) error {
	assets, err := FrontendSubFS()
	if err != nil {
		return err
	}
	quitOnce = sync.Once{}
	quitFn = nil
	app := &appBridge{cfg: cfg}
	showWindowCh := make(chan struct{}, 1)
	refreshCh := make(chan struct{}, 1)

	go initTray(cfg.Port, showWindowCh, refreshCh)

	return wails.Run(&options.App{
		Title:             "onWatch Menubar",
		Width:             widthForView(cfg.DefaultView),
		Height:            heightForView(cfg.DefaultView),
		MinWidth:          240,
		MinHeight:         120,
		MaxWidth:          420,
		MaxHeight:         720,
		Frameless:         true,
		AlwaysOnTop:       true,
		StartHidden:       true,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
		Mac: &macoptions.Options{
			TitleBar: &macoptions.TitleBar{
				HideTitleBar: true,
				HideToolbarSeparator: true,
				UseToolbar: false,
			},
			Appearance: macoptions.NSAppearanceNameDarkAqua,
		},
		OnDomReady: func(ctx context.Context) {
			quitFn = func() {
				wailsruntime.Quit(ctx)
			}
			go func() {
				for {
					select {
					case <-showWindowCh:
						wailsruntime.Show(ctx)
					case <-refreshCh:
						wailsruntime.WindowReload(ctx)
					}
				}
			}()
		},
		OnShutdown: func(ctx context.Context) {
			quitFn = nil
			systray.Quit()
		},
	})
}

func stopCompanion() error {
	quitOnce.Do(func() {
		if quitFn != nil {
			quitFn()
		}
		systray.Quit()
	})
	return nil
}

func initTray(port int, showWindowCh chan<- struct{}, refreshCh chan<- struct{}) {
	systray.Run(func() {
		templateIcon, regularIcon := trayIcons()
		if len(templateIcon) > 0 && len(regularIcon) > 0 {
			systray.SetTemplateIcon(templateIcon, regularIcon)
		}
		systray.SetTitle("onWatch")
		systray.SetTooltip("onWatch menubar companion")

		openItem := systray.AddMenuItem("Show Snapshot", "Show the menubar snapshot window")
		refreshItem := systray.AddMenuItem("Refresh Snapshot", "Refresh the menubar snapshot")
		dashboardItem := systray.AddMenuItem("Open Dashboard", "Open the local onWatch dashboard")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit Menubar", "Quit the menubar companion")

		go func() {
			for {
				select {
				case <-openItem.ClickedCh:
					select {
					case showWindowCh <- struct{}{}:
					default:
					}
				case <-refreshItem.ClickedCh:
					select {
					case refreshCh <- struct{}{}:
					default:
					}
				case <-dashboardItem.ClickedCh:
					_ = browser.OpenURL(fmt.Sprintf("http://localhost:%d", port))
				case <-quitItem.ClickedCh:
					_ = stopCompanion()
					return
				}
			}
		}()
	}, func() {})
}

func widthForView(view ViewType) int {
	switch view {
	case ViewMinimal:
		return 240
	case ViewDetailed:
		return 400
	default:
		return 320
	}
}

func heightForView(view ViewType) int {
	switch view {
	case ViewMinimal:
		return 160
	case ViewDetailed:
		return 620
	default:
		return 420
	}
}
