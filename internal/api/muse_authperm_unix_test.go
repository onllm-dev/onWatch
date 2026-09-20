//go:build !windows

package api

import "testing"

func TestMuseAuthFilePermsOK(t *testing.T) {
	if !museAuthFilePermsOK(0o600) {
		t.Error("0600 is owner-only and must be accepted")
	}
	if !museAuthFilePermsOK(0o400) {
		t.Error("0400 is owner-only and must be accepted")
	}
	if museAuthFilePermsOK(0o644) {
		t.Error("0644 is world-readable and must be rejected: the file holds a bearer key")
	}
	if museAuthFilePermsOK(0o660) {
		t.Error("0660 is group-readable and must be rejected")
	}
}
