package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time via -ldflags "-X main.version=<ver>".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// hasHelpFlag reports whether args contains -h or --help.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "version":
		return runVersion(args[1:])
	case "run":
		return runBoth(args[1:])
	case "routing":
		return runRouting(args[1:])
	case "subscriptions":
		return runSubscriptions(args[1:])
	case "check":
		return runCheck(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runVersion(args []string) error {
	if hasHelpFlag(args) {
		fmt.Print(`vpn-manager version — print version and exit

Usage: vpn-manager version
`)
		return nil
	}
	fmt.Printf("vpn-manager %s\n", version)
	return nil
}

func runRouting(args []string) error {
	if len(args) == 0 {
		printRoutingHelp()
		return nil
	}

	switch args[0] {
	case "run":
		return routingRun(args[1:])
	case "restore":
		return routingRestore(args[1:])
	case "help", "-h", "--help":
		printRoutingHelp()
		return nil
	default:
		return fmt.Errorf("unknown routing subcommand: %s", args[0])
	}
}

func runSubscriptions(args []string) error {
	if len(args) == 0 {
		printSubscriptionsHelp()
		return nil
	}

	switch args[0] {
	case "run":
		return subscriptionsRun(args[1:])
	case "dry-run":
		return subscriptionsDryRun(args[1:])
	case "help", "-h", "--help":
		printSubscriptionsHelp()
		return nil
	default:
		return fmt.Errorf("unknown subscriptions subcommand: %s", args[0])
	}
}

// runBoth runs routing, then subscriptions using a shared signal context so
// that SIGTERM/interrupt during the routing phase also prevents subscriptions
// from starting.
func runBoth(args []string) error {
	if hasHelpFlag(args) {
		printRunHelp()
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := routingRunCtx(ctx, args); err != nil {
		return fmt.Errorf("routing: %w", err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("interrupted: %w", ctx.Err())
	}
	if err := subscriptionsRunCtx(ctx, args); err != nil {
		return fmt.Errorf("subscriptions: %w", err)
	}
	return nil
}

func printUsage() {
	fmt.Print(`vpn-manager — VPN subscription and routing manager for OpenWrt

Usage: vpn-manager <command> [subcommand] [options]

Commands:
  run            Run routing and subscriptions pipelines (initial bootstrap)
  routing        Manage domain/IP routing lists (download, apply, restore)
  subscriptions  Download and process VPN subscriptions
  check          Validate config and report what is configured
  version        Print version and exit
  help           Show this help message

Run "vpn-manager <command> --help" for command-specific options.
`)
}

func printRunHelp() {
	fmt.Print(`vpn-manager run — run routing then subscriptions (initial bootstrap)

Usage: vpn-manager run [options]

Options:
  -c, --config     Path to config file (default: /etc/horn-vpn-manager/config.json)
  -t, --template   Path to sing-box template
  -v               Increase verbosity (up to -vvv)
  --no-color       Disable colored output
  --debug          Debug mode: config/template from binary dir, output to ./out, no system actions
`)
}

func printCheckHelp() {
	fmt.Print(`vpn-manager check — validate config and report what is configured

Usage: vpn-manager check [options]

Options:
  -c, --config   Path to config file (default: /etc/horn-vpn-manager/config.json)
  -v             Increase verbosity (up to -vvv)
  --no-color     Disable colored output
`)
}

func printSubscriptionsHelp() {
	fmt.Print(`vpn-manager subscriptions — download and process VPN subscriptions

Usage: vpn-manager subscriptions <subcommand> [options]

Subcommands:
  run            Download subscriptions and process nodes
  dry-run        Download and decode subscriptions without applying to system
  help           Show this help message

Options:
  -c, --config     Path to config file (default: /etc/horn-vpn-manager/config.json)
  -t, --template   Path to sing-box template (default: embedded; in --debug: <bindir>/sing-box.template.default.json)
  -v               Increase verbosity (up to -vvv)
  --no-color       Disable colored output
  --debug          Debug mode: config/template from binary dir, output to ./out, no system actions
`)
}

func printRoutingHelp() {
	fmt.Print(`vpn-manager routing — manage domain/IP routing lists

Usage: vpn-manager routing <subcommand> [options]

Subcommands:
  run            Download lists, cache, and apply to system
  restore        Apply from existing cache without downloading (for boot)
  help           Show this help message

Options:
  -c, --config              Path to config file (default: /etc/horn-vpn-manager/config.json)
  -v                        Increase verbosity (up to -vvv)
  --no-color                Disable colored output
  --debug                   Debug mode: config from binary dir, output to ./out, no system actions
  --with-subscriptions      Also pre-fetch subscription route lists into cache (run only)
`)
}
