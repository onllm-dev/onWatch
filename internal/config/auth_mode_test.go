package config

import (
	"net"
	"os"
	"testing"
)

func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("invalid test IP %q", s)
	}
	return ip
}

func TestAuthMode_DefaultLocal(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer clearTestEnv()

	cfg, err := loadWithArgs(nil)
	if err != nil {
		t.Fatalf("loadWithArgs() failed: %v", err)
	}
	if cfg.AuthMode != AuthModeLocal {
		t.Errorf("AuthMode = %q, want %q", cfg.AuthMode, AuthModeLocal)
	}
	if cfg.TrustedUserHeader != "X-Forwarded-User" {
		t.Errorf("TrustedUserHeader = %q, want default X-Forwarded-User", cfg.TrustedUserHeader)
	}
}

func TestAuthMode_TrustedProxy(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_AUTH_MODE", "trusted_proxy")
	os.Setenv("ONWATCH_TRUSTED_PROXY_CIDRS", "172.30.0.0/16, 127.0.0.1")
	os.Setenv("ONWATCH_TRUSTED_USER_HEADER", "X-authentik-username")
	defer clearTestEnv()

	cfg, err := loadWithArgs(nil)
	if err != nil {
		t.Fatalf("loadWithArgs() failed: %v", err)
	}
	if cfg.AuthMode != AuthModeTrustedProxy {
		t.Errorf("AuthMode = %q, want %q", cfg.AuthMode, AuthModeTrustedProxy)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("TrustedProxyCIDRs = %v, want 2 entries", cfg.TrustedProxyCIDRs)
	}
	if cfg.TrustedUserHeader != "X-authentik-username" {
		t.Errorf("TrustedUserHeader = %q, want X-authentik-username", cfg.TrustedUserHeader)
	}
}

func TestAuthMode_TrustedProxyWithoutCIDRsFailsClosed(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_AUTH_MODE", "trusted_proxy")
	defer clearTestEnv()

	if _, err := loadWithArgs(nil); err == nil {
		t.Fatal("expected error when trusted_proxy mode has no CIDRs, got nil")
	}
}

func TestAuthMode_InvalidValueRejected(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_AUTH_MODE", "oidc")
	defer clearTestEnv()

	if _, err := loadWithArgs(nil); err == nil {
		t.Fatal("expected error for invalid ONWATCH_AUTH_MODE, got nil")
	}
}

func TestAuthMode_InvalidCIDRRejected(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_AUTH_MODE", "trusted_proxy")
	os.Setenv("ONWATCH_TRUSTED_PROXY_CIDRS", "not-a-cidr")
	defer clearTestEnv()

	if _, err := loadWithArgs(nil); err == nil {
		t.Fatal("expected error for invalid CIDR, got nil")
	}
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	nets, err := ParseTrustedProxyCIDRs([]string{"172.30.0.0/16", "127.0.0.1", "::1", "fd00::/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxyCIDRs() failed: %v", err)
	}
	if len(nets) != 4 {
		t.Fatalf("got %d networks, want 4", len(nets))
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"172.30.5.9", true},
		{"172.31.0.1", false},
		{"127.0.0.1", true},
		{"127.0.0.2", false},
		{"::1", true},
		{"fd00::1234", true},
		{"2001:db8::1", false},
	}
	for _, tc := range cases {
		got := false
		for _, n := range nets {
			if n.Contains(mustParseIP(t, tc.ip)) {
				got = true
				break
			}
		}
		if got != tc.want {
			t.Errorf("IP %s in trusted set = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
