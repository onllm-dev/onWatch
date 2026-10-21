package web

import (
	"crypto/sha256"
	"fmt"
	"html/template"
)

// peakHoursAssetVersion fingerprints the shared peak/off-peak helper the same
// way the Mistral assets are versioned, so a local rebuild cannot leave an
// older cached copy in a browser.
var peakHoursAssetVersion = func() string {
	data, err := staticFS.ReadFile("static/peak-hours.js")
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}()

// peakHoursTemplateFuncs exposes the asset version to the dashboard layout.
var peakHoursTemplateFuncs = template.FuncMap{
	"peakHoursVersion": func() string { return peakHoursAssetVersion },
}
