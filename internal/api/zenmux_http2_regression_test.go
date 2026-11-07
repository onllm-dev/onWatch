package api

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestZenmuxUsageHTTP2TLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("request protocol = %s, want HTTP/2", r.Proto)
		}
		if r.URL.Path != "/api/v1/management/usage" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"success":true,"data":{"tokensByModel":[{"bizTime":"20260903","modelSlug":"sample/model","requestCounts":"2"}]}}`)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
	defer client.CloseIdleConnections()
	transport := client.usageHTTP.Transport.(*http.Transport)
	if transport.TLSClientConfig == nil {
		t.Fatal("missing cloned TLS configuration")
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots

	result, err := client.FetchUsage(context.Background(), ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
	if err != nil {
		t.Fatalf("FetchUsage over HTTP/2: %v", err)
	}
	if len(result.Data.TokensByModel) != 1 || result.Data.TokensByModel[0].ModelSlug != "sample/model" {
		t.Fatalf("unexpected usage data: %+v", result.Data)
	}
}
