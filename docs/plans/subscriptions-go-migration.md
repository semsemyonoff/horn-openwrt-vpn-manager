# Plan: Migrate subscriptions pipeline from subs.sh to Go

## Overview

Implement the next major step of the `horn-vpn-manager` rewrite by porting the legacy subscription pipeline from `horn-vpn-manager/files/subs.sh` into the Go core.

This plan builds on the existing Go routing implementation and keeps the same overall direction:

- subscriptions remain a separate CLI pipeline from routing
- the Go core is the primary implementation target
- shell should only remain for package/init glue
- no backward compatibility with the old shell internals is required
- `horn-vpn-manager-luci` is out of scope for this phase

Implementation should follow the current Go architecture where it makes sense, but the existing package/module layout is a reference, not a rigid contract. Reshape it if needed to produce a cleaner and more testable result.

## Config Format Context

The subscriptions migration should target the new unified config format rather than the legacy `subs.json` and `domains.json` split.

Reference shape:

```json
{
  "singbox": {
    "log_level": "warn",
    "test_url": "https://www.gstatic.com/generate_204",
    "template": "/etc/horn-vpn-manager/sing-box.template.json"
  },
  "fetch": {
    "retries": 3,
    "timeout_seconds": 15,
    "parallelism": 2
  },
  "routing": {
    "domains": {
      "url": "https://example.com/domains.lst"
    },
    "subnets": {
      "urls": [
        "https://example.com/subnets1.lst",
        "https://example.com/subnets2.lst"
      ],
      "manual_file": "/etc/horn-vpn-manager/lists/manual-ip.lst"
    }
  },
  "subscriptions": {
    "default": {
      "name": "Default",
      "url": "https://example.com/sub",
      "default": true,
      "enabled": true,
      "exclude": ["Russia", "traffic"],
      "interval": "5m",
      "tolerance": 100
    },
    "work": {
      "name": "Work",
      "url": "https://example.com/work",
      "route": {
        "domains": ["jira.example.com"],
        "domain_urls": [
          "https://example.com/work-domains.lst"
        ],
        "ip_cidrs": ["203.0.113.0/24"],
        "ip_urls": [
          "https://example.com/work-ips.lst"
        ]
      }
    }
  }
}
```

Key expectations:

- `singbox` contains settings directly related to generated `sing-box` config
- `fetch` contains shared download/runtime settings used by both routing and subscriptions
- `routing` contains global routing sources already used by the migrated routing pipeline
- `subscriptions` remains a keyed object, not an array; keys are stable subscription IDs
- per-subscription routing data should live under `route` when possible to keep transport/subscription settings separate from routing policy
- field names should prefer long-term clarity over mirroring shell variable names

The exact shape may still evolve during implementation, but all new subscriptions work should target this unified schema and not reintroduce the old two-file model.

The source of truth for generated `sing-box` config is the official documentation:

- `https://sing-box.sagernet.org/configuration/`

The migrated Go implementation must cover both explicitly requested stages and the remaining important behavior present in `subs.sh`, including:

- debug mode
- per-subscription retries
- config validation for default subscriptions
- disabled subscription handling
- stable node tag generation
- single-node vs multi-node outbound generation
- `urltest` and `selector` groups
- `sing-box` config rendering and validation
- config apply / restart flow
- tag-name export for future UI integration, if still useful

## Validation Commands

- `cd horn-vpn-manager && go test ./...`
- `cd horn-vpn-manager && golangci-lint run`
- `make lint`

### Task 1: Add subscriptions command scaffold, config model, and debug mode with raw format support

- [x] Extend the unified Go config schema with the `singbox` section and the `subscriptions` section needed for the subscription pipeline
- [x] Add `vpn-manager subscriptions run`, `vpn-manager subscriptions dry-run`, and `vpn-manager subscriptions help`
- [x] Implement `--debug` for subscriptions, matching the routing debug model: local config/template next to the binary, local output directory, and no system-side actions
- [x] Add the first subscriptions runner/service layer and keep system effects abstracted behind interfaces
- [x] Implement the first decoding path for raw subscription payloads, i.e. payloads that already contain `vless://` lines
- [x] Add fixtures and tests for raw-format subscriptions and debug-mode execution
- [x] Mark completed

### Task 2: Add base64 and base64url subscription decoding

- [x] Implement decoding for standard base64 subscription payloads
- [x] Implement decoding for URL-safe base64 payloads, because the legacy shell supports both
- [x] Keep decoding isolated from download/orchestration so the logic stays testable
- [x] Add tests covering valid base64, valid base64url, empty payloads, and malformed payloads
- [x] Preserve the current behavior of treating undecodable payloads as invalid subscription content rather than silently succeeding
- [x] Mark completed

