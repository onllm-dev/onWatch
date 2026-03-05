package api

import (
	"context"
	"errors"
	"testing"
)

func TestRefreshAnthropicToken_ReturnsCanceledContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := RefreshAnthropicToken(ctx, "refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if resp != nil {
		t.Fatalf("response = %#v, want nil", resp)
	}
}
