package web

// Data-protection endpoints: the privacy notice, data export and data erasure.
//
// These are the operator-facing half of the mechanisms in
// internal/store/privacy_store.go. They exist so a deployer can actually
// honour the rights they owe: access and portability (GDPR Art. 15/20, DPDP
// s.11), erasure (GDPR Art. 17, DPDP s.12(3) and s.8(7)) and the information
// duty (GDPR Art. 13/14, DPDP s.5).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// eraseConfirmation is the exact string a caller must send to erase data.
// Erasure cannot be undone, so it takes a deliberate, typed acknowledgement
// rather than just a POST that a stray click or a replayed request could make.
const eraseConfirmation = "ERASE"

// isPublicNoticePath reports whether a path is the privacy notice, which is
// served without authentication.
//
// GDPR Art. 12 requires the notice to be easily accessible, and a notice that
// can only be read after logging in is no use to someone asking what is held
// about them. The page carries no personal data - only a description of what
// this install processes - so there is nothing to protect behind auth.
func isPublicNoticePath(path, basePath string) bool {
	return path == basePath+"/privacy"
}

// PrivacyPage renders the privacy notice (GET /privacy).
func (h *Handler) PrivacyPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.privacyTmpl == nil {
		respondError(w, http.StatusInternalServerError, "privacy notice template not loaded")
		return
	}

	data := map[string]interface{}{
		"Title":    "Privacy",
		"BasePath": h.getBasePath(),
		"Version":  h.version,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.privacyTmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		h.logger.Error("failed to render privacy notice", "error", err)
	}
}

// ExportData streams a complete export of everything stored (GET
// /api/privacy/export), for GDPR Art. 15/20 and DPDP s.11.
//
// The response streams straight from SQLite so a long history does not have to
// be assembled in memory first, which keeps the export inside the project's
// RAM ceiling. Credentials are redacted; personal data is not.
func (h *Handler) ExportData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.store == nil {
		respondError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	filename := fmt.Sprintf("onwatch-export-%s.json", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// The body is generated as it is written, so it must not be cached by an
	// intermediary - it is a copy of the operator's own data.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if err := h.store.ExportAll(r.Context(), w); err != nil {
		// Headers are already sent, so the status cannot be changed. Log it and
		// let the truncated JSON fail to parse rather than handing back a file
		// that looks complete but is not.
		h.logger.Error("data export failed", "error", err)
	}
}

// maxRetentionDays caps the accepted retention period at roughly 100 years.
// It exists to reject a nonsense value rather than to express a policy.
const maxRetentionDays = 36500

// applyRetentionSettings validates and persists the retention fields of a
// settings update, recording what changed in result.
//
// Both fields are optional. When only one is supplied, it is validated against
// the stored value of the other, so a partial update cannot leave the pair
// incoherent.
func (h *Handler) applyRetentionSettings(body map[string]json.RawMessage, result map[string]interface{}) error {
	rawScrub, hasScrub := body[store.SettingRetentionScrubDays]
	rawDelete, hasDelete := body[store.SettingRetentionDeleteDays]
	if !hasScrub && !hasDelete {
		return nil
	}

	current := h.store.RetentionPolicyFromSettings()
	scrubDays := int(current.ScrubAfter.Hours() / 24)
	deleteDays := int(current.DeleteAfter.Hours() / 24)

	parse := func(raw json.RawMessage, field string) (int, error) {
		var days int
		if err := json.Unmarshal(raw, &days); err != nil {
			return 0, fmt.Errorf("invalid %s value - expected a whole number of days", field)
		}
		if days < 0 {
			return 0, fmt.Errorf("%s cannot be negative", field)
		}
		if days > maxRetentionDays {
			return 0, fmt.Errorf("%s cannot exceed %d days", field, maxRetentionDays)
		}
		return days, nil
	}

	var err error
	if hasScrub {
		if scrubDays, err = parse(rawScrub, store.SettingRetentionScrubDays); err != nil {
			return err
		}
	}
	if hasDelete {
		if deleteDays, err = parse(rawDelete, store.SettingRetentionDeleteDays); err != nil {
			return err
		}
	}

	if scrubDays > 0 && deleteDays > 0 && deleteDays < scrubDays {
		return fmt.Errorf("%s (%d) must be at least %s (%d): rows would be deleted before they were ever scrubbed",
			store.SettingRetentionDeleteDays, deleteDays, store.SettingRetentionScrubDays, scrubDays)
	}

	if hasScrub {
		if err := h.store.SetSetting(store.SettingRetentionScrubDays, strconv.Itoa(scrubDays)); err != nil {
			h.logger.Error("failed to save retention scrub setting", "error", err)
			return fmt.Errorf("failed to save setting")
		}
		result[store.SettingRetentionScrubDays] = scrubDays
	}
	if hasDelete {
		if err := h.store.SetSetting(store.SettingRetentionDeleteDays, strconv.Itoa(deleteDays)); err != nil {
			h.logger.Error("failed to save retention delete setting", "error", err)
			return fmt.Errorf("failed to save setting")
		}
		result[store.SettingRetentionDeleteDays] = deleteDays
	}

	h.logger.Info("retention policy updated", "scrub_days", scrubDays, "delete_days", deleteDays)
	return nil
}

// eraseRequest is the body of an erasure request.
type eraseRequest struct {
	Scope    string `json:"scope"`
	Provider string `json:"provider"`
	Confirm  string `json:"confirm"`
}

// EraseData deletes stored personal data (POST /api/privacy/erase), for GDPR
// Art. 17 and DPDP s.12(3) and s.8(7).
//
// Scope "all" clears every provider's history plus device and account records.
// Scope "provider" clears one provider, which is the proportionate answer when
// consent is withdrawn for one purpose rather than all of them.
func (h *Handler) EraseData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.store == nil {
		respondError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	var req eraseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Case-sensitive on purpose: the UI asks the operator to type it.
	if req.Confirm != eraseConfirmation {
		respondError(w, http.StatusBadRequest,
			fmt.Sprintf("erasure requires \"confirm\":%q - this cannot be undone", eraseConfirmation))
		return
	}

	switch strings.ToLower(strings.TrimSpace(req.Scope)) {
	case "all":
		removed, err := h.store.EraseAll(r.Context())
		if err != nil {
			h.logger.Error("full erasure failed", "error", err)
			respondError(w, http.StatusInternalServerError, "erasure failed")
			return
		}
		// Logged without any identifier: the record of the action is the point,
		// and re-recording what was just erased would defeat it.
		h.logger.Warn("erased all stored data on operator request", "tables", len(removed))
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"scope":   "all",
			"removed": removed,
		})

	case "provider":
		provider := strings.ToLower(strings.TrimSpace(req.Provider))
		removed, err := h.store.EraseProvider(r.Context(), provider)
		if err != nil {
			// The store resolves the name against its registry, so an unknown
			// provider is a client error rather than a server fault.
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.Warn("erased provider data on operator request", "provider", provider, "tables", len(removed))
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"scope":    "provider",
			"provider": provider,
			"removed":  removed,
		})

	default:
		respondError(w, http.StatusBadRequest, `scope must be "all" or "provider"`)
	}
}
