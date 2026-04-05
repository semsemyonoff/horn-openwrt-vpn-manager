package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

type checkFlags struct {
	configPath string
	verbosity  int
	noColor    bool
}

func parseCheckFlags(args []string) checkFlags {
	f := checkFlags{configPath: config.DefaultPath}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" || args[i] == "--config":
			if i+1 < len(args) {
				i++
				f.configPath = args[i]
			}
		case args[i] == "--no-color":
			f.noColor = true
		case strings.HasPrefix(args[i], "-v") && !strings.HasPrefix(args[i], "--"):
			f.verbosity = len(args[i]) - 1
		}
	}
	return f
}

func runCheck(args []string) error {
	flags := parseCheckFlags(args)
	logx.Setup(!flags.noColor, flags.verbosity, false)

	logx.Header("config check")

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		logx.Err("Config invalid: %v", err)
		return err
	}
	logx.OK("Config loaded: %s", flags.configPath)

	// Report fetch settings.
	logx.Detail("Fetch: retries=%d timeout=%ds parallelism=%d",
		cfg.Fetch.Retries, cfg.Fetch.TimeoutSeconds, cfg.Fetch.Parallelism)

	// Report subscriptions.
	if len(cfg.Subscriptions) > 0 {
		enabled := 0
		for _, sub := range cfg.Subscriptions {
			if sub.IsEnabled() {
				enabled++
			}
		}
		logx.Info("Subscriptions: %d configured (%d enabled)", len(cfg.Subscriptions), enabled)
		for id, sub := range cfg.Subscriptions {
			if sub.Default {
				logx.Detail("  default: %s (%s)", id, sub.Name)
				break
			}
		}
		// Check subscription-level constraints without aborting.
		if err := cfg.ValidateSubscriptions(); err != nil {
			logx.Warn("Subscription config issue: %v", err)
		}
	} else {
		logx.Info("No subscriptions configured")
	}

	// Report routing.
	hasRouting := cfg.Routing.Domains.URL != "" || len(cfg.Routing.Subnets.URLs) > 0
	if hasRouting {
		logx.Info("Routing: configured")
		if cfg.Routing.Domains.URL != "" {
			logx.Detail("  domains url: configured")
		}
		if n := len(cfg.Routing.Subnets.URLs); n > 0 {
			logx.Detail("  subnet urls: %d configured", n)
		}
	} else {
		logx.Info("Routing: not configured")
	}

	// Report sing-box template.
	if cfg.Singbox.Template != "" {
		if _, err := os.Stat(cfg.Singbox.Template); err != nil {
			logx.Warn("sing-box template not found: %s", cfg.Singbox.Template)
		} else {
			logx.Detail("sing-box template: %s", cfg.Singbox.Template)
		}
	} else {
		logx.Detail("sing-box template: (built-in default)")
	}

	// Non-fatal: check if sing-box is available on the system.
	if _, err := os.Stat("/usr/bin/sing-box"); err != nil {
		logx.Warn("sing-box not found at /usr/bin/sing-box (expected on OpenWrt device)")
	} else {
		logx.OK("sing-box binary found")
	}

	logx.Header("check passed")
	fmt.Printf("config: %s\n", flags.configPath)
	return nil
}
