package tracker

import (
	"context"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type MistralTracker struct {
	store   *store.Store
	onReset func(string)
}

func NewMistralTracker(s *store.Store) *MistralTracker { return &MistralTracker{store: s} }
func (t *MistralTracker) SetOnReset(fn func(string))   { t.onReset = fn }
func (t *MistralTracker) Process(ctx context.Context, snap *api.MistralSnapshot) error {
	for _, q := range snap.Quotas {
		reset, e := t.store.TrackMistral(ctx, snap.Identity, q)
		if e != nil {
			return e
		}
		if reset && t.onReset != nil {
			t.onReset(q.Name)
		}
	}
	return nil
}
