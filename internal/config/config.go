// Package config loads and validates the server configuration from YAML.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the server configuration loaded from YAML.
type Config struct {
	GRPCListen         string   `yaml:"grpc_listen"`
	HTTPListen         string   `yaml:"http_listen"`
	PostgresDSN        string   `yaml:"postgres_dsn"`
	VictoriaMetricsURL string   `yaml:"victoriametrics_url"`
	LokiURL            string   `yaml:"loki_url"`
	DataDir            string   `yaml:"data_dir"`
	GRPCServerDNSNames []string `yaml:"grpc_server_dns_names"`
	APIKey             string   `yaml:"api_key"`
	OrgID              string   `yaml:"org_id"`
	AlertWebhookURL    string   `yaml:"alert_webhook_url"`
	WatchdogEnabled    bool     `yaml:"watchdog_enabled"`
}

// Load reads the YAML file at path, applies defaults, and validates required fields.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", path, err)
	}

	if cfg.GRPCListen == "" {
		cfg.GRPCListen = ":8443"
	}
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = ":8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./tmp/data"
	}
	if len(cfg.GRPCServerDNSNames) == 0 {
		cfg.GRPCServerDNSNames = []string{"localhost"}
	}

	if cfg.OrgID == "" {
		cfg.OrgID = "default"
	}
	// api_key has no default — it's a secret, must be set explicitly
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("config: api_key is required")
	}

	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("config: postgres_dsn is required")
	}
	if cfg.VictoriaMetricsURL == "" {
		return nil, fmt.Errorf("config: victoriametrics_url is required")
	}
	if cfg.LokiURL == "" {
		return nil, fmt.Errorf("config: loki_url is required")
	}

	return &cfg, nil
}
