package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfig_LoadsFromEnv(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key_123")
	os.Setenv("ONWATCH_POLL_INTERVAL", "120")
	os.Setenv("ONWATCH_PORT", "8080")
	os.Setenv("ONWATCH_ADMIN_USER", "myuser")
	os.Setenv("ONWATCH_ADMIN_PASS", "mypass")
	os.Setenv("ONWATCH_DB_PATH", "/tmp/test.db")
	os.Setenv("ONWATCH_LOG_LEVEL", "debug")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.SyntheticAPIKey != "syn_test_key_123" {
		t.Errorf("SyntheticAPIKey = %q, want %q", cfg.SyntheticAPIKey, "syn_test_key_123")
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

func TestConfig_LoadsZaiFromEnv(t *testing.T) {
	os.Setenv("ZAI_API_KEY", "zai_test_key_456")
	os.Setenv("ZAI_BASE_URL", "https://custom.z.ai/api")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ZaiAPIKey != "zai_test_key_456" {
		t.Errorf("ZaiAPIKey = %q, want %q", cfg.ZaiAPIKey, "zai_test_key_456")
	}
	if cfg.ZaiBaseURL != "https://custom.z.ai/api" {
		t.Errorf("ZaiBaseURL = %q, want %q", cfg.ZaiBaseURL, "https://custom.z.ai/api")
	}
}

func TestConfig_ZaiDefaults(t *testing.T) {
	os.Setenv("ZAI_API_KEY", "zai_test_key")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ZaiBaseURL != "https://api.z.ai/api" {
		t.Errorf("ZaiBaseURL = %q, want default %q", cfg.ZaiBaseURL, "https://api.z.ai/api")
	}
}

func TestConfig_DefaultValues(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key_123")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.PollInterval != 120*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 120*time.Second)
	}
	if cfg.Port != 9211 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9211)
	}
	if cfg.AdminUser != "admin" {
		t.Errorf("AdminUser = %q, want %q", cfg.AdminUser, "admin")
	}
	if cfg.AdminPass != "changeme" {
		t.Errorf("AdminPass = %q, want %q", cfg.AdminPass, "changeme")
	}
	// Default DB path depends on HOME availability
	home, homeErr := os.UserHomeDir()
	if homeErr == nil && home != "" {
		expectedDBPath := filepath.Join(home, ".onwatch", "data", "onwatch.db")
		if cfg.DBPath != expectedDBPath {
			t.Errorf("DBPath = %q, want %q", cfg.DBPath, expectedDBPath)
		}
	} else {
		if cfg.DBPath != "./onwatch.db" {
			t.Errorf("DBPath = %q, want %q (HOME unavailable fallback)", cfg.DBPath, "./onwatch.db")
		}
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestConfig_OnlySyntheticProvider(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	providers := cfg.AvailableProviders()
	if len(providers) != 1 || providers[0] != "synthetic" {
		t.Errorf("AvailableProviders() = %v, want [synthetic]", providers)
	}

	if !cfg.HasProvider("synthetic") {
		t.Error("HasProvider('synthetic') should be true")
	}

	if cfg.HasProvider("zai") {
		t.Error("HasProvider('zai') should be false")
	}
}

func TestConfig_OnlyZaiProvider(t *testing.T) {
	os.Setenv("ZAI_API_KEY", "zai_test_key")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	providers := cfg.AvailableProviders()
	if len(providers) != 1 || providers[0] != "zai" {
		t.Errorf("AvailableProviders() = %v, want [zai]", providers)
	}

	if cfg.HasProvider("synthetic") {
		t.Error("HasProvider('synthetic') should be false")
	}

	if !cfg.HasProvider("zai") {
		t.Error("HasProvider('zai') should be true")
	}
}

func TestConfig_BothProviders(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ZAI_API_KEY", "zai_test_key")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	providers := cfg.AvailableProviders()
	if len(providers) != 2 {
		t.Errorf("AvailableProviders() = %v, want 2 providers", providers)
	}

	if !cfg.HasProvider("synthetic") {
		t.Error("HasProvider('synthetic') should be true")
	}

	if !cfg.HasProvider("zai") {
		t.Error("HasProvider('zai') should be true")
	}
}

func TestConfig_ValidatesNoProvidersConfigured(t *testing.T) {
	os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when no providers are configured")
	}

	if !strings.Contains(err.Error(), "at least one provider") {
		t.Errorf("Error message should mention 'at least one provider', got: %v", err)
	}
}

