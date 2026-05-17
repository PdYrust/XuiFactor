# XuiFactor Operations Guide

## 1. Overview

XuiFactor is a Linux sidecar for 3x-ui servers. It applies temporary traffic factor rules to 3x-ui client counters stored in SQLite without modifying 3x-ui source code.

Rule metadata is stored in the same SQLite database using `xui_factor_*` metadata tables. Factored traffic remains in the 3x-ui counters after a rule is disabled, and future traffic after disabling is counted normally.

Repository: https://github.com/PdYrust/XuiFactor

## 2. Installation from release package

Download the release archive that matches the server architecture, then install from the extracted package:

```sh
tar -xzf xui-factor_v0.3.5-beta_linux_amd64.tar.gz
cd xui-factor_v0.3.5-beta_linux_amd64
sudo ./scripts/install.sh
```

Use the `linux_arm64` archive on ARM64 hosts.

On systemd hosts, install enables and starts `xui-factor.service` by default. Use `--no-start` to install files without starting the daemon, and `--no-enable` to avoid enabling service startup.

## 3. Installation from local checkout

Build locally and run the installer from the checkout:

```sh
make build
sudo ./scripts/install.sh
```

After installation, the command should be available as `xui-factor`.

## 4. Default paths

```text
config: /etc/xui-factor/config.json
database: /etc/x-ui/x-ui.db
backups: /var/backups/xui-factor
service: xui-factor.service
```

The default config points at the standard 3x-ui SQLite database path. Use `--config PATH` to run commands with a different config file.

Cleanup defaults:

```text
auto_cleanup: true
missing_client_grace: 30s
disabled_rule_retention: 7d
audit_retention: 30d
```

## 5. First health check

Run doctor before applying rules:

```sh
xui-factor doctor
```

Doctor checks config loading, database access, required 3x-ui schema, XuiFactor metadata readiness, backup directory access, rule counts, and service state where available.

## 6. Backups

Create a backup before broad changes:

```sh
xui-factor backup
```

Backups are SQLite-consistent and are written to the configured backup directory. The command prints the final backup path.

## 7. Single-user factor rules

Apply a factor to one client:

```sh
xui-factor enable --email User --inbound-id 1 --factor 1.2
```

`factor 1.2` means new traffic is counted as 120%. `factor 5` means new traffic is counted as 500%. `factor 1` applies no extra traffic and only refreshes baseline behavior during ticks.

Use `--inbound-id` when the same email may exist on more than one inbound.

## 8. Bulk factor rules

Apply a factor to all enabled clients:

```sh
xui-factor enable-all --factor 1.2
```

`enable-all` is persistent by default. Current matching clients are enrolled immediately, and future matching clients are enrolled by the daemon tick from their current counters. Traffic that happened before enrollment is not multiplied.

Compatible active single-user rules are consolidated into the persistent scope. Existing baselines and remainders are preserved, and the old single-user rules become merged metadata that cleanup can prune later.

Apply a factor only to clients with a configured traffic limit:

```sh
xui-factor enable-all --factor 1.2 --limited-only
```

Apply a factor only within one inbound:

```sh
xui-factor enable-all --factor 1.2 --inbound-id 1
```

Use snapshot mode to target only clients that exist when the command runs:

```sh
xui-factor enable-all --factor 1.2 --once
```

Paused and disabled bulk scopes do not enroll new clients. Resuming a paused scope refreshes existing baselines before enrollment continues.

Test a single-user rule before using `enable-all` on a production server.

## 9. Policy decisions

Before each tick, XuiFactor resolves one effective factor decision per client. Active excludes have highest precedence and mean no factor. Active user overrides come next and replace broader factors for one exact client. Single-user rules then apply before inbound persistent scopes, and inbound scopes apply before global persistent scopes.

This keeps overlapping metadata from double-applying factors. Suppressed lower-priority targets keep their baselines fresh from current counters, so they do not apply old traffic retroactively if they later become effective.

## 10. Exclude policies

Exclude one client from future factor application while keeping broad rules or scopes active:

```sh
xui-factor exclude --email User --inbound-id 1
```

List active excludes:

```sh
xui-factor excludes
```

Disable an exclude:

```sh
xui-factor unexclude --email User --inbound-id 1
```

An exclude is tied to the current traffic id, inbound id, and email. It stops future factor updates for that client but does not subtract previously factored traffic. After `unexclude`, matching rules and scopes can factor future traffic from the current counters; traffic that arrived while excluded is not retroactively factored.

## 11. Override policies

Set a specific future factor for one client while keeping broad rules or scopes active:

```sh
xui-factor override --email User --inbound-id 1 --factor 1.2
```

List active overrides:

```sh
xui-factor overrides
```

Remove an override:

```sh
xui-factor remove-override --email User --inbound-id 1
```

An override is tied to the current traffic id, inbound id, and email. It replaces the broader matched factor for that client and does not stack with a scope factor. Exclude policies still win over overrides. Previously factored traffic remains unchanged, and removing an override does not retroactively change traffic that arrived while the override was active.

## 12. Explain and effective status

Inspect one client's final effective decision:

```sh
xui-factor explain --email User --inbound-id 1
```

Show grouped effective policy totals:

```sh
xui-factor status --effective
```

Show client-level effective factors for one inbound:

```sh
xui-factor status --clients --inbound-id 1
```

Effective views use the same precedence as tick:

```text
exclude > override > single-user rule > inbound scope > global scope
```

Explain and effective status are read-only. They do not create audit events, refresh baselines, or modify 3x-ui counters.

## 13. Pause and resume

Pause one rule without changing counters:

```sh
xui-factor pause --email User --inbound-id 1
```

