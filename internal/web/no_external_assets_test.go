package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// externalAssetHosts are third-party origins the dashboard must never reach.
// Loading an asset from any of them discloses the viewer's IP address and
// User-Agent to that third party on every page view, which breaks the
// documented air-gapped operation and turns each dashboard load into a
// third-party data transfer the deployer never agreed to.
var externalAssetHosts = []string{
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"cdn.jsdelivr.net",
	"cdnjs.cloudflare.com",
	"unpkg.com",
	"googletagmanager.com",
	"google-analytics.com",
}

// TestTemplates_NoExternalAssetOrigins asserts every served template is
// self-contained.
func TestTemplates_NoExternalAssetOrigins(t *testing.T) {
	t.Parallel()

	entries, err := fs.Glob(templatesFS, "templates/*.html")
	if err != nil {
		t.Fatalf("glob templates: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no templates found - the embed directive or this glob is wrong")
	}

	for _, name := range entries {
		data, err := templatesFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(data)
		for _, host := range externalAssetHosts {
			if strings.Contains(body, host) {
				t.Errorf("%s references external origin %q - vendor the asset under static/vendor/ instead", name, host)
			}
		}
	}
}

// TestStaticAssets_NoExternalAssetOrigins covers the embedded static files,
// including the quick-view page and the service worker.
func TestStaticAssets_NoExternalAssetOrigins(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"static/app.js", "static/menubar.html", "static/sw.js", "static/style.css", "static/theme-init.js"} {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		body := string(data)
		for _, host := range externalAssetHosts {
			if strings.Contains(body, host) {
				t.Errorf("%s references external origin %q - vendor the asset under static/vendor/ instead", name, host)
			}
		}
	}
}

// TestVendoredAssets_AreEmbedded asserts the replacements for the former CDN
// assets ship inside the binary.
func TestVendoredAssets_AreEmbedded(t *testing.T) {
	t.Parallel()

	required := []string{
		"static/vendor/chart.umd.min.js",
		"static/vendor/chartjs-adapter-date-fns.bundle.min.js",
		"static/vendor/fonts.css",
		"static/vendor/fonts/ubuntu-400-latin.woff2",
		"static/vendor/fonts/ubuntu-500-latin.woff2",
		"static/vendor/fonts/ubuntu-700-latin.woff2",
		"static/vendor/fonts/jetbrains-mono-400-latin.woff2",
		"static/vendor/LICENSES.md",
	}
	for _, name := range required {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			t.Errorf("vendored asset %s is not embedded: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("vendored asset %s is empty", name)
		}
	}
}

// TestLayout_UsesVendoredAssets asserts the layout actually points at the
// vendored copies rather than simply having dropped the charts.
func TestLayout_UsesVendoredAssets(t *testing.T) {
	t.Parallel()

	data, err := templatesFS.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatalf("read layout.html: %v", err)
	}
	layout := string(data)
	for _, ref := range []string{
		"/static/vendor/chart.umd.min.js",
		"/static/vendor/chartjs-adapter-date-fns.bundle.min.js",
		"/static/vendor/fonts.css",
	} {
		if !strings.Contains(layout, ref) {
			t.Errorf("layout.html must load %s", ref)
		}
	}
}

// TestFontsCSS_ReferencesLocalFilesOnly asserts the vendored stylesheet points
// at bundled woff2 files and keeps the unicode-range subsetting.
func TestFontsCSS_ReferencesLocalFilesOnly(t *testing.T) {
	t.Parallel()

	data, err := staticFS.ReadFile("static/vendor/fonts.css")
	if err != nil {
		t.Fatalf("read vendor/fonts.css: %v", err)
	}
	css := string(data)
	if strings.Contains(css, "https://") || strings.Contains(css, "http://") {
		t.Error("vendor/fonts.css must not reference any absolute URL")
	}
	if !strings.Contains(css, "unicode-range:") {
		t.Error("vendor/fonts.css must keep unicode-range subsetting so latin-ext stays a separate download")
	}
	for _, fam := range []string{"Ubuntu", "JetBrains Mono"} {
		if !strings.Contains(css, fam) {
			t.Errorf("vendor/fonts.css must declare the %s family used by style.css", fam)
		}
	}
}

// TestSecurityHeaders_CSPHasNoExternalOrigins asserts the Content-Security-Policy
// no longer whitelists third-party hosts. The CSP is the enforcement backstop:
// even if a future template regressed, the browser would refuse the request.
func TestSecurityHeaders_CSPHasNoExternalOrigins(t *testing.T) {
	t.Parallel()

	handler := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	for _, host := range externalAssetHosts {
		if strings.Contains(csp, host) {
			t.Errorf("CSP still whitelists %q: %s", host, csp)
		}
	}
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"font-src 'self'",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP must contain %q, got: %s", directive, csp)
		}
	}
}