func TestConfig_ValidatesSyntheticAPIKey_Format(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{"valid prefix", "syn_valid_key", false},
		{"valid with numbers", "syn_12345", false},
		{"valid long", "syn_abcdefghijklmnopqrstuvwxyz1234567890", false},
		{"missing prefix", "invalid_key", true},
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
	os.Setenv("ZAI_API_KEY", "zai_test_key")
	os.Setenv("ONWATCH_POLL_INTERVAL", "5")
	defer os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail with interval < 10s")
	}
}

func TestConfig_ValidatesInterval_Maximum(t *testing.T) {
	os.Setenv("ZAI_API_KEY", "zai_test_key")
	os.Setenv("ONWATCH_POLL_INTERVAL", "7200")
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
			os.Setenv("ZAI_API_KEY", "zai_test_key")
			os.Setenv("ONWATCH_PORT", tt.port)
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

func TestConfig_RedactsSyntheticAPIKey(t *testing.T) {
	cfg := &Config{
		SyntheticAPIKey: "syn_secret_api_key_xyz789",
	}

	str := cfg.String()
	if strings.Contains(str, "syn_secret_api_key_xyz789") {
		t.Error("String() should not contain full Synthetic API key")
	}
}

func TestConfig_RedactsZaiAPIKey(t *testing.T) {
	cfg := &Config{
		ZaiAPIKey: "zai_secret_key_abc123",
	}

	str := cfg.String()
	if strings.Contains(str, "zai_secret_key_abc123") {
		t.Error("String() should not contain full Z.ai API key")
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
	os.Setenv("ONWATCH_POLL_INTERVAL", "120")
	os.Setenv("ONWATCH_PORT", "8080")
	os.Setenv("ONWATCH_DB_PATH", "/tmp/env.db")
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
		DBPath:    "/tmp/test_onwatch.db",
	}
	writer, err = cfg.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter() failed: %v", err)
	}
	if writer == os.Stdout {
		t.Error("Background mode should not return os.Stdout")
	}
}

func TestConfig_LoadsAnthropicFromEnv(t *testing.T) {
	os.Setenv("ANTHROPIC_TOKEN", "sk-ant-test-token-123")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.AnthropicToken != "sk-ant-test-token-123" {
		t.Errorf("AnthropicToken = %q, want %q", cfg.AnthropicToken, "sk-ant-test-token-123")
	}
}

func TestConfig_OnlyAnthropicProvider(t *testing.T) {
	os.Setenv("ANTHROPIC_TOKEN", "sk-ant-test-token")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	providers := cfg.AvailableProviders()
	if len(providers) != 1 || providers[0] != "anthropic" {
		t.Errorf("AvailableProviders() = %v, want [anthropic]", providers)
	}

	if !cfg.HasProvider("anthropic") {
		t.Error("HasProvider('anthropic') should be true")
	}

	if cfg.HasProvider("synthetic") {
		t.Error("HasProvider('synthetic') should be false")
	}

	if cfg.HasProvider("zai") {
		t.Error("HasProvider('zai') should be false")
	}
}

func TestConfig_AnthropicWithOtherProviders(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ANTHROPIC_TOKEN", "sk-ant-test-token")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	providers := cfg.AvailableProviders()
	if len(providers) != 2 {
		t.Errorf("AvailableProviders() = %v, want 2 providers", providers)
	}

	if !cfg.HasProvider("synthetic") {
		t.Error("HasProvider('synthetic') should be true")
	}
	if !cfg.HasProvider("anthropic") {
		t.Error("HasProvider('anthropic') should be true")
	}
}

