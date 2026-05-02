package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMoonshotClient(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		respBody   string
		wantErr    error
		check      func(t *testing.T, resp *MoonshotBalanceResponse)
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			respBody:   `{"code":0,"data":{"available_balance":100.5,"voucher_balance":20.0,"cash_balance":80.5}}`,
			wantErr:    nil,
			check: func(t *testing.T, resp *MoonshotBalanceResponse) {
				if resp == nil {
					t.Fatal("expected non-nil response")
				}
				if resp.Code != 0 {
					t.Errorf("expected code 0, got %d", resp.Code)
				}
				if resp.Data.AvailableBalance != 100.5 {
					t.Errorf("expected available 100.5, got %v", resp.Data.AvailableBalance)
				}
				if resp.Data.VoucherBalance != 20.0 {
					t.Errorf("expected voucher 20.0, got %v", resp.Data.VoucherBalance)
				}
				if resp.Data.CashBalance != 80.5 {
					t.Errorf("expected cash 80.5, got %v", resp.Data.CashBalance)
				}
			},
		},
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			respBody:   `{"error":"invalid token"}`,
			wantErr:    ErrMoonshotUnauthorized,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			respBody:   `{"error":"too many requests"}`,
			wantErr:    ErrMoonshotRateLimited,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			respBody:   `{"error":"internal server error"}`,
			wantErr:    ErrMoonshotServerError,
		},
		{
			name:       "invalid json",
			statusCode: http.StatusOK,
			respBody:   `{"data": {invalid json`,
			wantErr:    ErrMoonshotInvalidResponse,
		},
		{
			name:       "empty response",
			statusCode: http.StatusOK,
			respBody:   ``,
			wantErr:    ErrMoonshotInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
					t.Errorf("expected Bearer test-key, got %s", auth)
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.respBody))
			}))
			defer server.Close()

			client := NewMoonshotClient("test-key", nil, WithMoonshotBaseURL(server.URL))
			resp, err := client.FetchBalance(context.Background())

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errorsIs(err, tt.wantErr) {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.check != nil {
					tt.check(t, resp)
				}
			}
		})
	}
}

func errorsIs(err, target error) bool {
	if err == target {
		return true
	}
	return err != nil && target != nil && err.Error() == target.Error() || (err != nil && target != nil && err.Error() != "" && target.Error() != "" && err.Error()[:len(target.Error())] == target.Error())
}
