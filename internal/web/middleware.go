// Package web provides HTTP server components for the SynTrack dashboard.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

const sessionCookieName = "syntrack_session"
const sessionMaxAge = 7 * 24 * 3600 // 7 days

// SessionStore manages in-memory session tokens (single-user, lightweight).
type SessionStore struct {
	mu       sync.RWMutex
	tokens   map[string]time.Time // token -> expiry
	username string
	password string
}

// NewSessionStore creates a session store with the given credentials.
func NewSessionStore(username, password string) *SessionStore {
	return &SessionStore{
		tokens:   make(map[string]time.Time),
		username: username,
		password: password,
	}
}

// Authenticate validates credentials and returns a session token if valid.
func (s *SessionStore) Authenticate(username, password string) (string, bool) {
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) == 1
	if !userMatch || !passMatch {
		return "", false
	}

	token := generateToken()
	s.mu.Lock()
	s.tokens[token] = time.Now().Add(time.Duration(sessionMaxAge) * time.Second)
	s.mu.Unlock()
	return token, true
}

// ValidateToken checks if a session token is valid and not expired.
func (s *SessionStore) ValidateToken(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	expiry, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return false
	}
	return true
}

// Invalidate removes a session token.
func (s *SessionStore) Invalidate(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// SessionAuthMiddleware uses session cookies for browser requests and Basic Auth for API.
func SessionAuthMiddleware(sessions *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Static assets bypass authentication
			if isStaticAsset(path) {
				next.ServeHTTP(w, r)
				return
			}

			// Login page is always accessible
			if path == "/login" {
				next.ServeHTTP(w, r)
				return
			}

			// Check session cookie first
			if cookie, err := r.Cookie(sessionCookieName); err == nil {
				if sessions.ValidateToken(cookie.Value) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// For API endpoints, also accept Basic Auth (for curl/scripts)
			if strings.HasPrefix(path, "/api/") {
				u, p, ok := extractCredentials(r)
				if ok {
					userMatch := subtle.ConstantTimeCompare([]byte(u), []byte(sessions.username)) == 1
					passMatch := subtle.ConstantTimeCompare([]byte(p), []byte(sessions.password)) == 1
					if userMatch && passMatch {
						next.ServeHTTP(w, r)
						return
					}
				}
				// API requests get 401 with Basic Auth challenge
				w.Header().Set("WWW-Authenticate", `Basic realm="SynTrack"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Browser requests: redirect to login page
			http.Redirect(w, r, "/login", http.StatusFound)
		})
	}
}

// AuthMiddleware returns an http.Handler that enforces Basic Auth.
// Kept for backwards compatibility with tests.
func AuthMiddleware(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isStaticAsset(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			u, p, ok := extractCredentials(r)
			if !ok {
				writeUnauthorized(w)
				return
			}

			userMatch := subtle.ConstantTimeCompare([]byte(u), []byte(username)) == 1
			passMatch := subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1

			if !userMatch || !passMatch {
				writeUnauthorized(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth is an alias for AuthMiddleware.
func RequireAuth(username, password string) func(http.Handler) http.Handler {
	return AuthMiddleware(username, password)
}

// extractCredentials extracts username and password from the Authorization header.
func extractCredentials(r *http.Request) (username, password string, ok bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", "", false
	}

	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", "", false
	}

	encoded := authHeader[len(prefix):]
	if encoded == "" {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return parts[0], parts[1], true
}

// isStaticAsset checks if the request path is for a static asset.
func isStaticAsset(path string) bool {
	return strings.HasPrefix(path, "/static/")
}

// writeUnauthorized sends a 401 Unauthorized response.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="SynTrack"`)
	w.WriteHeader(http.StatusUnauthorized)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