Resume one paused rule from current counters:

```sh
xui-factor resume --email User --inbound-id 1
```

Pause or resume all matching rules:

```sh
xui-factor pause-all
xui-factor resume-all
```

Traffic accumulated while a rule is paused is not factored when the rule resumes.

## 14. Disable behavior

Disable one rule:

```sh
xui-factor disable --email User --inbound-id 1
```

Disable all active or paused rules:

```sh
xui-factor disable-all
```

Disabling a rule does not subtract previously factored traffic. After disabling, future traffic is counted normally by 3x-ui.

## 15. One-shot tick

Run one factor tick manually:

```sh
xui-factor tick
```

This is useful after enabling a rule, during maintenance checks, or before starting the daemon service.

## 16. Daemon service operation

Install and update enable and start the service by default on systemd hosts. Check service state:

```sh
systemctl status xui-factor.service --no-pager
```

Restart the service:

```sh
sudo systemctl restart xui-factor.service
```

Enable and start the service manually when an opt-out flag was used:

```sh
sudo systemctl enable --now xui-factor.service
```

Follow service logs:

```sh
journalctl -u xui-factor.service -f
```

The service runs `xui-factor` in daemon mode and polls at the interval configured in `/etc/xui-factor/config.json`.

## 17. Report, status, and audit

Show a concise management report:

```sh
xui-factor report
xui-factor report --inbound-id 1
```

Report summarizes active scopes, single-user rules, excludes, overrides, effective clients, service state, metadata state, and traffic impact. Traffic impact is aggregated from `traffic_applied` audit events. v0.3.4-beta adds inbound and email metadata to new traffic audit events so filtered impact reports improve from this release forward; older events are not rewritten.

List effective active and paused rules:

```sh
xui-factor status
```

Normal status hides orphaned, merged, and ineffective legacy rules. Persistent scopes remain visible with their current materialized client count.

Normal status summarizes active scopes, active single-user rules, active policies, and effective factored clients. Active excludes and overrides are shown as policies. Scope lines show an `effective=` count when policies reduce the number of clients that currently receive the scope factor.

Use `status --effective` to inspect grouped final decisions, and `status --clients --inbound-id ID` to inspect client-level effective factors without large unfiltered output.

List all rules, including inactive metadata:

```sh
xui-factor status --all
```

Show audit events for one client:

```sh
xui-factor audit --email User --inbound-id 1
```

Filter audit events:

```sh
xui-factor audit --event override_enabled --limit 20
xui-factor audit --email User --inbound-id 1
xui-factor audit --since 24h
```

Use status and audit after lifecycle changes to confirm the intended rule state.

## 18. Reconcile legacy metadata

Reconcile older active single-user rules after upgrading or after manual database repair:

```sh
xui-factor reconcile --dry-run
xui-factor reconcile
```

Use `--inbound-id 1` to limit the repair to one inbound. Reconcile adopts compatible legacy rules into matching persistent scopes, marks ineffective active rules as orphaned, and never modifies 3x-ui counters. Orphaned legacy rules are inactive and are pruned later by cleanup retention.

## 19. Metadata cleanup

The daemon runs lightweight cleanup automatically when `auto_cleanup` is enabled. Deleted or replaced clients are first marked missing by traffic id, inbound id, and email. Missing client tracking is pruned after the grace period.

Run a dry run before manual cleanup on production servers:

```sh
xui-factor cleanup --dry-run
```

Run cleanup with configured retention:

```sh
xui-factor cleanup
```

Override disabled-rule and audit retention for one run:

```sh
xui-factor cleanup --older-than 24h
```

Cleanup prunes only XuiFactor metadata, including inactive excludes and overrides after retention. It never modifies 3x-ui counters and does not subtract previously factored traffic. SQLite `VACUUM` is explicit only:

```sh
xui-factor cleanup --vacuum
```

## 20. Update workflow

From a new release package, run:

```sh
sudo ./scripts/update.sh
```

The update workflow preserves existing config, refreshes installed package files, enables `xui-factor.service`, and restarts it by default. Use `--no-start` or `--no-restart` to skip restart, and `--no-enable` to avoid enabling service startup.

## 21. Uninstall workflow

Remove the installed binary, service, and shared package files while preserving config and backups:

```sh
sudo ./scripts/uninstall.sh
```

Remove XuiFactor-owned config and shared files:

```sh
sudo ./scripts/uninstall.sh --purge
```

Uninstall and purge must not remove `/etc/x-ui/x-ui.db`.

## 22. Recovery expectations

XuiFactor does not automatically restore backups. To recover from a selected backup:

1. Stop `xui-factor.service`.
2. Stop 3x-ui.
3. Restore the selected backup database manually.
4. Start 3x-ui.
5. Start `xui-factor.service`.
6. Run `xui-factor doctor`.
7. Check `xui-factor status` and `xui-factor audit`.

Keep backup files until the recovered server has been verified.

## 23. Safety checklist

- Run `xui-factor doctor` first.
- Run `xui-factor backup` before broad changes.
- Test a single user before `enable-all`.
- Use `xui-factor exclude --email User --inbound-id 1` for clients that must not receive a broad factor.
- Use `xui-factor override --email User --inbound-id 1 --factor 1.2` when one exact client needs a different future factor.
- Use `xui-factor explain --email User --inbound-id 1` when the final decision is unclear.
- Use `--limited-only` if unlimited clients should be skipped.
- Run `xui-factor reconcile --dry-run` after upgrades with legacy active rules.
- Run `xui-factor cleanup --dry-run` before manual metadata cleanup.
- Verify `xui-factor status` and `xui-factor audit` after changes.
- Keep backup files before uninstall or purge.
