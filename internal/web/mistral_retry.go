package web

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
)

type mistralRetryControllerAPI interface {
	RequestRetry(string) error
	ConnectionState(string) (api.MistralConnection, bool)
}

func (h *Handler) mistralConnection() api.MistralConnection {
	if controller, ok := h.agentManager.(mistralRetryControllerAPI); ok {
		if c, running := controller.ConnectionState("mistral"); running {
			return c
		}
	}
	var c api.MistralConnection
	if h.store != nil {
		if raw, err := h.store.GetSetting("mistral_connection"); err == nil {
			_ = json.Unmarshal([]byte(raw), &c)
		}
	}
	// A persisted pending flag must never outlive the running agent.
	c.Retrying = false
	c.CanRetry = false
	c.NextRetryAt = nil
	return c
}

func (h *Handler) RetryMistral(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, 405, "method not allowed")
		return
	}
	controller, ok := h.agentManager.(mistralRetryControllerAPI)
	if !ok || h.config == nil || !h.config.HasProvider("mistral") || !h.agentManager.IsRunning("mistral") {
		respondError(w, 409, "Mistral polling is not running. Enable it before retrying.")
		return
	}
	if err := controller.RequestRetry("mistral"); err != nil {
		var cooldown *agent.RetryCooldownError
		if errors.As(err, &cooldown) {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(cooldown.RetryAfter.Seconds())))))
			respondError(w, 429, "Please wait before retrying Mistral.")
			return
		}
		respondError(w, 409, "Mistral retry is unavailable.")
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}
