// Package config handles loading and validating the unified config file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
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
	// ConnectTimeout is emitted as connect_timeout on every generated node
	// outbound. Empty means "omit the field", leaving sing-box's own default.
	ConnectTimeout string `json:"connect_timeout"`
}

// Subscription defines a single subscription entry.
//
// Nodes carries inline vless:// URIs for a self-hosted provider and is mutually
// exclusive with a non-empty URL.
type Subscription struct {
	Name      string             `json:"name"`
	URL       string             `json:"url"`
	Nodes     []string           `json:"nodes,omitempty"`
	Default   bool               `json:"default"`
	Enabled   *bool              `json:"enabled"`
	Include   []string           `json:"include"`
	Exclude   []string           `json:"exclude"`
	Interval  string             `json:"interval"`
	Tolerance int                `json:"tolerance"`
	Retries   *int               `json:"retries,omitempty"`
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
	// Capture whether manual_file was explicitly set before applyDefaults fills it in.
	hasExplicitManualFile := cfg.Routing.Subnets.ManualFile != ""
	cfg.applyDefaults()
	if err := cfg.validate(hasExplicitManualFile); err != nil {
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

func (c *Config) validate(hasExplicitManualFile bool) error {
	hasRouting := c.Routing.Domains.URL != "" || len(c.Routing.Subnets.URLs) > 0 || hasExplicitManualFile
	hasSubs := len(c.Subscriptions) > 0
	if !hasRouting && !hasSubs {
		return errors.New("config must have at least routing (domains.url, subnets.urls, or subnets.manual_file) or subscriptions configured")
	}
	if c.Singbox.ConnectTimeout != "" {
		if _, err := time.ParseDuration(c.Singbox.ConnectTimeout); err != nil {
			return fmt.Errorf("singbox has invalid connect_timeout %q: must be a Go duration (e.g. \"3s\", \"500ms\")", c.Singbox.ConnectTimeout)
		}
	}
	return nil
}

// ValidateSubscriptions checks subscription-specific constraints required before
// running the subscription pipeline:
//   - at least one subscription must be defined
//   - exactly one subscription must have "default": true
//   - the default subscription must not be disabled
//   - no subscription may have an empty string in its exclude list
//   - a subscription carries either a url or inline nodes, never both; an
//     enabled one must carry exactly one of them
func (c *Config) ValidateSubscriptions() error {
	if len(c.Subscriptions) == 0 {
		return errors.New("no subscriptions configured")
	}
	var defaultID string
	var defaultCount int
	for id, sub := range c.Subscriptions {
		if sub == nil {
			return fmt.Errorf("subscription %q is null; remove it or provide a valid config object", id)
		}
		if sub.Default {
			defaultCount++
			defaultID = id
		}
		if slices.Contains(sub.Include, "") {
			return fmt.Errorf("subscription %q has an empty include pattern: remove it or provide a non-empty pattern", id)
		}
		if slices.Contains(sub.Exclude, "") {
			return fmt.Errorf("subscription %q has an empty exclude pattern: remove it or provide a non-empty pattern", id)
		}
		if sub.Interval != "" {
			if _, err := time.ParseDuration(sub.Interval); err != nil {
				return fmt.Errorf("subscription %q has invalid interval %q: must be a Go duration (e.g. \"5m\", \"30s\")", id, sub.Interval)
			}
		}
		if err := validateSource(id, sub); err != nil {
			return err
		}
	}
	if defaultCount == 0 {
		return errors.New("no default subscription defined (set \"default\": true on one subscription)")
	}
	if defaultCount > 1 {
		return errors.New("multiple default subscriptions defined, only one allowed")
	}
	if !c.Subscriptions[defaultID].IsEnabled() {
		return fmt.Errorf("default subscription %q cannot be disabled", defaultID)
	}
	return nil
}

// validateSource checks that a subscription draws its nodes from exactly one
// source: a remote url or an inline nodes list. A disabled subscription with
// neither is left alone, since it is never fetched.
func validateSource(id string, sub *Subscription) error {
	if sub.URL != "" && len(sub.Nodes) > 0 {
		return fmt.Errorf("subscription %q has both url and nodes: keep the remote url or the inline nodes, not both", id)
	}
	if sub.URL == "" && len(sub.Nodes) == 0 {
		if sub.IsEnabled() {
			return fmt.Errorf("subscription %q has neither url nor nodes: set a subscription url or add inline vless:// nodes", id)
		}
		return nil
	}
	for _, uri := range sub.Nodes {
		if uri == "" {
			return fmt.Errorf("subscription %q has an empty node: remove it or provide a vless:// URI", id)
		}
		if _, err := vless.Parse(uri); err != nil {
			return fmt.Errorf("subscription %q has an invalid node %q: %w", id, uri, err)
		}
	}
	return nil
}
