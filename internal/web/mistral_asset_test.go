package web

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMistralRecoveryAssetCacheKeys(t *testing.T) {
	h, db := newMenubarTestHandler(t)
	defer db.Close()
	h.version = "unchanged-local-version"
	digest := sha256.New()
	for _, name := range []string{"mistral-recovery.js", "mistral-recovery.css"} {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		digest.Write(data)
	}
	key := fmt.Sprintf("%x", digest.Sum(nil))
	for name, handler := range map[string]http.HandlerFunc{"menubar": h.MenubarPage, "dashboard": h.Dashboard} {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, httptest.NewRequest("GET", "/?provider=mistral", nil))
			for _, asset := range []string{"mistral-recovery.js", "mistral-recovery.css"} {
				if !strings.Contains(rr.Body.String(), "/static/"+asset+"?v="+key) {
					t.Fatalf("%s lacks content-based URL for %s", name, asset)
				}
			}
		})
	}
}
