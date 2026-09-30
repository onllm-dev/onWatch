package menubar

import "sync"

// Both native entry points share one gate for the lifetime of the picker.
type browserGrantFlow struct{ mu sync.Mutex }

func (g *browserGrantFlow) run(blocked func() string, request func(string) (bool, string), verify func(string) error, retry func() error) string {
	if !g.mu.TryLock() {
		return "busy"
	}
	defer g.mu.Unlock()
	if path := blocked(); path != "" {
		granted, detail := request(path)
		if !granted {
			if detail == "cancelled" {
				return "cancelled"
			}
			return "unavailable"
		}
		if err := verify(path); err != nil {
			return "unavailable"
		}
	}
	if retry == nil {
		return "retry_failed"
	}
	if err := retry(); err != nil {
		return "retry_failed"
	}
	return "retrying"
}
