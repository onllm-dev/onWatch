package menubar

import (
	"encoding/json"
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed frontend/index.html frontend/menubar.css frontend/menubar.js
var frontendFS embed.FS

// FrontendAsset returns a named frontend asset.
func FrontendAsset(name string) ([]byte, error) {
	return frontendFS.ReadFile("frontend/" + name)
}

// FrontendSubFS exposes the embedded frontend directory.
func FrontendSubFS() (fs.FS, error) {
	return fs.Sub(frontendFS, "frontend")
}

// HTML returns the embedded index document.
func HTML() (string, error) {
	data, err := FrontendAsset("index.html")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// InlineHTML renders the menubar HTML with inline CSS and JS for the browser test page.
func InlineHTML(view ViewType, settings *Settings) (string, error) {
	indexHTML, err := HTML()
	if err != nil {
		return "", err
	}
	css, err := FrontendAsset("menubar.css")
	if err != nil {
		return "", err
	}
	js, err := FrontendAsset("menubar.js")
	if err != nil {
		return "", err
	}
	normalized := settings.Normalize()
	normalized.DefaultView = view
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("menubar.InlineHTML: %w", err)
	}
	bootstrap := fmt.Sprintf(`<script>window.__ONWATCH_MENUBAR_BRIDGE__={mode:"browser",view:%q,settings:%s};</script>`, view, payload)
	indexHTML = strings.Replace(indexHTML, `<link rel="stylesheet" href="menubar.css">`, "<style>"+string(css)+"</style>", 1)
	indexHTML = strings.Replace(indexHTML, `<script src="menubar.js"></script>`, bootstrap+"<script>"+string(js)+"</script>", 1)
	return indexHTML, nil
}
