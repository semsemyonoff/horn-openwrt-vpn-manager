package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/routing"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/system"
)

type routingFlags struct {
	configPath        string
	verbosity         int
	debug             bool
	noColor           bool
	withSubscriptions bool
}

// binDir returns the directory containing the running binary.
func binDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Dir(exe), nil
}

func parseRoutingFlags(args []string) (routingFlags, error) {
	f := routingFlags{configPath: config.DefaultPath}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" || args[i] == "--config":
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires an argument", args[i])
			}
			i++
			f.configPath = args[i]
		case args[i] == "--debug":
			f.debug = true
		case args[i] == "--no-color":
			f.noColor = true
		case args[i] == "--with-subscriptions":
			f.withSubscriptions = true
		case strings.HasPrefix(args[i], "-v") && !strings.HasPrefix(args[i], "--"):
			f.verbosity = len(args[i]) - 1 // -v=1, -vv=2, -vvv=3
		}
	}
	return f, nil
}

func routingRun(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return routingRunCtx(ctx, args)
}

func routingRunCtx(ctx context.Context, args []string) error {
	flags, err := parseRoutingFlags(args)
	if err != nil {
		return err
	}
	logx.Setup(!flags.noColor, flags.verbosity, flags.debug)

	if flags.debug {
		return routingRunDebug(flags)
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	applier := system.NewOpenWrt()
	runner := routing.NewRunner(cfg, applier)

	if err := runner.Run(ctx); err != nil {
		return err
	}

	if flags.withSubscriptions {
		subsListsDir := routing.DefaultListsDir + "/" + subscription.SubsListsSubdir
		logx.Header("downloading subscription lists")
		opts := fetch.Options{
			Retries:     cfg.Fetch.Retries,
			Timeout:     time.Duration(cfg.Fetch.TimeoutSeconds) * time.Second,
			Parallelism: cfg.Fetch.Parallelism,
		}
		if err := subscription.DownloadSubscriptionLists(ctx, cfg, subsListsDir, opts); err != nil {
			return fmt.Errorf("download subscription lists: %w", err)
		}
		logx.Header("done")
	}

	return nil
}

func routingRunDebug(flags routingFlags) error {
	dir, err := binDir()
	if err != nil {
		return err
	}

	// In debug mode, config comes from binary's directory unless explicitly overridden
	cfgPath := flags.configPath
	if cfgPath == config.DefaultPath {
		cfgPath = filepath.Join(dir, "config.json")
	}

	outDir := filepath.Join(dir, "out")

	logx.Info("Debug mode: using local files from %s", logx.Bold(dir))
	logx.Dim("debug implies no system actions (dnsmasq, firewall)")
	logx.Dim("config=%s", cfgPath)
	logx.Dim("output=%s", outDir)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	applier := system.NewDebugApplier()
	runner := routing.NewRunner(cfg, applier)
	runner.ListsDir = outDir

	if err := runner.Run(ctx); err != nil {
		return err
	}

	if flags.withSubscriptions {
		subsListsDir := filepath.Join(outDir, "lists", subscription.SubsListsSubdir)
		logx.Header("downloading subscription lists")
		opts := fetch.Options{
			Retries:     cfg.Fetch.Retries,
			Timeout:     time.Duration(cfg.Fetch.TimeoutSeconds) * time.Second,
			Parallelism: cfg.Fetch.Parallelism,
		}
		if err := subscription.DownloadSubscriptionLists(ctx, cfg, subsListsDir, opts); err != nil {
			return fmt.Errorf("download subscription lists: %w", err)
		}
		logx.Header("done")
	}

	return nil
}

func routingRestore(args []string) error {
	flags, err := parseRoutingFlags(args)
	if err != nil {
		return err
	}
	logx.Setup(!flags.noColor, flags.verbosity, false)

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	applier := system.NewOpenWrt()
	runner := routing.NewRunner(cfg, applier)

	return runner.Restore()
}
