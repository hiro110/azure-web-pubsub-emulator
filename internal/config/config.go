package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Auth    AuthConfig    `yaml:"auth"`
	Logging LoggingConfig `yaml:"logging"`
	Hubs    []HubConfig   `yaml:"hubs"`
}

type ServerConfig struct {
	Host string    `yaml:"host"`
	Port int       `yaml:"port"`
	TLS  TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Enabled      bool     `yaml:"enabled"`
	AutoGenerate bool     `yaml:"autoGenerate"`
	CertFile     string   `yaml:"certFile"`
	KeyFile      string   `yaml:"keyFile"`
	Hosts        []string `yaml:"hosts"`
}

type AuthConfig struct {
	AccessKey string `yaml:"accessKey"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type HubConfig struct {
	Name            string       `yaml:"name"`
	EventHandlerURL string       `yaml:"eventHandlerUrl"`
	Events          EventsConfig `yaml:"events"`
}

type EventsConfig struct {
	System []string `yaml:"system"`
	User   []string `yaml:"user"`
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 7290,
			TLS: TLSConfig{
				AutoGenerate: true,
				Hosts:        []string{"localhost", "127.0.0.1"},
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// Load reads the YAML config file at path (missing file is not an error),
// then applies environment variable overrides.
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	applyEnvOverrides(&cfg)
	return &cfg, nil
}

// applyEnvOverrides applies WEBPUBSUB_* environment variables.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("WEBPUBSUB_ACCESS_KEY"); v != "" {
		cfg.Auth.AccessKey = v
	}
	if v := os.Getenv("WEBPUBSUB_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("WEBPUBSUB_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("WEBPUBSUB_LOG_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}
	if v := os.Getenv("WEBPUBSUB_TLS_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Server.TLS.Enabled = b
		} else {
			fmt.Fprintf(os.Stderr, "warning: WEBPUBSUB_TLS_ENABLED=%q is not a valid bool (use true/false/1/0); ignoring\n", v)
		}
	}
	if v := os.Getenv("WEBPUBSUB_TLS_CERT_FILE"); v != "" {
		cfg.Server.TLS.CertFile = v
	}
	if v := os.Getenv("WEBPUBSUB_TLS_KEY_FILE"); v != "" {
		cfg.Server.TLS.KeyFile = v
	}
}

// ApplyFlags overrides config values with CLI flag values (non-zero/non-empty only).
func (c *Config) ApplyFlags(port int, accessKey, logLevel string, tlsEnabled *bool, tlsCert, tlsKey string) {
	if port != 0 {
		c.Server.Port = port
	}
	if accessKey != "" {
		c.Auth.AccessKey = accessKey
	}
	if logLevel != "" {
		c.Logging.Level = logLevel
	}
	if tlsEnabled != nil {
		c.Server.TLS.Enabled = *tlsEnabled
	}
	if tlsCert != "" {
		c.Server.TLS.CertFile = tlsCert
	}
	if tlsKey != "" {
		c.Server.TLS.KeyFile = tlsKey
	}
}

// Validate returns an error if the configuration is invalid.
func (c *Config) Validate() error {
	if c.Auth.AccessKey == "" {
		return fmt.Errorf("auth.accessKey is required")
	}
	decoded, err := base64.StdEncoding.DecodeString(c.Auth.AccessKey)
	if err != nil {
		return fmt.Errorf("auth.accessKey must be a valid Base64 string: %w", err)
	}
	if len(decoded) < 32 {
		return fmt.Errorf("auth.accessKey must decode to at least 32 bytes (got %d)", len(decoded))
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535 (got %d)", c.Server.Port)
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("logging.level must be one of debug|info|warn|error (got %q)", c.Logging.Level)
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("logging.format must be one of text|json (got %q)", c.Logging.Format)
	}

	if c.Server.TLS.Enabled {
		hasCert := c.Server.TLS.CertFile != ""
		hasKey := c.Server.TLS.KeyFile != ""
		if hasCert != hasKey {
			return fmt.Errorf("server.tls.certFile and server.tls.keyFile must both be set or both be empty")
		}
		if !c.Server.TLS.AutoGenerate && !hasCert {
			return fmt.Errorf("server.tls.autoGenerate is false but no certFile/keyFile provided")
		}
	}

	seen := map[string]bool{}
	for i, hub := range c.Hubs {
		if hub.Name == "" {
			return fmt.Errorf("hubs[%d].name is required", i)
		}
		if seen[hub.Name] {
			return fmt.Errorf("duplicate hub name %q", hub.Name)
		}
		seen[hub.Name] = true
	}

	return nil
}
