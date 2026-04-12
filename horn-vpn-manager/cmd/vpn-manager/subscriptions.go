package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/system"
)

type subsFlags struct {
	configPath    string
	templatePath  string
	verbosity     int
	debug         bool
	noColor       bool
	downloadLists bool
	cachedLists   bool
}

func parseSubsFlags(args []string) (subsFlags, error) {
	f := subsFlags{configPath: config.DefaultPath}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" || args[i] == "--config":
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires an argument", args[i])
			}
			i++
			f.configPath = args[i]
		case args[i] == "-t" || args[i] == "--template":
			if i+1 >= len(args) {
				return f, fmt.Errorf("flag %s requires an argument", args[i])
			}
			i++
			f.templatePath = args[i]
		case args[i] == "--debug":
			f.debug = true
		case args[i] == "--no-color":
			f.noColor = true
		case args[i] == "--download-lists":
			f.downloadLists = true
		case args[i] == "--cached-lists":
			f.cachedLists = true
		case strings.HasPrefix(args[i], "-v") && !strings.HasPrefix(args[i], "--"):
			f.verbosity = len(args[i]) - 1
		}
	}
	return f, nil
}

// applyListsFlags wires the list-cache flags onto the runner.
//
//   - --cached-lists: use prefetched lists from SubsListsDir (cache-first);
//     downloads only on cache miss; uses cache as fallback when download fails.
//     Intended to consume lists pre-fetched by "routing run --with-subscriptions".
//   - --download-lists: always download fresh lists, save them to SubsListsDir,
//     and fall back to the saved copy if a download later fails.
//   - (no flag): live refresh — always download, no cache interaction.
func applyListsFlags(runner *subscription.Runner, flags subsFlags, subsListsDir string) {
	switch {
	case flags.downloadLists:
		runner.SubsListsDir = subsListsDir
		runner.DownloadLists = true
	case flags.cachedLists:
		runner.SubsListsDir = subsListsDir
		// DownloadLists stays false → cache-first, download only on miss.
	}
}

func subscriptionsRun(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return subscriptionsRunCtx(ctx, args)
}

func subscriptionsRunCtx(ctx context.Context, args []string) error {
	flags, err := parseSubsFlags(args)
	if err != nil {
		return err
	}
	logx.Setup(!flags.noColor, flags.verbosity, flags.debug)

	if flags.debug {
		return subscriptionsRunDebug(flags, false)
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	applier := system.NewOpenWrt()
	runner := subscription.NewRunner(cfg, applier)
	runner.TemplatePath = flags.templatePath
	applyListsFlags(runner, flags, subscription.DefaultSubsListsDir)

	return runner.Run(ctx)
}

func subscriptionsDryRun(args []string) error {
	flags, err := parseSubsFlags(args)
	if err != nil {
		return err
	}
	logx.Setup(!flags.noColor, flags.verbosity, flags.debug)

	if flags.debug {
		return subscriptionsRunDebug(flags, true)
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	applier := subscription.NewDebugApplier()
	runner := subscription.NewRunner(cfg, applier)
	runner.TemplatePath = flags.templatePath
	runner.DryRun = true
	applyListsFlags(runner, flags, subscription.DefaultSubsListsDir)

	return runner.Run(ctx)
}

func subscriptionsRunDebug(flags subsFlags, dryRun bool) error {
	dir, err := binDir()
	if err != nil {
		return err
	}

	cfgPath := flags.configPath
	if cfgPath == config.DefaultPath {
		cfgPath = filepath.Join(dir, "config.json")
	}

	templatePath := flags.templatePath
	if templatePath == "" {
		local := filepath.Join(dir, "sing-box.template.json")
		if _, statErr := os.Stat(local); statErr == nil {
			templatePath = local
		} else {
			templatePath = filepath.Join(dir, "sing-box.template.default.json")
		}
	}

	outDir := filepath.Join(dir, "out")

	logx.Info("Debug mode: using local files from %s", logx.Bold(dir))
	logx.Dim("debug implies no system actions (sing-box)")
	logx.Dim("config=%s", cfgPath)
	logx.Dim("template=%s", templatePath)
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

	subsListsDir := filepath.Join(outDir, "lists", subscription.SubsListsSubdir)

	applier := subscription.NewDebugApplier()
	runner := subscription.NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.ConfigDir = outDir
	runner.TemplatePath = templatePath
	runner.DryRun = dryRun
	applyListsFlags(runner, flags, subsListsDir)

	return runner.Run(ctx)
}
