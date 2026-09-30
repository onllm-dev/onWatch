package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestMistralNativeRetryRequest(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/watch/api/menubar/mistral/retry" || r.Header.Get("X-Requested-With") == "" {
			t.Errorf("bad retry request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(202)
	}))
	defer s.Close()
	_, p, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpMistralRetry(port, "/watch")(); err != nil {
		t.Fatal(err)
	}
}
