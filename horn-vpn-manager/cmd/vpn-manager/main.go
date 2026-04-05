package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
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

// runBoth runs routing, then subscriptions, forwarding the same flags to each.
func runBoth(args []string) error {
	if err := routingRun(args); err != nil {
		return fmt.Errorf("routing: %w", err)
	}
	if err := subscriptionsRun(args); err != nil {
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
  help           Show this help message

Run "vpn-manager <command> help" for command-specific options.
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
  -c, --config   Path to config file (default: /etc/horn-vpn-manager/config.json)
  -v             Increase verbosity (up to -vvv)
  --no-color     Disable colored output
  --debug        Debug mode: config from binary dir, output to ./out, no system actions
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
  -c, --config   Path to config file (default: /etc/horn-vpn-manager/config.json)
  -v             Increase verbosity (up to -vvv)
  --no-color     Disable colored output
  --debug        Debug mode: config from binary dir, output to ./out, no system actions
`)
}
