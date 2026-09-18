package config

import (
	"strings"
	"testing"
)

// TestEffectiveHost_DefaultsToLoopbackOutsideContainers asserts the dashboard
// is not exposed to the whole network unless the operator asks for it.
//
// The dashboard serves provider account emails, usage history and the settings
// page. Binding it to every interface by default is the opposite of data
// protection by design (GDPR Art. 25(2)). In a container the network namespace
// is the boundary and 0.0.0.0 is what makes a published port work at all, so
// that case keeps the old default.
func TestEffectiveHost_DefaultsToLoopbackOutsideContainers(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		docker   bool
		wantHost string
	}{
		{name: "unset on a workstation", host: "", docker: false, wantHost: "127.0.0.1"},
		{name: "unset in a container", host: "", docker: true, wantHost: "0.0.0.0"},
		{name: "explicit wins on a workstation", host: "0.0.0.0", docker: false, wantHost: "0.0.0.0"},
		{name: "explicit wins in a container", host: "127.0.0.1", docker: true, wantHost: "127.0.0.1"},
		{name: "explicit lan address", host: "192.168.1.10", docker: false, wantHost: "192.168.1.10"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Host: tc.host}
			if got := c.EffectiveHost(tc.docker); got != tc.wantHost {
				t.Errorf("EffectiveHost() = %q, want %q", got, tc.wantHost)
			}
		})
	}
}

// TestIsLoopbackHost covers the predicate the exposure check relies on.
func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"127.0.0.1", "localhost", "::1", "127.5.5.5", "[::1]"}
	for _, h := range loopback {
		if !IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = false, want true", h)
		}
	}
	exposed := []string{"0.0.0.0", "", "192.168.1.10", "::", "10.0.0.1", "example.com"}
	for _, h := range exposed {
		if IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = true, want false", h)
		}
	}
}

// TestValidate_RefusesNetworkExposureWithDefaultPassword asserts onWatch will
// not publish the dashboard to the network while the password is still the
// documented default.
//
// "changeme" is in the README, so a network-reachable dashboard with it is
// effectively unauthenticated (GDPR Art. 25(1) and Art. 32(1)(b)). The refusal
// names both remedies, and ONWATCH_ALLOW_DEFAULT_PASSWORD exists so nobody is
// hard-blocked on a trusted private network.
func TestValidate_RefusesNetworkExposureWithDefaultPassword(t *testing.T) {
	cases := []struct {
		name        string
		host        string
		pass        string
		allowEscape bool
		wantErr     bool
	}{
		{name: "loopback with default password is fine", host: "127.0.0.1", pass: DefaultAdminPass, wantErr: false},
		{name: "network with a real password is fine", host: "0.0.0.0", pass: "a-real-password", wantErr: false},
		{name: "network with the default password is refused", host: "0.0.0.0", pass: DefaultAdminPass, wantErr: true},
		{name: "lan address with the default password is refused", host: "192.168.1.10", pass: DefaultAdminPass, wantErr: true},
		{name: "escape hatch permits it", host: "0.0.0.0", pass: DefaultAdminPass, allowEscape: true, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				Host:                 tc.host,
				AdminPass:            tc.pass,
				AllowDefaultPassword: tc.allowEscape,
			}
			err := c.ValidateNetworkExposure(false)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				// The message has to tell the operator what to do about it.
				for _, want := range []string{"ONWATCH_ADMIN_PASS", "ONWATCH_HOST"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error should name %s: %v", want, err)
					}
				}
			}
		})
	}
}

// TestLoad_AllowDefaultPasswordEnv covers the escape hatch parsing.
func TestLoad_AllowDefaultPasswordEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{env: "true", want: true},
		{env: "1", want: true},
		{env: "yes", want: true},
		{env: "false", want: false},
		{env: "", want: false},
	} {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv("ONWATCH_ALLOW_DEFAULT_PASSWORD", tc.env)
			cfg, err := loadWithArgs([]string{})
			if err != nil {
				t.Fatalf("loadWithArgs: %v", err)
			}
			if cfg.AllowDefaultPassword != tc.want {
				t.Errorf("AllowDefaultPassword = %v, want %v", cfg.AllowDefaultPassword, tc.want)
			}
		})
	}
}

// TestLoad_MetricsPublicEnv covers the opt-in for an unauthenticated /metrics.
func TestLoad_MetricsPublicEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{env: "true", want: true},
		{env: "1", want: true},
		{env: "", want: false},
		{env: "false", want: false},
	} {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv("ONWATCH_METRICS_PUBLIC", tc.env)
			cfg, err := loadWithArgs([]string{})
			if err != nil {
				t.Fatalf("loadWithArgs: %v", err)
			}
			if cfg.MetricsPublic != tc.want {
				t.Errorf("MetricsPublic = %v, want %v", cfg.MetricsPublic, tc.want)
			}
		})
	}
}
