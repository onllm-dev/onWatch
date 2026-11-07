package config

import (
	"strings"
	"testing"
)

func TestZenmuxConfigurationEnabledByKey(t *testing.T) {
	t.Setenv("ZENMUX_MANAGEMENT_API_KEY", "synthetic-test-value")
	t.Setenv("ZENMUX_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasProvider("zenmux") {
		t.Fatal("key did not enable Zenmux")
	}
}

func TestZenmuxConfigurationRedactsKey(t *testing.T) {
	t.Setenv("ZENMUX_MANAGEMENT_API_KEY", "synthetic-test-value")
	t.Setenv("ZENMUX_ENABLED", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.String(), "synthetic-test-value") || strings.Contains(cfg.String(), "synt") || strings.Contains(cfg.String(), "lue") {
		t.Fatal("config leaked key material")
	}
}

func TestZenmuxConfigurationRespectsOptOut(t *testing.T) {
	t.Setenv("ZENMUX_MANAGEMENT_API_KEY", "synthetic-test-value")
	t.Setenv("ZENMUX_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasProvider("zenmux") {
		t.Fatal("explicit opt-out ignored")
	}
}
