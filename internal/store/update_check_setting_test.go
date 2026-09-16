package store

import "testing"

// TestStore_UpdateCheckEnabled_DefaultTrue pins the default and the
// accepted off-values for the automatic version check.
//
// The check reaches api.github.com, which discloses the machine's IP address
// and User-Agent to a third party, so it has to be switchable off. It defaults
// on because missing a security update is its own harm, and the call is
// disclosed in docs/PRIVACY.md.
func TestStore_UpdateCheckEnabled_DefaultTrue(t *testing.T) {
	t.Parallel()

	if !(*Store)(nil).UpdateCheckEnabled() {
		t.Fatal("nil store should default to true")
	}

	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if !s.UpdateCheckEnabled() {
		t.Fatal("unset setting should default to true")
	}

	for _, off := range []string{"false", "0", "no", "off", "FALSE", " off "} {
		if err := s.SetSetting(SettingUpdateCheck, off); err != nil {
			t.Fatalf("SetSetting(%q): %v", off, err)
		}
		if s.UpdateCheckEnabled() {
			t.Fatalf("want false for %q", off)
		}
	}

	for _, on := range []string{"true", "1", "yes", "on", ""} {
		if err := s.SetSetting(SettingUpdateCheck, on); err != nil {
			t.Fatalf("SetSetting(%q): %v", on, err)
		}
		if !s.UpdateCheckEnabled() {
			t.Fatalf("want true for %q", on)
		}
	}
}