func TestConfig_HasMultipleProviders(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantBoth bool
	}{
		{
			name:     "no providers",
			cfg:      Config{},
			wantBoth: false,
		},
		{
			name:     "synthetic only",
			cfg:      Config{SyntheticAPIKey: "syn_test"},
			wantBoth: false,
		},
		{
			name:     "anthropic only",
			cfg:      Config{AnthropicToken: "sk-ant-test"},
			wantBoth: false,
		},
		{
			name:     "synthetic and zai",
			cfg:      Config{SyntheticAPIKey: "syn_test", ZaiAPIKey: "zai_test"},
			wantBoth: true,
		},
		{
			name:     "synthetic and anthropic",
			cfg:      Config{SyntheticAPIKey: "syn_test", AnthropicToken: "sk-ant-test"},
			wantBoth: true,
		},
		{
			name:     "all three",
			cfg:      Config{SyntheticAPIKey: "syn_test", ZaiAPIKey: "zai_test", AnthropicToken: "sk-ant-test"},
			wantBoth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.HasMultipleProviders()
			if got != tt.wantBoth {
				t.Errorf("HasMultipleProviders() = %v, want %v", got, tt.wantBoth)
			}
			// HasBothProviders should be backward-compatible alias
			gotAlias := tt.cfg.HasBothProviders()
			if gotAlias != tt.wantBoth {
				t.Errorf("HasBothProviders() = %v, want %v", gotAlias, tt.wantBoth)
			}
		})
	}
}

func TestConfig_RedactsAnthropicToken(t *testing.T) {
	cfg := &Config{
		AnthropicToken: "sk-ant-secret-token-abc123",
	}

	str := cfg.String()
	if strings.Contains(str, "sk-ant-secret-token-abc123") {
		t.Error("String() should not contain full Anthropic token")
	}
	if !strings.Contains(str, "AnthropicToken:") {
		t.Error("String() should contain AnthropicToken field")
	}
}

func TestConfig_RedactsAnthropicToken_AutoDetected(t *testing.T) {
	cfg := &Config{
		AnthropicToken:     "sk-ant-auto-token-xyz",
		AnthropicAutoToken: true,
	}

	str := cfg.String()
	if !strings.Contains(str, "AnthropicAutoToken: true") {
		t.Error("String() should contain AnthropicAutoToken: true when auto-detected")
	}
}

func TestConfig_AvailableProviders_Empty(t *testing.T) {
	cfg := &Config{}
	providers := cfg.AvailableProviders()
	if len(providers) != 0 {
		t.Errorf("AvailableProviders() = %v, want empty slice", providers)
	}
}

func TestConfig_HasProvider_Codex(t *testing.T) {
	cfg := &Config{CodexToken: "oauth_access_token"}
	if !cfg.HasProvider("codex") {
		t.Error("HasProvider('codex') should be true when CodexToken is set")
	}
}

func TestConfig_AvailableProviders_IncludesCodex(t *testing.T) {
	cfg := &Config{CodexToken: "oauth_access_token"}
	providers := cfg.AvailableProviders()
	if len(providers) != 1 || providers[0] != "codex" {
		t.Fatalf("AvailableProviders() = %v, want [codex]", providers)
	}
}

func TestConfig_HasMultipleProviders_WithCodex(t *testing.T) {
	cfg := &Config{CodexToken: "oauth_access_token", SyntheticAPIKey: "syn_test"}
	if !cfg.HasMultipleProviders() {
		t.Fatal("HasMultipleProviders() should be true for codex + synthetic")
	}
}

func TestConfig_HasProvider_Unknown(t *testing.T) {
	cfg := &Config{
		SyntheticAPIKey: "syn_test",
	}

	if cfg.HasProvider("unknown") {
		t.Error("HasProvider('unknown') should be false for unknown provider")
	}
}

func TestConfig_RedactAPIKey_EdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		prefix         string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:           "empty key",
			key:            "",
			prefix:         "syn_",
			wantContains:   []string{"(not set)"},
			wantNotContain: []string{"***"},
		},
		{
			name:           "short synthetic key",
			key:            "syn_ab",
			prefix:         "syn_",
			wantContains:   []string{"syn_***...***"},
			wantNotContain: []string{"syn_ab"},
		},
		{
			name:           "long synthetic key",
			key:            "syn_abcdefghijklmnopqrstuvwxyz",
			prefix:         "syn_",
			wantContains:   []string{"syn_abcd***...***xyz"},
			wantNotContain: []string{"syn_abcdefghijklmnopqrstuvwxyz"},
		},
		{
			name:           "key without expected prefix",
			key:            "some_random_key",
			prefix:         "syn_",
			wantContains:   []string{"***...***"},
			wantNotContain: []string{"some_random_key"},
		},
		{
			name:           "zai key without prefix check",
			key:            "zai_test_key_123",
			prefix:         "",
			wantContains:   []string{"zai_***...***123"},
			wantNotContain: []string{"zai_test_key_123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactAPIKey(tt.key, tt.prefix)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("redactAPIKey() = %q, should contain %q", result, want)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("redactAPIKey() = %q, should NOT contain %q", result, notWant)
				}
			}
		})
	}
}

