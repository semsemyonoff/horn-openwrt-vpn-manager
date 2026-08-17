// Package config handles loading and validating the unified config file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/nodes"
)

const DefaultPath = "/etc/horn-vpn-manager/config.json"

// DefaultManualIPFile is where routing.subnets.manual_file points when the
// config leaves it unset.
const DefaultManualIPFile = "/etc/horn-vpn-manager/lists/manual-ip.lst"

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
// Nodes carries inline node URIs for a self-hosted provider and is mutually
// exclusive with a non-empty URL. Any scheme the nodes dispatcher knows is
// accepted, so a list may mix protocols.
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
	Fallback  *Fallback          `json:"fallback,omitempty"`
}

// Fallback declares an ordered chain of backup subscriptions. It may be set on
// any subscription: the declaring subscription's final tag becomes a generated
// fallback group listing its own nodes first, then each backup in order.
//
// BlacklistTimeout is how long a failing outbound stays skipped; empty leaves
// sing-box's own default.
type Fallback struct {
	Subscriptions    []string `json:"subscriptions"`
	BlacklistTimeout string   `json:"blacklist_timeout"`
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
		c.Routing.Subnets.ManualFile = DefaultManualIPFile
	}
}

func (c *Config) validate(hasExplicitManualFile bool) error {
	hasRouting := c.Routing.Domains.URL != "" || len(c.Routing.Subnets.URLs) > 0 || hasExplicitManualFile
	hasSubs := len(c.Subscriptions) > 0
	if !hasRouting && !hasSubs {
		return errors.New("config must have at least routing (domains.url, subnets.urls, or subnets.manual_file) or subscriptions configured")
	}
	if c.Singbox.ConnectTimeout != "" {
		// time.ParseDuration accepts "0" and a leading "-", so the sign has to
		// be checked separately: a non-positive value is written onto every node
		// outbound and would take the whole proxy down.
		if d, err := time.ParseDuration(c.Singbox.ConnectTimeout); err != nil || d <= 0 {
			return fmt.Errorf("singbox has invalid connect_timeout %q: must be a positive Go duration (e.g. \"3s\", \"500ms\")", c.Singbox.ConnectTimeout)
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
//   - every fallback chain references existing subscriptions and forms no cycle
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
			if d, err := time.ParseDuration(sub.Interval); err != nil || d <= 0 {
				return fmt.Errorf("subscription %q has invalid interval %q: must be a positive Go duration (e.g. \"5m\", \"30s\")", id, sub.Interval)
			}
		}
		if err := validateSource(id, sub); err != nil {
			return err
		}
		if err := c.validateFallback(id, sub); err != nil {
			return err
		}
	}
	if err := c.validateFallbackCycles(); err != nil {
		return err
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
			return fmt.Errorf("subscription %q has neither url nor nodes: set a subscription url or add inline node URIs (supported schemes: %s)", id, supportedSchemes())
		}
		return nil
	}
	for _, uri := range sub.Nodes {
		if uri == "" {
			return fmt.Errorf("subscription %q has an empty node: remove it or provide a node URI (supported schemes: %s)", id, supportedSchemes())
		}
		if _, err := nodes.Parse(uri); err != nil {
			return fmt.Errorf("subscription %q has an invalid node %q: %w", id, uri, err)
		}
	}
	return nil
}

// supportedSchemes renders the node schemes the dispatcher accepts, so a source
// error tells the operator what a valid inline node looks like.
func supportedSchemes() string {
	return strings.Join(nodes.Schemes(), ", ")
}

// validateFallback checks a single fallback chain declaration. A chain is
// allowed on any subscription, but a disabled one is never generated, so its
// chain is left unvalidated the same way its missing source is.
func (c *Config) validateFallback(id string, sub *Subscription) error {
	fb := sub.Fallback
	if fb == nil || !sub.IsEnabled() {
		return nil
	}
	if len(fb.Subscriptions) == 0 {
		return fmt.Errorf("subscription %q has an empty fallback chain: list at least one backup subscription or remove \"fallback\"", id)
	}
	seen := make(map[string]bool, len(fb.Subscriptions))
	for _, ref := range fb.Subscriptions {
		switch {
		case ref == "":
			return fmt.Errorf("subscription %q has an empty id in its fallback chain: remove it or name a backup subscription", id)
		case ref == id:
			return fmt.Errorf("subscription %q lists itself in its fallback chain: a chain may only name other subscriptions", id)
		case seen[ref]:
			return fmt.Errorf("subscription %q lists %q twice in its fallback chain: each backup may appear only once", id, ref)
		}
		seen[ref] = true
		backup, ok := c.Subscriptions[ref]
		if !ok || backup == nil {
			return fmt.Errorf("subscription %q has a fallback chain referencing unknown subscription %q: name an existing subscription id", id, ref)
		}
		if !backup.IsEnabled() {
			return fmt.Errorf("subscription %q has a fallback chain referencing disabled subscription %q: enable it or drop it from the chain", id, ref)
		}
	}
	if fb.BlacklistTimeout != "" {
		// A non-positive timeout expires a blacklist entry before it is set,
		// defeating the chain, so it is rejected rather than passed through.
		if d, err := time.ParseDuration(fb.BlacklistTimeout); err != nil || d <= 0 {
			return fmt.Errorf("subscription %q has invalid fallback blacklist_timeout %q: must be a positive Go duration (e.g. \"1m\", \"30s\")", id, fb.BlacklistTimeout)
		}
	}
	return nil
}

// validateFallbackCycles walks the chain graph and rejects loops of any length.
// A referenced subscription may declare a chain of its own, so resolution is
// recursive and a → b → a is expressible without any self-reference.
//
// References that validateFallback already rejects (unknown, disabled) are
// skipped here so the more specific error wins.
func (c *Config) validateFallbackCycles() error {
	const (
		unvisited = iota
		onPath
		done
	)
	state := make(map[string]int, len(c.Subscriptions))
	var path []string

	var walk func(id string) error
	walk = func(id string) error {
		state[id] = onPath
		path = append(path, id)
		defer func() {
			path = path[:len(path)-1]
			state[id] = done
		}()

		sub := c.Subscriptions[id]
		if sub == nil || sub.Fallback == nil || !sub.IsEnabled() {
			return nil
		}
		for _, ref := range sub.Fallback.Subscriptions {
			next, ok := c.Subscriptions[ref]
			if !ok || next == nil || !next.IsEnabled() {
				continue
			}
			switch state[ref] {
			case onPath:
				cycle := append(slices.Clone(path[slices.Index(path, ref):]), ref)
				return fmt.Errorf("fallback chain forms a cycle: %s: a chain must not lead back to a subscription already in it", strings.Join(cycle, " -> "))
			case done:
				continue
			}
			if err := walk(ref); err != nil {
				return err
			}
		}
		return nil
	}

	// Sorted so the reported cycle does not depend on map iteration order.
	for _, id := range slices.Sorted(maps.Keys(c.Subscriptions)) {
		if state[id] != unvisited {
			continue
		}
		if err := walk(id); err != nil {
			return err
		}
	}
	return nil
}
