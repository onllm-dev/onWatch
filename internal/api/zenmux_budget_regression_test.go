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

// The budget tests below shrink the whole-request timeout through
// withZenmuxBudgets so the same logic runs in milliseconds. Production values
// stay at the documented 20s/60s (guarded by
// TestZenmuxProductionBudgetsUnchanged).
const (
	testZenmuxQuotaBudget = 400 * time.Millisecond
	// The usage budget sits far above the quota budget on purpose: the upper
	// bound for a quota call (budget + slack) must stay clearly below the usage
	// budget, so a call cut off by the wrong allowance still fails the test
	// instead of slipping past a loose upper bound.
	testZenmuxUsageBudget = 4 * time.Second
	// Race detector overhead on a loaded runner can add hundreds of
	// milliseconds after the budget timer fires. The upper bounds allow this
	// much absolute slack on top of the budget instead of a tight multiplier.
	testZenmuxBudgetSlack = 2 * time.Second
)

func TestZenmuxProductionBudgetsUnchanged(t *testing.T) {
	t.Parallel()
	c := NewZenmuxClient("synthetic-key")
	defer c.CloseIdleConnections()
	if c.http.Timeout != 20*time.Second || c.usageHTTP.Timeout != 60*time.Second {
		t.Fatalf("client budgets = %s/%s, want 20s/60s", c.http.Timeout, c.usageHTTP.Timeout)
	}
	quotaTransport, ok := c.http.Transport.(*http.Transport)
	if !ok || quotaTransport.ResponseHeaderTimeout != 20*time.Second {
		t.Fatalf("quota header timeout = %v, want 20s", quotaTransport.ResponseHeaderTimeout)
	}
	usageTransport, ok := c.usageHTTP.Transport.(*http.Transport)
	if !ok || usageTransport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("usage header timeout = %v, want 60s", usageTransport.ResponseHeaderTimeout)
	}
}

func TestZenmuxUsageBudgetBeyondQuota(t *testing.T) {
	for _, delayHeaders := range []bool{true, false} {
		name := "body"
		if delayHeaders {
			name = "headers"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Past the quota budget but comfortably inside the usage budget,
			// so the analytics request proves it is not bound by the smaller
			// allowance.
			delay := testZenmuxQuotaBudget + (testZenmuxUsageBudget-testZenmuxQuotaBudget)/2
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !delayHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, `{"success":true,"data":{"tokensByModel":[]}}`)
			}))
			defer server.Close()
			c := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL), withZenmuxBudgets(testZenmuxQuotaBudget, testZenmuxUsageBudget))
			defer c.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 2*testZenmuxUsageBudget)
			defer cancel()
			_, err := c.FetchUsage(ctx, ZenmuxUsageQuery{Dimension: "BIZ_MTH", Time: "202609", BillType: "subscription"})
			if err != nil {
				t.Fatalf("analytics should survive %s delayed beyond the quota budget: %v", name, err)
			}
		})
	}
}

// These exercise one shared whole-request budget: a slow header and a slow body
// must not each get their own timeout.
func TestZenmuxUsageProductionBudget(t *testing.T) {
	for _, scenario := range []string{"headers", "body", "combined"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "combined" {
					timer := time.NewTimer(testZenmuxUsageBudget / 2)
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
			client := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL), withZenmuxBudgets(testZenmuxQuotaBudget, testZenmuxUsageBudget))
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 3*testZenmuxUsageBudget)
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
			if elapsed < testZenmuxUsageBudget*9/10 || elapsed > testZenmuxUsageBudget+testZenmuxBudgetSlack {
				t.Fatalf("%s: analytics ended after %s, want approximately %s", scenario, elapsed, testZenmuxUsageBudget)
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
					timer := time.NewTimer(testZenmuxQuotaBudget / 2)
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
			client := NewZenmuxClient("synthetic-key", withZenmuxBaseURL(server.URL), withZenmuxBudgets(testZenmuxQuotaBudget, testZenmuxUsageBudget))
			defer client.CloseIdleConnections()
			// The caller budget is the "long" overall allowance: it must sit
			// above the quota upper bound so the request budget, not the caller
			// context, is what ends the call.
			ctx, cancel := context.WithTimeout(context.Background(), testZenmuxUsageBudget)
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
			if elapsed < testZenmuxQuotaBudget*9/10 || elapsed > testZenmuxQuotaBudget+testZenmuxBudgetSlack {
				t.Fatalf("%s: quota ended after %s, want approximately %s", scenario, elapsed, testZenmuxQuotaBudget)
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
