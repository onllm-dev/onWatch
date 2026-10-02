package web

import (
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type mistralRetryController struct {
	calls   int
	err     error
	running bool
}

func (m *mistralRetryController) Start(string) error    { return nil }
func (m *mistralRetryController) Stop(string)           {}
func (m *mistralRetryController) IsRunning(string) bool { return m.running }
func (m *mistralRetryController) RequestRetry(key string) error {
	if key != "mistral" {
		panic(key)
	}
	m.calls++
	return m.err
}
func (m *mistralRetryController) ConnectionState(string) (api.ProviderConnection, bool) {
	return api.ProviderConnection{Reason: "browser_access_denied", Message: "Allow browser access.", CanRetry: true}, m.running
}

func TestMistralRetryHTTP(t *testing.T) {
	for _, bp := range []string{"", "/watch"} {
		t.Run(bp, func(t *testing.T) {
			db, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true, BasePath: bp})
			controller := &mistralRetryController{running: true}
			h.SetAgentManager(controller)
			hash, err := HashPassword("test-password")
			if err != nil {
				t.Fatal(err)
			}
			s := NewServer(9211, h, slog.Default(), "admin", hash, "", bp, "", nil)
			for _, tc := range []struct {
				name, path, method, addr string
				header, auth             bool
				want                     int
			}{
				{"local tray", "/api/menubar/mistral/retry", "POST", "127.0.0.1:9", true, false, 202},
				{"remote tray", "/api/menubar/mistral/retry", "POST", "192.0.2.1:9", true, false, 401},
				{"dashboard unauth", "/api/mistral/retry", "POST", "127.0.0.1:9", true, false, 401},
				{"dashboard auth", "/api/mistral/retry", "POST", "192.0.2.1:9", true, true, 202},
				{"csrf", "/api/menubar/mistral/retry", "POST", "127.0.0.1:9", false, false, 403},
				{"method", "/api/menubar/mistral/retry", "GET", "127.0.0.1:9", true, false, 405},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := httptest.NewRequest(tc.method, bp+tc.path, nil)
					r.RemoteAddr = tc.addr
					if tc.header {
						r.Header.Set("X-Requested-With", "XMLHttpRequest")
					}
					if tc.auth {
						r.SetBasicAuth("admin", "test-password")
					}
					w := httptest.NewRecorder()
					s.httpServer.Handler.ServeHTTP(w, r)
					if w.Code != tc.want {
						t.Fatalf("%d %s", w.Code, w.Body.String())
					}
				})
			}
			for _, tc := range []struct {
				err  error
				want int
			}{{&agent.RetryCooldownError{RetryAfter: 29 * time.Second}, 429}, {agent.ErrRetryUnavailable, 409}} {
				controller.err = tc.err
				w := httptest.NewRecorder()
				h.RetryMistral(w, httptest.NewRequest("POST", "/api/mistral/retry", nil))
				if w.Code != tc.want {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				if tc.want == 429 && w.Header().Get("Retry-After") != "29" {
					t.Fatal("missing cooldown header")
				}
			}
		})
	}
}

func TestMistralConnectionWithoutHistory(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewHandler(db, nil, nil, nil, &config.Config{MistralEnabled: true})
	h.SetAgentManager(&mistralRetryController{running: true})
	current := h.buildMistralCurrent()
	c := current["connection"].(api.ProviderConnection)
	if c.Reason != "browser_access_denied" || !c.CanRetry {
		t.Fatalf("%+v", c)
	}
	card := normalizeProviderCard("mistral", "Mistral", "", current, 80, 95)
	if card == nil || card.Connection == nil || len(card.Quotas) != 0 {
		t.Fatalf("missing no-history recovery card: %+v", card)
	}
}
