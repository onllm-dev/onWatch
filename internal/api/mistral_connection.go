package api

import (
	"errors"
	"io/fs"
	"strings"
	"time"
)

// MistralConnection contains only safe diagnostics, never cookies or OS errors.
type MistralConnection struct {
	Reason      string     `json:"reason"`
	Message     string     `json:"message"`
	Retrying    bool       `json:"retrying"`
	NextRetryAt *time.Time `json:"nextRetryAt,omitempty"`
	CanRetry    bool       `json:"canRetry"`
}

// MistralConnectionError deliberately does not retain the raw library error:
// warning strings can contain cookie values, command output, or private paths.
type MistralConnectionError struct {
	Reason     string
	Browser    string
	permission bool
}

func (e *MistralConnectionError) Error() string { return "mistral: " + e.Connection().Message }
func (e *MistralConnectionError) Is(target error) bool {
	return target == ErrMistralAuth || e.permission && target == fs.ErrPermission
}

func (e *MistralConnectionError) Connection() MistralConnection {
	browser := map[string]string{"chrome": "Chrome", "edge": "Microsoft Edge", "firefox": "Firefox", "safari": "Safari"}[e.Browser]
	if browser == "" {
		browser = "the selected browser"
	}
	message := "Could not import browser session. Retry connection or use manual cookies."
	switch e.Reason {
	case "browser_access_denied":
		message = "Allow onWatch to access " + browser + "'s data, then retry connection. On macOS, use Grant Browser Access in the tray menu."
	case "credential_store_denied":
		message = "Access to the browser credential store was denied. Allow the Keychain or keyring prompt, then retry connection."
	case "credential_store_unavailable":
		message = "The browser credential store is unavailable. Unlock your Keychain or keyring, then retry connection, or use manual cookies."
	case "browser_store_unavailable":
		message = "Could not read " + browser + "'s cookie storage. Check that the selected profile exists and is readable, then retry connection."
	case "no_session":
		message = "No Mistral login found. Sign in to Mistral in " + browser + ", then retry connection, or update manual cookies."
	case "session_rejected":
		message = "Mistral rejected the saved session. Sign in again in " + browser + ", then retry connection, or update manual cookies."
	}
	return MistralConnection{Reason: e.Reason, Message: message}
}

// ClassifyMistralImportError uses a small allowlist of known library diagnostics.
// Unknown failures must not be reported as a signed-out browser.
func ClassifyMistralImportError(browser string, err error, warnings []string) *MistralConnectionError {
	var existing *MistralConnectionError
	if errors.As(err, &existing) {
		return existing
	}
	reason := "import_failed"
	permission := errors.Is(err, fs.ErrPermission)
	if err == nil && len(warnings) == 0 {
		reason = "no_session"
	}
	messages := append([]string(nil), warnings...)
	if err != nil {
		messages = append(messages, err.Error())
	}
	for _, message := range messages {
		m := strings.ToLower(message)
		credential := strings.Contains(m, "keychain") || strings.Contains(m, "keyring") || strings.Contains(m, "secret service")
		denied := strings.Contains(m, "permission denied") || strings.Contains(m, "operation not permitted") || strings.Contains(m, "user canceled") || strings.Contains(m, "user cancelled") || strings.Contains(m, "access denied")
		switch {
		case credential && denied:
			return &MistralConnectionError{Reason: "credential_store_denied", Browser: browser}
		case credential:
			reason = "credential_store_unavailable"
		case denied:
			permission = true
		case strings.Contains(m, "database is locked") || strings.Contains(m, "failed to copy cookies db") || strings.Contains(m, "failed to open") || strings.Contains(m, "unable to open database") || errors.Is(err, fs.ErrNotExist):
			if reason == "import_failed" {
				reason = "browser_store_unavailable"
			}
		}
	}
	if permission {
		reason = "browser_access_denied"
	}
	return &MistralConnectionError{Reason: reason, Browser: browser, permission: permission}
}
