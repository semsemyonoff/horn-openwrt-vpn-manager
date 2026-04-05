// Package config handles loading and validating the unified config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

const DefaultPath = "/etc/horn-vpn-manager/config.json"

type Config struct {
	Fetch         Fetch                    `json:"fetch"`
	Singbox       Singbox                  `json:"singbox"`
	Routing       Routing                  `json:"routing"`
	Subscriptions map[string]*Subscription `json:"subscriptions"`
}

// Singbox holds settings used when generating sing-box config.
type Singbox struct {
	LogLevel string `json:"log_level"`
	TestURL  string `json:"test_url"`
	Template string `json:"template"`
}

// Subscription defines a single subscription entry.
type Subscription struct {
	Name      string             `json:"name"`
	URL       string             `json:"url"`
	Default   bool               `json:"default"`
	Enabled   *bool              `json:"enabled"`
	Exclude   []string           `json:"exclude"`
	Interval  string             `json:"interval"`
	Tolerance int                `json:"tolerance"`
	Route     *SubscriptionRoute `json:"route,omitempty"`
}

// IsEnabled returns true if the subscription is not explicitly disabled.
func (s *Subscription) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// SubscriptionRoute holds per-subscription routing policy.
type SubscriptionRoute struct {
	Domains    []string `json:"domains"`
	DomainURLs []string `json:"domain_urls"`
	IPCIDRs    []string `json:"ip_cidrs"`
	IPURLs     []string `json:"ip_urls"`
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
	hasRouting := c.Routing.Domains.URL != "" || len(c.Routing.Subnets.URLs) > 0
	hasSubs := len(c.Subscriptions) > 0
	if !hasRouting && !hasSubs {
		return fmt.Errorf("config must have at least routing (domains.url or subnets.urls) or subscriptions configured")
	}
	return nil
}
