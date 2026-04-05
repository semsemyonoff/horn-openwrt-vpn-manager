// Package config handles loading and validating the unified config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

const DefaultPath = "/etc/horn-vpn-manager/config.json"

type Config struct {
	Fetch   Fetch   `json:"fetch"`
	Routing Routing `json:"routing"`
}

type Fetch struct {
	Retries        int `json:"retries"`
	TimeoutSeconds int `json:"timeout_seconds"`
	Parallelism    int `json:"parallelism"`
}

type Routing struct {
	Domains Domains `json:"domains"`
	Subnets Subnets `json:"subnets"`
}

type Domains struct {
	URL string `json:"url"`
}

type Subnets struct {
	URLs       []string `json:"urls"`
	ManualFile string   `json:"manual_file"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Fetch.Retries <= 0 {
		c.Fetch.Retries = 3
	}
	if c.Fetch.TimeoutSeconds <= 0 {
		c.Fetch.TimeoutSeconds = 15
	}
	if c.Fetch.Parallelism <= 0 {
		c.Fetch.Parallelism = 2
	}
	if c.Routing.Subnets.ManualFile == "" {
		c.Routing.Subnets.ManualFile = "/etc/horn-vpn-manager/lists/manual-ip.lst"
	}
}

func (c *Config) validate() error {
	if c.Routing.Domains.URL == "" && len(c.Routing.Subnets.URLs) == 0 {
		return fmt.Errorf("routing: at least one of domains.url or subnets.urls must be configured")
	}
	return nil
}