func TestConfig_String_ContainsProviders(t *testing.T) {
	cfg := &Config{
		SyntheticAPIKey: "syn_test_key_12345",
		ZaiAPIKey:       "zai_test_key_67890",
		PollInterval:    60 * time.Second,
		Port:            9211,
		AdminUser:       "admin",
		AdminPass:       "secret",
		DBPath:          "./test.db",
		LogLevel:        "info",
		DebugMode:       false,
	}

	str := cfg.String()

	// Check providers list is shown
	if !strings.Contains(str, "Providers:") {
		t.Error("String() should contain Providers list")
	}

	// Check both API keys are redacted
	if strings.Contains(str, "syn_test_key_12345") {
		t.Error("String() should not contain full Synthetic API key")
	}

	if strings.Contains(str, "zai_test_key_67890") {
		t.Error("String() should not contain full Z.ai API key")
	}

	// Check ZaiBaseURL is shown
	if !strings.Contains(str, "ZaiBaseURL:") {
		t.Error("String() should contain ZaiBaseURL")
	}
}

func TestConfig_HasProvider_Copilot(t *testing.T) {
	cfg := &Config{CopilotToken: "ghp_test_token"}
	if !cfg.HasProvider("copilot") {
		t.Error("HasProvider('copilot') should be true when CopilotToken is set")
	}
	if cfg.HasProvider("antigravity") {
		t.Error("HasProvider('antigravity') should be false when only CopilotToken is set")
	}
}

func TestConfig_HasProvider_Antigravity(t *testing.T) {
	cfg := &Config{AntigravityEnabled: true}
	if !cfg.HasProvider("antigravity") {
		t.Error("HasProvider('antigravity') should be true when AntigravityEnabled is set")
	}
}

func TestConfig_AvailableProviders_AllSix(t *testing.T) {
	cfg := &Config{
		AnthropicToken:     "sk-ant-test",
		SyntheticAPIKey:    "syn_test",
		ZaiAPIKey:          "zai_test",
		CopilotToken:       "ghp_test",
		CodexToken:         "codex_test",
		AntigravityEnabled: true,
	}
	providers := cfg.AvailableProviders()
	if len(providers) != 6 {
		t.Errorf("AvailableProviders() = %v, want 6 providers", providers)
	}
	if !cfg.HasMultipleProviders() {
		t.Error("HasMultipleProviders() should be true with 6 providers")
	}
}

func TestConfig_IsDockerEnvironment_DockerContainer(t *testing.T) {
	os.Setenv("DOCKER_CONTAINER", "true")
	defer os.Unsetenv("DOCKER_CONTAINER")

	cfg := &Config{}
	if !cfg.IsDockerEnvironment() {
		t.Error("IsDockerEnvironment() should return true when DOCKER_CONTAINER is set")
	}
}

func TestConfig_IsDockerEnvironment_Kubernetes(t *testing.T) {
	os.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	defer os.Unsetenv("KUBERNETES_SERVICE_HOST")

	cfg := &Config{}
	if !cfg.IsDockerEnvironment() {
		t.Error("IsDockerEnvironment() should return true when KUBERNETES_SERVICE_HOST is set")
	}
}

func TestConfig_IsDockerEnvironment_NotDocker(t *testing.T) {
	// Ensure none of the Docker env vars are set
	os.Unsetenv("DOCKER_CONTAINER")
	os.Unsetenv("KUBERNETES_SERVICE_HOST")

	cfg := &Config{}
	// On a normal dev machine without /.dockerenv, this should be false
	// (We cannot guarantee /.dockerenv doesn't exist, but on dev machines it won't)
	result := cfg.IsDockerEnvironment()
	_ = result // Just exercise the code path; result depends on host
}

