package api

import "time"

// ProviderConnection is a provider's user-facing connection diagnostic. It
// carries only safe, fixed messages, never credentials or OS errors.
type ProviderConnection struct {
	Reason      string     `json:"reason"`
	Message     string     `json:"message"`
	Retrying    bool       `json:"retrying"`
	NextRetryAt *time.Time `json:"nextRetryAt,omitempty"`
	CanRetry    bool       `json:"canRetry"`
}
