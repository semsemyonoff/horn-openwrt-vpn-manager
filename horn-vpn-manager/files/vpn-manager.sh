#!/bin/sh
# shellcheck disable=SC3043

# vpn-manager — CLI entry point for horn-vpn-manager
#
# Usage:
#   vpn-manager subscriptions [run|dry-run|help] [options]
#   vpn-manager domains [run|restore|help] [options]
#   vpn-manager help

LIBEXEC_DIR="/usr/libexec/horn-vpn-manager"

show_help() {
    printf 'vpn-manager — VPN subscription and routing manager for OpenWrt\n\n'
    printf 'Usage: vpn-manager <command> [subcommand] [options]\n\n'
    printf 'Commands:\n'
    printf '  subscriptions  Manage VPN subscriptions (download, update sing-box config)\n'
    printf '  domains        Manage domain/IP routing lists (download, apply, restore)\n'
    printf '  help           Show this help message\n'
    printf '\nExamples:\n'
    printf '  vpn-manager subscriptions run          Download and apply subscriptions\n'
    printf '  vpn-manager subscriptions dry-run -v   Simulate subscription update\n'
    printf '  vpn-manager domains run                Download and apply domain lists\n'
    printf '  vpn-manager domains restore            Restore cached lists (boot)\n'
    printf '\nRun "vpn-manager <command> help" for command-specific options.\n'
}

case "${1:-help}" in
    subscriptions|subs)
        shift
        exec "$LIBEXEC_DIR/subs.sh" "$@"
        ;;
    routing|domains)
        shift
        exec "$LIBEXEC_DIR/getdomains.sh" "$@"
        ;;
    help|-h|--help)
        show_help
        exit 0
        ;;
    *)
        printf 'Unknown command: %s\n\n' "$1" >&2
        show_help >&2
        exit 1
        ;;
esac
