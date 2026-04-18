package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
)

// testAccessKey is a valid Base64 string that decodes to 32 bytes.
const testAccessKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func validConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Auth: config.AuthConfig{
			AccessKey: testAccessKey,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_Defaults(t *testing.T) {
	path := writeTempConfig(t, "auth:\n  accessKey: "+testAccessKey+"\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default port: got %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("default host: got %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level: got %q, want info", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("default log format: got %q, want text", cfg.Logging.Format)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, ": invalid: yaml: :")
	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoad_FullConfig(t *testing.T) {
	content := `
server:
  host: "127.0.0.1"
  port: 9090
auth:
  accessKey: "` + testAccessKey + `"
logging:
  level: "debug"
  format: "json"
hubs:
  - name: "chat"
    eventHandlerUrl: "http://localhost:3000/eventhandler"
  - name: "notifications"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("host: got %q, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("port: got %d, want 9090", cfg.Server.Port)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("log level: got %q, want debug", cfg.Logging.Level)
	}
	if len(cfg.Hubs) != 2 {
		t.Errorf("hubs: got %d, want 2", len(cfg.Hubs))
	}
	if cfg.Hubs[0].Name != "chat" {
		t.Errorf("hub[0].name: got %q, want chat", cfg.Hubs[0].Name)
	}
	if cfg.Hubs[0].EventHandlerURL != "http://localhost:3000/eventhandler" {
		t.Errorf("hub[0].eventHandlerUrl: got %q", cfg.Hubs[0].EventHandlerURL)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("WEBPUBSUB_PORT", "9000")
	t.Setenv("WEBPUBSUB_LOG_LEVEL", "warn")
	t.Setenv("WEBPUBSUB_ACCESS_KEY", testAccessKey)

	cfg, err := config.Load("/nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != 9000 {
		t.Errorf("env port: got %d, want 9000", cfg.Server.Port)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("env log level: got %q, want warn", cfg.Logging.Level)
	}
	if cfg.Auth.AccessKey != testAccessKey {
		t.Errorf("env access key not applied")
	}
}

func TestLoad_EnvInvalidPort(t *testing.T) {
	t.Setenv("WEBPUBSUB_PORT", "notanumber")

	cfg, err := config.Load("/nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// invalid env port is silently ignored; default remains
	if cfg.Server.Port != 8080 {
		t.Errorf("invalid env port should keep default 8080, got %d", cfg.Server.Port)
	}
}

func TestApplyFlags(t *testing.T) {
	cfg := validConfig()
	cfg.ApplyFlags(9090, testAccessKey, "debug")

	if cfg.Server.Port != 9090 {
		t.Errorf("port: got %d, want 9090", cfg.Server.Port)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("log level: got %q, want debug", cfg.Logging.Level)
	}
}

func TestApplyFlags_ZeroValues(t *testing.T) {
	cfg := validConfig()
	cfg.ApplyFlags(0, "", "")

	if cfg.Server.Port != 8080 {
		t.Error("port should not change when flag is zero")
	}
	if cfg.Auth.AccessKey != testAccessKey {
		t.Error("accessKey should not change when flag is empty")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:    "valid config",
			mutate:  func(c *config.Config) {},
			wantErr: "",
		},
		{
			name:    "missing accessKey",
			mutate:  func(c *config.Config) { c.Auth.AccessKey = "" },
			wantErr: "accessKey is required",
		},
		{
			name:    "non-base64 accessKey",
			mutate:  func(c *config.Config) { c.Auth.AccessKey = "not-base64!!!" },
			wantErr: "valid Base64",
		},
		{
			name:    "too short accessKey",
			mutate:  func(c *config.Config) { c.Auth.AccessKey = "c2hvcnQ=" }, // "short" = 5 bytes
			wantErr: "at least 32 bytes",
		},
		{
			name:    "port 0",
			mutate:  func(c *config.Config) { c.Server.Port = 0 },
			wantErr: "port must be between",
		},
		{
			name:    "port 99999",
			mutate:  func(c *config.Config) { c.Server.Port = 99999 },
			wantErr: "port must be between",
		},
		{
			name:    "invalid log level",
			mutate:  func(c *config.Config) { c.Logging.Level = "verbose" },
			wantErr: "logging.level",
		},
		{
			name:    "invalid log format",
			mutate:  func(c *config.Config) { c.Logging.Format = "xml" },
			wantErr: "logging.format",
		},
		{
			name: "hub missing name",
			mutate: func(c *config.Config) {
				c.Hubs = []config.HubConfig{{Name: ""}}
			},
			wantErr: "name is required",
		},
		{
			name: "duplicate hub name",
			mutate: func(c *config.Config) {
				c.Hubs = []config.HubConfig{{Name: "chat"}, {Name: "chat"}}
			},
			wantErr: "duplicate hub name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}
