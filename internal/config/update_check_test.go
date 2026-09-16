package config

import (
	"os"
	"testing"
)

// TestLoad_UpdateCheckEnvPinsOff asserts ONWATCH_UPDATE_CHECK can only ever
// restrict the version check, never force it back on over the dashboard toggle.
//
// This asymmetry is deliberate. The env var is the deployment-level policy for
// an air-gapped or fleet-managed install, so a false value pins the check off
// and the dashboard shows the control as locked. A true value only restates the
// default, because a privacy control the operator cannot switch off from the UI
// would not be a withdrawal "as easy as giving consent" under DPDP s.6(4).
func TestLoad_UpdateCheckEnvPinsOff(t *testing.T) {
	cases := []struct {
		env       string
		set       bool
		wantForce bool
	}{
		{set: false, wantForce: false},
		{env: "", set: true, wantForce: false},
		{env: "false", set: true, wantForce: true},
		{env: "FALSE", set: true, wantForce: true},
		{env: "0", set: true, wantForce: true},
		{env: "no", set: true, wantForce: true},
		{env: "off", set: true, wantForce: true},
		{env: " off ", set: true, wantForce: true},
		{env: "true", set: true, wantForce: false},
		{env: "1", set: true, wantForce: false},
		{env: "yes", set: true, wantForce: false},
	}

	for _, tc := range cases {
		name := "unset"
		if tc.set {
			name = "set-" + tc.env
		}
		t.Run(name, func(t *testing.T) {
			if tc.set {
				t.Setenv("ONWATCH_UPDATE_CHECK", tc.env)
			} else {
				os.Unsetenv("ONWATCH_UPDATE_CHECK")
			}

			cfg, err := loadWithArgs([]string{})
			if err != nil {
				t.Fatalf("loadWithArgs: %v", err)
			}
			if cfg.UpdateCheckForcedOff != tc.wantForce {
				t.Errorf("UpdateCheckForcedOff = %v, want %v for env %q (set=%v)",
					cfg.UpdateCheckForcedOff, tc.wantForce, tc.env, tc.set)
			}
		})
	}
}
