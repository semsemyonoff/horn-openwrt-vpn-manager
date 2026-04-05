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
	case "routing":
		return runRouting(args[1:])
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

func printUsage() {
	fmt.Print(`vpn-manager — VPN subscription and routing manager for OpenWrt

Usage: vpn-manager <command> [subcommand] [options]

Commands:
  routing        Manage domain/IP routing lists (download, apply, restore)
  help           Show this help message

Run "vpn-manager <command> help" for command-specific options.
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