func TestConfig_LogWriter_TestMode(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		DebugMode: false,
		TestMode:  true,
		DBPath:    filepath.Join(tmpDir, "test.db"),
	}
	writer, err := cfg.LogWriter()
	if err != nil {
		t.Fatalf("LogWriter() failed: %v", err)
	}
	if writer == os.Stdout {
		t.Error("TestMode background should not return os.Stdout")
	}
	// Verify the test log file was created
	logPath := filepath.Join(tmpDir, ".onwatch-test.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("Expected log file at %s", logPath)
	}
}

func TestConfig_LoadWithArgs_TestFlag(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := loadWithArgs([]string{"--test"})
	if err != nil {
		t.Fatalf("loadWithArgs() failed: %v", err)
	}
	if !cfg.TestMode {
		t.Error("TestMode should be true when --test flag is set")
	}
}

func TestConfig_LoadWithArgs_DbEqualsSyntax(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	defer os.Clearenv()

	cfg, err := loadWithArgs([]string{"--db=/tmp/equals.db"})
	if err != nil {
		t.Fatalf("loadWithArgs() failed: %v", err)
	}
	if cfg.DBPath != "/tmp/equals.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/tmp/equals.db")
	}
}

func TestConfig_LoadAntigravityFromEnv(t *testing.T) {
	os.Setenv("ANTIGRAVITY_ENABLED", "true")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !cfg.AntigravityEnabled {
		t.Error("AntigravityEnabled should be true when ANTIGRAVITY_ENABLED=true")
	}
	if !cfg.HasProvider("antigravity") {
		t.Error("HasProvider('antigravity') should be true")
	}
}

func TestConfig_LoadCopilotFromEnv(t *testing.T) {
	os.Setenv("COPILOT_TOKEN", "ghp_test_copilot_token")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.CopilotToken != "ghp_test_copilot_token" {
		t.Errorf("CopilotToken = %q, want %q", cfg.CopilotToken, "ghp_test_copilot_token")
	}
}

func TestConfig_SecureCookiesFromEnv(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_SECURE_COOKIES", "true")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !cfg.SecureCookies {
		t.Error("SecureCookies should be true when ONWATCH_SECURE_COOKIES=true")
	}
}

func TestConfig_SessionIdleTimeoutFromEnv(t *testing.T) {
	os.Setenv("SYNTHETIC_API_KEY", "syn_test_key")
	os.Setenv("ONWATCH_SESSION_IDLE_TIMEOUT", "300")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.SessionIdleTimeout != 300*time.Second {
		t.Errorf("SessionIdleTimeout = %v, want %v", cfg.SessionIdleTimeout, 300*time.Second)
	}
}

func TestConfig_IsDefaultPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		want     bool
	}{
		{"default password", "changeme", true},
		{"custom password", "mysecretpassword", false},
		{"empty password", "", false},
		{"similar password", "changeme!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AdminPass: tt.password}
			if got := cfg.IsDefaultPassword(); got != tt.want {
				t.Errorf("IsDefaultPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOnwatchEnvFile_WithOnwatchKeys(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "contains SYNTHETIC_API_KEY",
			content: "SYNTHETIC_API_KEY=syn_test123\n",
			want:    true,
		},
		{
			name:    "contains ANTHROPIC_TOKEN",
			content: "ANTHROPIC_TOKEN=sk-ant-test\n",
			want:    true,
		},
		{
			name:    "contains CODEX_TOKEN",
			content: "CODEX_TOKEN=oauth_token\n",
			want:    true,
		},
		{
			name:    "contains ONWATCH_ prefix",
			content: "ONWATCH_PORT=9211\nONWATCH_ADMIN_USER=admin\n",
			want:    true,
		},
		{
			name:    "contains ANTIGRAVITY_ENABLED",
			content: "ANTIGRAVITY_ENABLED=true\n",
			want:    true,
		},
		{
			name:    "generic env file without onwatch keys",
			content: "DATABASE_URL=postgres://localhost\nREDIS_URL=redis://localhost\n",
			want:    false,
		},
		{
			name:    "empty file",
			content: "",
			want:    false,
		},
		{
			name:    "comments only",
			content: "# This is a comment\n# Another comment\n",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envPath := filepath.Join(tmpDir, tt.name+".env")
			if err := os.WriteFile(envPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}
			got := isOnwatchEnvFile(envPath)
			if got != tt.want {
				t.Errorf("isOnwatchEnvFile() = %v, want %v for content: %q", got, tt.want, tt.content)
			}
		})
	}
}

