package api

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/steipete/sweetcookie"
)

func TestMistralImportDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, warning, reason string
		err                   error
	}{
		{"permission", "", "browser_access_denied", &fs.PathError{Op: "open", Path: "/private/person/Cookies", Err: fs.ErrPermission}},
		{"warning permission", "sweetcookie: failed to copy cookies DB: open /private/person/Cookies: operation not permitted", "browser_access_denied", nil},
		{"keychain denied", "sweetcookie: macOS keychain read failed (Chrome Safe Storage): user canceled the operation", "credential_store_denied", nil},
		{"keychain unavailable", "sweetcookie: macOS keychain read failed (Chrome Safe Storage): item could not be found", "credential_store_unavailable", nil},
		{"locked", "sweetcookie: failed to read Chrome cookies: database is locked", "browser_store_unavailable", nil},
		{"unknown", "unexpected failure with secret-cookie and /private/person", "import_failed", nil},
		{"empty", "", "no_session", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ImportMistralSessions(context.Background(), MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
				result := sweetcookie.Result{}
				if tc.warning != "" {
					result.Warnings = []string{tc.warning}
				}
				return result, tc.err
			})
			var diagnostic *MistralConnectionError
			if !errors.As(err, &diagnostic) || diagnostic.Reason != tc.reason {
				t.Fatalf("got %v, want reason %s", err, tc.reason)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-cookie") {
				t.Fatal("diagnostic leaks raw input")
			}
			if tc.err != nil && !errors.Is(err, fs.ErrPermission) {
				t.Fatal("lost permission cause")
			}
		})
	}
}

func TestMistralImportUsableSessionIgnoresWarnings(t *testing.T) {
	sessions, err := ImportMistralSessions(context.Background(), MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Warnings: []string{"unrelated store failed"}, Cookies: []sweetcookie.Cookie{{Name: "ory_session_test", Value: "synthetic", Domain: ".mistral.ai", Path: "/"}}}, nil
	})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
}

func TestMistralImportCSRFOnlyIsNotSession(t *testing.T) {
	_, err := ImportMistralSessions(context.Background(), MistralSource{Browser: "chrome", Profile: "synthetic", Container: 0}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "csrftoken", Value: "synthetic", Domain: ".mistral.ai", Path: "/"}}}, nil
	})
	var diagnostic *MistralConnectionError
	if !errors.As(err, &diagnostic) || diagnostic.Reason != "no_session" {
		t.Fatalf("got %v", err)
	}
}
