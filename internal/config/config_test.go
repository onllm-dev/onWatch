package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfig_LoadsFromEnv(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key_123")
	os.Setenv("SYNTRACK_POLL_INTERVAL", "120")
	os.Setenv("SYNTRACK_PORT", "8080")
	os.Setenv("SYNTRACK_ADMIN_USER", "myuser")
	os.Setenv("SYNTRACK_ADMIN_PASS", "mypass")
	os.Setenv("SYNTRACK_DB_PATH", "/tmp/test.db")
	os.Setenv("SYNTRACK_LOG_LEVEL", "debug")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.APIKey != "syn_test_key_123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "syn_test_key_123")
	}
	if cfg.PollInterval != 120*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 120*time.Second)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if cfg.AdminUser != "myuser" {
		t.Errorf("AdminUser = %q, want %q", cfg.AdminUser, "myuser")
	}
	if cfg.AdminPass != "mypass" {
		t.Errorf("AdminPass = %q, want %q", cfg.AdminPass, "mypass")
	}
	if cfg.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/tmp/test.db")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestConfig_DefaultValues(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key_123")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 60*time.Second)
	}
	if cfg.Port != 8932 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8932)
	}
	if cfg.AdminUser != "admin" {
		t.Errorf("AdminUser = %q, want %q", cfg.AdminUser, "admin")
	}
	if cfg.AdminPass != "changeme" {
		t.Errorf("AdminPass = %q, want %q", cfg.AdminPass, "changeme")
	}
	if cfg.DBPath != "./syntrack.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "./syntrack.db")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestConfig_ValidatesAPIKey_Required(t *testing.T) {
	os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with empty API key")
	}
}

func TestConfig_ValidatesAPIKey_Format(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{"valid prefix", "syn_valid_key", false},
		{"valid with numbers", "syn_12345", false},
		{"valid long", "syn_abcdefghijklmnopqrstuvwxyz1234567890", false},
		{"missing prefix", "invalid_key", true},
		{"empty", "", true},
		{"wrong prefix", "api_test_key", true},
		{"syn only", "syn_", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Clearenv()
			os.Setenv("SYNTHETIC_API_KEY", tt.apiKey)
			defer os.Clearenv()

			_, err := Load()
			if tt.wantErr && err == nil {
				t.Errorf("Load() should fail for API key %q", tt.apiKey)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Load() should succeed for API key %q, got: %v", tt.apiKey, err)
			}
		})
	}
}

func TestConfig_ValidatesInterval_Minimum(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("SYNTRACK_POLL_INTERVAL", "5")
	defer os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with interval < 10s")
	}
}

func TestConfig_ValidatesInterval_Maximum(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("SYNTRACK_POLL_INTERVAL", "7200")
	defer os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with interval > 3600s")
	}
}

func TestConfig_ValidatesPort_Range(t *testing.T) {
	tests := []struct {
		name   string
		port   string
		wantOK bool
	}{
		{"valid port", "8932", true},
		{"min valid", "1024", true},
		{"max valid", "65535", true},
		{"too low", "1023", false},
		{"too high", "65536", false},
		{"privileged", "80", false},
		{"negative", "-1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Clearenv()
			os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
			os.Setenv("SYNTRACK_PORT", tt.port)
			defer os.Clearenv()

			_, err := Load()
			if tt.wantOK && err != nil {
				t.Errorf("Load() should succeed for port %s, got: %v", tt.port, err)
			}
			if !tt.wantOK && err == nil {
				t.Errorf("Load() should fail for port %s", tt.port)
			}
		})
	}
}

func TestConfig_RedactsAPIKey(t *testing.T) {
	cfg := &Config{
		APIKey: "syn_secret_api_key_xyz789",
	}

	str := cfg.String()
	if strings.Contains(str, "syn_secret_api_key_xyz789") {
		t.Error("String() should not contain full API key")
	}
}

func TestConfig_DebugMode_Default(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.DebugMode {
		t.Error("DebugMode should default to false")
	}
}

func TestConfig_LoadWithArgs_FlagOverridesEnv(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("SYNTRACK_POLL_INTERVAL", "120")
	os.Setenv("SYNTRACK_PORT", "8080")
	os.Setenv("SYNTRACK_DB_PATH", "/tmp/env.db")
	defer os.Clearenv()

	cfg, err := loadWithArgs([]string{"--interval", "30", "--port", "9000", "--db", "/tmp/flag.db"})
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 30*time.Second)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9000)
	}
	if cfg.DBPath != "/tmp/flag.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/tmp/flag.db")
	}
}

func TestConfig_LoadWithArgs_EqualsSyntax(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := loadWithArgs([]string{"--interval=45", "--port=7777"})
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.PollInterval != 45*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 45*time.Second)
	}
	if cfg.Port != 7777 {
		t.Errorf("Port = %d, want %d", cfg.Port, 7777)
	}
}

func TestConfig_DebugMode_Flag(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := loadWithArgs([]string{"--debug"})
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !cfg.DebugMode {
		t.Error("DebugMode should be true when --debug flag is set")
	}
}

func TestConfig_LogWriter(t *testing.T) {
	cfg := &Config{
		DebugMode: true,
	}
	writer, err := cfg.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter() failed: %v", err)
	}
	if writer != os.Stdout {
		t.Error("Debug mode should return os.Stdout")
	}

	cfg = &Config{
		DebugMode: false,
		DBPath:    "/tmp/test_syntrack.db",
	}
	writer, err = cfg.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter() failed: %v", err)
	}
	if writer == os.Stdout {
		t.Error("Background mode should not return os.Stdout")
	}
}
