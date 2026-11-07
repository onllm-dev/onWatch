package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestZenmuxUsageBudgetBeyondQuota(t *testing.T) {
	for _, delayHeaders := range []bool{true, false} {
		name := "body"
		if delayHeaders {
			name = "headers"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !delayHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				timer := time.NewTimer(22 * time.Second)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, `{"success":true,"data":{"tokensByModel":[]}}`)
			}))
			defer server.Close()
			c := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
			defer c.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			_, err := c.FetchUsage(ctx, ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609", BillType: "subscription"})
			if err != nil {
				t.Fatalf("analytics should survive %s delayed beyond quota budget: %v", name, err)
			}
		})
	}
}

// These deliberately exercise the production budgets, not a shorter parent
// deadline: a slow header and a slow body must share one 60-second budget.
func TestZenmuxUsageProductionBudget(t *testing.T) {
	for _, scenario := range []string{"headers", "body", "combined"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "combined" {
					timer := time.NewTimer(30 * time.Second)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
				}
				if scenario != "headers" {
					fmt.Fprint(w, `{"success":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()
			start := time.Now()
			_, err := client.FetchUsage(ctx, ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
			elapsed := time.Since(start)
			want := ErrZenmuxInvalidResponse
			if scenario == "headers" {
				want = ErrZenmuxNetwork
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s: analytics timeout error = %v, want %v", scenario, err, want)
			}
			if elapsed < 58*time.Second || elapsed > 62*time.Second {
				t.Fatalf("%s: analytics ended after %s, want approximately 60s", scenario, elapsed)
			}
		})
	}
}

func TestZenmuxQuotaProductionBudget(t *testing.T) {
	for _, scenario := range []string{"headers", "body", "combined"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "combined" {
					timer := time.NewTimer(10 * time.Second)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
				}
				if scenario != "headers" {
					fmt.Fprint(w, `{"success":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			_, err := client.FetchSnapshot(ctx)
			elapsed := time.Since(start)
			want := ErrZenmuxInvalidResponse
			if scenario == "headers" {
				want = ErrZenmuxNetwork
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s: quota timeout error = %v, want %v", scenario, err, want)
			}
			if elapsed < 18*time.Second || elapsed > 22*time.Second {
				t.Fatalf("%s: quota ended after %s, want approximately 20s", scenario, elapsed)
			}
		})
	}
}

func TestZenmuxUsageBudgetHonorsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
	defer c.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.FetchUsage(ctx, ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("caller cancellation did not stop request")
	}
}

func TestZenmuxUsageBudgetHonorsCancellationDuringBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL))
	defer c.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := c.FetchUsage(ctx, ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body cancellation = %v, want context deadline", err)
	}
}
