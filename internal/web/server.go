package web

import (
	"compress/gzip"
	"context"
	"embed"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed all:static/*
var staticFS embed.FS

// Server wraps an HTTP server with graceful shutdown capabilities
type Server struct {
	httpServer *http.Server
	handler    *Handler
	logger     *slog.Logger
	port       int
}

// NewServer creates a new Server instance.
// passwordHash should be a SHA-256 hex hash of the admin password.
// basePath is the URL prefix for subdirectory hosting (e.g. "/onwatch"), empty for root.
func NewServer(port int, handler *Handler, logger *slog.Logger, username, passwordHash, host, basePath string) *Server {
	if port == 0 {
		port = 9211 // default port
	}
	if host == "" {
		host = "0.0.0.0" // default bind address
	}

	// Helper to prefix routes with base path
	bp := basePath // e.g. "" or "/onwatch"
	p := func(path string) string { return bp + path }

	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc(p("/"), handler.Dashboard)
	mux.HandleFunc(p("/menubar"), handler.MenubarPage)
	mux.HandleFunc(p("/settings"), handler.SettingsPage)
	mux.HandleFunc(p("/login"), handler.Login)
	mux.HandleFunc(p("/logout"), handler.Logout)
	mux.HandleFunc(p("/api/providers"), handler.Providers)
	mux.HandleFunc(p("/api/providers/status"), handler.ProvidersStatus)
	mux.HandleFunc(p("/api/providers/toggle"), handler.ToggleProvider)
	mux.HandleFunc(p("/api/providers/reload"), handler.ReloadProviders)
	mux.HandleFunc(p("/api/current"), handler.Current)
	mux.HandleFunc(p("/api/history"), handler.History)
	mux.HandleFunc(p("/api/cycles"), handler.Cycles)
	mux.HandleFunc(p("/api/summary"), handler.Summary)
	mux.HandleFunc(p("/api/capabilities"), handler.Capabilities)
	mux.HandleFunc(p("/api/menubar/summary"), handler.MenubarSummary)
	mux.HandleFunc(p("/api/menubar/preferences"), handler.MenubarPreferences)
	mux.HandleFunc(p("/api/menubar/refresh"), handler.MenubarRefresh)
	mux.HandleFunc(p("/api/menubar/test"), handler.MenubarTest)
	mux.HandleFunc(p("/api/sessions"), handler.Sessions)
	mux.HandleFunc(p("/api/insights"), handler.Insights)
	mux.HandleFunc(p("/api/settings"), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			handler.UpdateSettings(w, r)
		} else {
			handler.GetSettings(w, r)
		}
	})
	mux.HandleFunc(p("/api/settings/smtp/test"), handler.SMTPTest)
	mux.HandleFunc(p("/api/password"), handler.ChangePassword)
	mux.HandleFunc(p("/api/cycle-overview"), handler.CycleOverview)
	mux.HandleFunc(p("/api/logging-history"), handler.LoggingHistory)
	mux.HandleFunc(p("/api/update/check"), handler.CheckUpdate)
	mux.HandleFunc(p("/api/update/apply"), handler.ApplyUpdate)
	mux.HandleFunc(p("/api/push/vapid"), handler.PushVAPIDKey)
	mux.HandleFunc(p("/api/push/subscribe"), handler.PushSubscribe)
	mux.HandleFunc(p("/api/push/test"), handler.PushTest)
	mux.HandleFunc(p("/api/codex/profiles"), handler.CodexProfiles)
	mux.HandleFunc(p("/api/codex/usage"), handler.CodexUsage)
	mux.HandleFunc(p("/api/codex/accounts/usage"), handler.CodexAccountsUsage)
	mux.HandleFunc(p("/api/minimax/current"), handler.currentMiniMax)
	mux.HandleFunc(p("/api/minimax/history"), handler.historyMiniMax)
	mux.HandleFunc(p("/api/minimax/cycles"), handler.cyclesMiniMax)
	mux.HandleFunc(p("/api/minimax/insights"), func(w http.ResponseWriter, r *http.Request) {
		handler.insightsMiniMax(w, r, parseInsightsRange(r.URL.Query().Get("range")))
	})
	mux.HandleFunc(p("/api/minimax/accounts"), handler.MiniMaxAccounts)
	mux.HandleFunc(p("/api/minimax/accounts/usage"), handler.MiniMaxAccountsUsage)
	mux.HandleFunc(p("/api/api-integrations/current"), handler.APIIntegrationsCurrent)
	mux.HandleFunc(p("/api/api-integrations/history"), handler.APIIntegrationsHistory)
	mux.HandleFunc(p("/api/api-integrations/health"), handler.APIIntegrationsHealth)

	// System alerts (in-dashboard notifications)
	mux.HandleFunc(p("/api/alerts"), handler.SystemAlerts)
	mux.HandleFunc(p("/api/alerts/dismiss"), handler.DismissAlert)
	mux.HandleFunc(p("/api/alerts/dismiss-all"), handler.DismissAllAlerts)
	mux.HandleFunc(p("/api/alerts/simulate"), handler.SimulateAlert)

	// Service worker (served with base path scope)
	mux.HandleFunc(p("/sw.js"), func(w http.ResponseWriter, r *http.Request) {
		// Serve service worker with base path substituted for icon paths
		data, _ := staticFS.ReadFile("static/sw.js")
		content := strings.ReplaceAll(string(data), "/static/", bp+"/static/")
		if bp != "" {
			content = strings.ReplaceAll(content, "clients.openWindow('/')", "clients.openWindow('"+bp+"/')")
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", bp+"/")
		w.Write([]byte(content))
	})
	// PWA manifest (dynamically rewritten with base path)
	mux.HandleFunc(p("/manifest.json"), func(w http.ResponseWriter, r *http.Request) {
		data, _ := staticFS.ReadFile("static/manifest.json")
		content := string(data)
		if bp != "" {
			content = strings.ReplaceAll(content, `"start_url": "/"`, `"start_url": "`+bp+`/"`)
			content = strings.ReplaceAll(content, `"scope": "/"`, `"scope": "`+bp+`/"`)
			content = strings.ReplaceAll(content, `"/static/`, `"`+bp+`/static/`)
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write([]byte(content))
	})

	// Static files from embedded filesystem
	staticDir, _ := fs.Sub(staticFS, "static")
	staticHandler := http.FileServer(http.FS(staticDir))
	mux.Handle(p("/static/"), http.StripPrefix(p("/static/"), contentTypeHandler(staticHandler)))

	// Apply middleware chain: security headers -> gzip compression -> auth -> routes
	var finalHandler http.Handler = mux
	if username != "" && passwordHash != "" {
		sessions := NewSessionStore(username, passwordHash, handler.store)
		handler.sessions = sessions
		finalHandler = sessionAuthMiddlewareWithBasePath(sessions, bp, logger)(mux)
	}
	// Apply security headers and gzip compression (outermost)
	finalHandler = securityHeadersMiddleware(gzipHandler(finalHandler))
	finalHandler = csrfMiddleware(finalHandler, bp)

	return &Server{
		httpServer: &http.Server{
			Addr:              net.JoinHostPort(host, strconv.Itoa(port)),
			Handler:           finalHandler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		handler: handler,
		logger:  logger,
		port:    port,
	}
}

// contentTypeHandler wraps a handler and sets proper Content-Type and Cache-Control headers
func contentTypeHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set content type based on file extension before serving
		if len(r.URL.Path) > 3 {
			switch {
			case len(r.URL.Path) > 4 && r.URL.Path[len(r.URL.Path)-4:] == ".css":
				w.Header().Set("Content-Type", "text/css")
			case r.URL.Path[len(r.URL.Path)-3:] == ".js":
				w.Header().Set("Content-Type", "application/javascript")
			case len(r.URL.Path) > 4 && r.URL.Path[len(r.URL.Path)-4:] == ".svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			}
		}
		if strings.HasSuffix(r.URL.Path, "app.js") {
			// app.js must revalidate so frontend behavior updates are visible immediately.
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			// Immutable caching - other static assets are versioned via ?v= query param.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}

// gzipResponseWriter wraps http.ResponseWriter with gzip compression
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (grw *gzipResponseWriter) Write(b []byte) (int, error) {
	return grw.Writer.Write(b)
}

var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// gzipHandler compresses responses for clients that accept gzip encoding
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			gz.Close()
			gzipWriterPool.Put(gz)
		}()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")

		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

// csrfMiddleware requires custom header on state-changing requests.
// Form-based endpoints (/login, /logout) are exempt since browsers
// cannot add custom headers to standard form submissions.
func csrfMiddleware(next http.Handler, basePath string) http.Handler {
	loginPath := basePath + "/login"
	logoutPath := basePath + "/logout"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			// Exempt form-based auth endpoints from CSRF header check.
			// These are protected by session cookies with SameSite=Strict instead.
			path := r.URL.Path
			if path != loginPath && path != logoutPath {
				if r.Header.Get("X-Requested-With") == "" {
					http.Error(w, "missing required header", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware adds security headers to all responses
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		// Enable XSS filter (legacy browsers)
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Content Security Policy
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' cdn.jsdelivr.net; "+
				"style-src 'self' 'unsafe-inline' fonts.googleapis.com; "+
				"font-src 'self' fonts.gstatic.com; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"worker-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// Start begins listening for HTTP requests
func (s *Server) Start() error {
	s.logger.Info("starting web server", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down web server")
	return s.httpServer.Shutdown(ctx)
}

// GetSessionStore returns the session store for token eviction.
func (s *Server) GetSessionStore() *SessionStore {
	if s.handler == nil {
		return nil
	}
	return s.handler.GetSessionStore()
}

// GetEmbeddedTemplates returns the embedded templates filesystem
func GetEmbeddedTemplates() embed.FS {
	return templatesFS
}

// GetEmbeddedStatic returns the embedded static files filesystem
func GetEmbeddedStatic() embed.FS {
	return staticFS
}