### Task 3: Add gzip plus base64 handling and payload normalization

- [x] Implement gzip detection/decompression before payload decoding, matching the legacy shell behavior
- [x] Support the `gzip + base64` path explicitly, as requested
- [x] Normalize Windows line endings before URI extraction
- [x] Add tests for raw gzip, gzip + base64, gzip + base64url, and line-ending normalization
- [x] Make sure the decoding pipeline remains incremental and debuggable, with clear log messages per format
- [x] Mark completed

### Task 4: Add subscription validation, retries, and exclusion filtering

- [x] Port config-level validation from the shell script: at least one subscription, exactly one default subscription, and the default subscription must not be disabled
- [x] Preserve handling for disabled subscriptions: skip them without aborting the whole run
- [x] Implement global retries plus per-subscription retry overrides
- [x] Implement exclude-pattern filtering against decoded server names
- [x] Add tests for default-subscription validation, disabled subscriptions, retry behavior, and exclusion filtering
- [x] Keep failure semantics aligned with legacy behavior: a failed default subscription aborts the run, non-default failures are logged and skipped
- [x] Mark completed

### Task 5: Port VLESS parsing, node identity, and outbound group generation

- [x] Implement a dedicated VLESS parser package/module that converts URIs into typed Go models rather than shell-built JSON strings
- [x] Port the stable node hash logic so `<id>-node-<hash>` remains deterministic for the same connection parameters
- [x] Port single-node behavior: generate `<id>-single` directly when only one node remains after filtering
- [x] Port multi-node behavior: generate node outbounds, `<id>-auto` `urltest`, and `<id>-manual` `selector`
- [x] Preserve default outbound selection logic and keep the chosen final outbound explicit in the generated config model
- [x] Use official `sing-box` documentation as the source of truth for field names and shape, not the shell script alone
- [x] Add tests for parsing, stable hashes, single-node mode, multi-node mode, and tag generation
- [x] Mark completed

### Task 6: Port per-subscription routing for manual domains and manual IP/CIDR entries

- [ ] Add support for manual per-subscription routing rules from subscription config: `domains` and `ip` / `ip_cidrs`
- [ ] Generate route rules only for non-default subscriptions, matching the current shell behavior
- [ ] Preserve route-to-outbound mapping semantics for both single-node and multi-node subscriptions
- [ ] Add tests for manual domain routing and manual IP/CIDR routing
- [ ] Keep routing generation separate from payload download/parsing so the code remains composable
- [ ] Mark completed

### Task 7: Port per-subscription domain and IP list downloads

- [ ] Implement download support for `domain_urls`
- [ ] Implement download support for `ip_urls`
- [ ] Port validation rules for downloaded domain lists and downloaded IP/CIDR lists
- [ ] Port normalization and deduplication rules, including “manual entries win” semantics before merging downloaded entries
- [ ] Add tests for list downloads, invalid downloaded entries, deduplication, and merge behavior
- [ ] Reuse the shared fetch layer and bounded concurrency where useful, but do not introduce uncontrolled fan-out on router-class hardware
- [ ] Mark completed

### Task 8: Render final sing-box config, validate it, and apply it safely

- [ ] Replace the shell placeholder/`awk` assembly with typed Go rendering of the final `sing-box` config
- [ ] Support the configured `singbox.log_level`, `singbox.test_url`, and template path from the new unified config
- [ ] Implement dry-run output so generated config can be inspected without replacing the live config or restarting services
- [ ] Validate generated config with `sing-box check`
- [ ] Apply the new config atomically, keep a backup if appropriate, and restart `sing-box`
- [ ] Preserve or intentionally replace legacy side artifacts such as tag-name export; if `subs-tags.json` still provides value for the next LuCI phase, generate it from Go as part of apply
- [ ] Explicitly review legacy-only artifacts such as `.needs-update` and keep them only if the new Go core still needs them
- [ ] Add tests around rendering, dry-run behavior, and apply decision logic; use mocks/fakes for system commands
- [ ] Mark completed

### Task 9: Finish integration, CLI polish, and regression coverage

- [ ] Make sure the subscriptions pipeline integrates cleanly with the existing Go routing core and shared config/fetch/system packages
- [ ] Ensure logging is operationally clear for cron-style use, including success, partial failure, and fatal failure cases
- [ ] Add end-to-end integration tests for `subscriptions run` and `subscriptions dry-run` using fixtures and fake system hooks
- [ ] Review whether any temporary shell wrappers still need to call the new subscriptions Go path, and keep them thin if they remain
- [ ] Update any affected docs or examples needed to make the new subscriptions pipeline understandable to the next implementation step
- [ ] Mark completed
