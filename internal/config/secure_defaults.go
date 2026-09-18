package config

// Secure-by-default network posture.
//
// onWatch used to bind every interface by default and ship a documented
// password ("changeme"), which together made a fresh install a dashboard the
// whole network could read: provider account emails, usage history and the
// settings page. That is what GDPR Art. 25 calls the wrong default, and
// Art. 32(1)(b) treats as a failure of confidentiality.

import (
	"fmt"
	"net"
	"strings"
)

// EffectiveHost returns the address the server should bind.
//
// An explicit ONWATCH_HOST always wins. With none set, a container binds
// 0.0.0.0 - the network namespace is the boundary there, and a published port
// does not work otherwise - while everywhere else binds loopback, so the
// dashboard is reachable from the machine it runs on and nowhere else until
// the operator decides otherwise.
func (c *Config) EffectiveHost(inContainer bool) string {
	if c != nil && strings.TrimSpace(c.Host) != "" {
		return strings.TrimSpace(c.Host)
	}
	if inContainer {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// IsLoopbackHost reports whether a bind address reaches only this machine.
// An empty address means "all interfaces" and is not loopback.
func IsLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ValidateNetworkExposure refuses to publish the dashboard to the network
// while the password is still the documented default.
//
// It is a refusal rather than a warning because the previous behaviour was a
// warning and the hole stayed open. ONWATCH_ALLOW_DEFAULT_PASSWORD=true is the
// deliberate override for a trusted private network, so nobody is hard-blocked.
func (c *Config) ValidateNetworkExposure(inContainer bool) error {
	if c == nil || c.AllowDefaultPassword || !c.IsDefaultPassword() {
		return nil
	}
	host := c.EffectiveHost(inContainer)
	if IsLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf(
		"refusing to serve the dashboard on %s with the default password: "+
			"anyone who can reach this machine could read your usage history and provider account details. "+
			"Set ONWATCH_ADMIN_PASS to a real password, or set ONWATCH_HOST=127.0.0.1 to keep it local. "+
			"To proceed anyway on a trusted network, set ONWATCH_ALLOW_DEFAULT_PASSWORD=true",
		host)
}