func TestIsOnwatchEnvFile_NonexistentFile(t *testing.T) {
	got := isOnwatchEnvFile("/nonexistent/path/.env")
	if got {
		t.Error("isOnwatchEnvFile() should return false for nonexistent file")
	}
}

func TestLoadEnvFile_PrefersStandardLocation(t *testing.T) {
	// Save original HOME and restore after test
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	// Create temp directory structure
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	// Create ~/.onwatch/.env
	onwatchDir := filepath.Join(tmpDir, ".onwatch")
	if err := os.MkdirAll(onwatchDir, 0755); err != nil {
		t.Fatalf("Failed to create .onwatch dir: %v", err)
	}
	standardEnv := filepath.Join(onwatchDir, ".env")
	envContent := "SYNTHETIC_API_KEY=syn_from_standard_location\nONWATCH_PORT=9999\n"
	if err := os.WriteFile(standardEnv, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write standard .env: %v", err)
	}

	// Clear env and load
	os.Clearenv()
	os.Setenv("HOME", tmpDir)
	loadEnvFile()

	// Verify the standard location was loaded
	if got := os.Getenv("SYNTHETIC_API_KEY"); got != "syn_from_standard_location" {
		t.Errorf("SYNTHETIC_API_KEY = %q, want %q", got, "syn_from_standard_location")
	}
	if got := os.Getenv("ONWATCH_PORT"); got != "9999" {
		t.Errorf("ONWATCH_PORT = %q, want %q", got, "9999")
	}
}

func TestLoadEnvFile_FallsBackToLocalOnwatchEnv(t *testing.T) {
	// Save original HOME and cwd
	origHome := os.Getenv("HOME")
	origDir, _ := os.Getwd()
	defer func() {
		os.Setenv("HOME", origHome)
		os.Chdir(origDir)
	}()

	// Create temp directory with NO ~/.onwatch/.env
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	// Create local .env with onwatch-specific keys
	localDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}
	localEnv := filepath.Join(localDir, ".env")
	envContent := "ZAI_API_KEY=zai_from_local\nONWATCH_PORT=8888\n"
	if err := os.WriteFile(localEnv, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write local .env: %v", err)
	}

	// Change to local directory
	if err := os.Chdir(localDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Clear env and load
	os.Clearenv()
	os.Setenv("HOME", tmpDir)
	loadEnvFile()

	// Verify the local .env was loaded (because standard location doesn't exist)
	if got := os.Getenv("ZAI_API_KEY"); got != "zai_from_local" {
		t.Errorf("ZAI_API_KEY = %q, want %q", got, "zai_from_local")
	}
}

func TestLoadEnvFile_IgnoresNonOnwatchLocalEnv(t *testing.T) {
	// Save original HOME and cwd
	origHome := os.Getenv("HOME")
	origDir, _ := os.Getwd()
	defer func() {
		os.Setenv("HOME", origHome)
		os.Chdir(origDir)
	}()

	// Create temp directory with NO ~/.onwatch/.env
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	// Create local .env WITHOUT onwatch-specific keys (generic env file)
	localDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}
	localEnv := filepath.Join(localDir, ".env")
	// This is a generic .env file, not onwatch-specific
	envContent := "DATABASE_URL=postgres://localhost\nREDIS_URL=redis://localhost\n"
	if err := os.WriteFile(localEnv, []byte(envContent), 0644); err != nil {
		t.Fatalf("Failed to write local .env: %v", err)
	}

	// Change to local directory
	if err := os.Chdir(localDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	// Clear env and load
	os.Clearenv()
	os.Setenv("HOME", tmpDir)
	loadEnvFile()

	// Verify the local .env was NOT loaded (because it's not onwatch-specific)
	if got := os.Getenv("DATABASE_URL"); got != "" {
		t.Errorf("DATABASE_URL should be empty (generic .env should not be loaded), got %q", got)
	}
}
