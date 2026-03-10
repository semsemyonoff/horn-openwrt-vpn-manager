# Repository Guidelines

## Project Structure & Module Organization

This repository is intentionally small and centered on one OpenWrt automation script:

- `subs.sh` — main POSIX `sh` script that downloads subscription data (auto-detects raw VLESS or base64 format), generates sing-box outbounds and route rules, validates the config, and restarts the service. Supports `--dry-run` and `-v`/`-vv`/`-vvv` verbosity flags.
- `subs.example.json` — example subscription config; copy to `subs.json` on the router and fill in real URLs. `subs.json` is not tracked in the repository.  The subscription with `"default": true` provides the fallback outbound; others require a `domains` array for routing rules.
- `config.template.json` — sing-box template with placeholders: `__DEFAULT_OUTBOUND__`, `__DEFAULT_TAG__`, `__VLESS_OUTBOUNDS__`, `__URLTEST_OUTBOUNDS__`, `__ROUTE_RULES__`.
- `README.md` — setup, flags, deployment, and cron usage notes.

There is no `src/` or `tests/` directory yet; keep new files near the root unless a larger test/docs structure is introduced.

## Build, Test, and Development Commands

- `sh -n subs.sh` — quick syntax check for the shell script.
- `shellcheck subs.sh` — optional lint pass if `shellcheck` is available locally.
- `jq . subs.example.json` — validate example subscription JSON formatting.
- `jq . config.template.json` — validate template JSON structure before deployment.

Do not run `subs.sh` locally against live infrastructure. Use `--dry-run -v` for safe local inspection of generated config output.

## Coding Style & Naming Conventions

Use POSIX `sh`; avoid Bash-only features. Match the existing style:

- Follow `.editorconfig` as the source of truth.
- Use 2-space indentation in `*.sh` and `*.json`; use 4 spaces elsewhere unless overridden.
- Uppercase variable names for path/config constants (`SUBS_CONF`, `TMPDIR`).
- Lowercase snake_case for shell functions (`parse_uri`, `is_excluded`).
- Keep log messages short and operational.

Prefer small, composable shell blocks over complex pipelines. Preserve placeholder names in `config.template.json` exactly. Keep Markdown files with a final newline; do not add one to files where `.editorconfig` forbids it.

## Testing Guidelines

There is no automated test suite yet. Before opening a change:

- Run `sh -n subs.sh`.
- Lint with `shellcheck subs.sh` when available.
- Validate JSON files with `jq`.
- If editing generation logic, test on a router or compatible environment with `sing-box check -c /etc/sing-box/config.json.new`.

Document manual verification steps in the PR when runtime behavior changes.

## Commit & Pull Request Guidelines

Git history is currently minimal (`Init`), so use clear imperative commit messages, preferably scoped, for example:

- `feat: add dry-run mode`
- `fix: skip excluded servers case-insensitively`
- `docs: clarify cron setup`

PRs should include a short summary, deployment impact, sample commands used for validation, and any config changes required on the router.

## Security & Configuration Tips

Do not commit real subscription URLs, credentials, or generated router configs. Keep production-specific values out of tracked files and scrub logs before sharing.
