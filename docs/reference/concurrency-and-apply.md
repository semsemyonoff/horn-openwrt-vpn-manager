# Run lock and applied state reference

Detail behind the one-line rules in `AGENTS.md` → *CLI Model → Concurrency and applied state*.

## The run lock

- **Every command that writes state takes an exclusive flock on `<config dir>/.run.lock`** (`system.AcquireRunLock`): `routing run`, `routing restore`, `subscriptions run`, `subscriptions dry-run`. `check` does not — rpcd calls it on every save and must not fail because cron is running. Routing and subscriptions share the route-list cache and both touch system services, so an overlap lets subscriptions build the config from the copy routing is replacing; the result is an applied config one revision behind with nothing in either log. The lock waits up to five minutes and then fails with `ErrLocked` rather than proceeding — sized against a whole `routing run --with-subscriptions`, since cron entries collide by construction (`0 */6` and `0 */12` fire together every 12 hours) and the right outcome is for subscriptions to wait and then build from the cache routing just refreshed.
- `runBoth` is safe because each phase acquires and releases in turn — do not hoist the lock around both, an flock on a second fd in the same process blocks just the same.
- **The lock is taken before the log file and before the config is read.** `logx.SetLogFile` truncates the log another run is still writing — the Run tab then shows a live run's log vanishing — and a config loaded before a wait of several minutes can be a generation out of date by the time it is applied. Order is: parse flags → `logx.Setup` → lock → `SetLogFile` → `config.Load`.

## Applied-revision markers

- **"Already applied" means an applied-revision marker, not equal file contents.** A run killed between promoting a file and restarting the service leaves the new file live and the old config in the running process; comparing files alone then reads as "already applied" on every later run and skips the restart *forever*. `OpenWrt` writes `<StateDir>/.applied-<service>` (a digest) only after a restart succeeds, drops it when a restart fails, and requires the service to be running before skipping. An empty `StateDir` disables the optimisation, which is the old unconditional-restart behaviour. Pinned by `TestApplySingbox_promoted_but_never_restarted` and `TestApplySingbox_failed_restart_clears_marker`.
- **A pipeline compares what it is about to apply with what is live, and skips the service restart when they match.** `ApplySingbox` restarting on an identical config tears down every established connection on a schedule; `ApplyDomains` restarting dnsmasq flushes the DNS cache the same way. `ApplySingbox` still restarts when `/etc/init.d/sing-box running` fails, so skipping never leaves a stopped service down — an init script without a `running` action reads as "not running" and keeps the old unconditional behaviour.
- **The skip sits after `sing-box check`, never before it:** that check is the only place a config meets the sing-box binary actually installed, so swapping the extended build for the stock one has to keep surfacing `unknown outbound type` even on a run where the rendered config did not change. Pinned by `TestApplySingbox_unchanged_still_validates`.

## Partial failures

- **A partial download must not narrow what is applied.** `routing.Run` writes the subnet cache only when **every** URL succeeded: the cache is a single merged file, so writing the survivors silently drops the failed lists' entries and the next firewall reload routes less than before, behind an error the operator has not read yet. All-failed and partial-failed both keep the previous cache; only the log line differs.
