# XuiFactor Operations Guide

## 1. Overview

XuiFactor is a Linux sidecar for 3x-ui 3.x servers. It applies temporary traffic factor rules to 3x-ui client counters without modifying 3x-ui source code. Panels running on either the SQLite database file or PostgreSQL are supported.

Rule metadata is stored in the panel database using `xui_factor_*` metadata tables. Factored traffic remains in the 3x-ui counters after a rule is disabled, and future traffic after disabling is counted normally.

On 3.x a client is identified by email and can be attached to several inbounds. XuiFactor resolves inbound membership through the panel's `clients` and `client_inbounds` tables; the `client_traffics.inbound_id` column names only one of the attached inbounds and is treated as informational.

Upgrade 3x-ui first, let the panel finish its boot migration, and only then upgrade XuiFactor. A panel database that predates 3.x (its `client_traffics` table still has `all_time`) is rejected with an upgrade hint.

Repository: https://github.com/PdYrust/XuiFactor

## 2. Installation from release package

Download the release archive that matches the server architecture, then install from the extracted package:

```sh
tar -xzf xui-factor_v0.6.0_linux_amd64.tar.gz
cd xui-factor_v0.6.0_linux_amd64
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

After installation, the command is available as `factor`. The installer links `/usr/local/bin/factor` to the `xui-factor` binary, so both names work. Because `/usr/local/bin` comes before `/usr/bin` in `PATH`, calls that start with a number, such as `factor 42`, are passed to the coreutils `factor` command unchanged. If `/usr/local/bin/factor` already belongs to something else, the installer leaves it alone and prints a warning; use `xui-factor` then.

## 4. Default paths and database backends

```text
config: /etc/xui-factor/config.json
database: /etc/x-ui/x-ui.db
backups: /var/backups/xui-factor
service: xui-factor.service
```

The default config points at the standard 3x-ui SQLite database path. Use `--config PATH` to run commands with a different config file.

Database backends are selected with `database_backend` and `database_dsn`:

```json
{
  "database_backend": "auto",
  "database_path": "/etc/x-ui/x-ui.db",
  "database_dsn": ""
}
```

- `auto` (default): a `database_dsn` in the config file selects PostgreSQL; otherwise the panel's own setting decides, as described below.
- `sqlite`: use `database_path`.
- `postgres`: use `database_dsn`, for example `postgres://x-ui:password@127.0.0.1:5432/x-ui?sslmode=disable`. When `database_dsn` is empty, `XUI_DB_DSN` is used.

On `auto` without a configured DSN, XuiFactor resolves the backend the same way 3x-ui does: PostgreSQL is used only when `XUI_DB_TYPE` is `postgres` (or `postgresql`, `pg`), and the connection string comes from `XUI_DB_DSN`. Both values are read from the process environment first and then from the panel service environment file (`/etc/default/x-ui`, `/etc/conf.d/x-ui`, or `/etc/sysconfig/x-ui`, depending on the distro), so the daemon and one-off commands detect a PostgreSQL panel without extra configuration. A leftover `XUI_DB_DSN` on a SQLite panel is ignored. Unknown `database_backend` values are rejected.

The installer creates the config file with mode `0600` because `database_dsn` can hold a password. Commands print the DSN with the password redacted.

Cleanup defaults:

```text
auto_cleanup: true
missing_client_grace: 30s
disabled_rule_retention: 7d
audit_retention: 30d
```

`factor_on_managed_node` (default `false`) allows ticks on a panel managed by a 3x-ui master; see [Master and node panels](#17-master-and-node-panels).

## 5. First health check

Run doctor before applying rules:

```sh
factor doctor
```

Doctor checks config loading, the active database backend and connection, database access and write access, the required 3x-ui 3.x tables and columns (`client_traffics`, `inbounds`, `clients`, `client_inbounds`), XuiFactor metadata readiness, backup directory access, the panel role in a master/node setup, rule counts, and service state where available.

## 6. Backups

Create a backup before broad changes:

```sh
factor backup
```

Backups are written to the configured backup directory and the command prints the final backup path. On SQLite the backup is a consistent `.db` file. On PostgreSQL the backup is a `.dump` file created with `pg_dump`; install PostgreSQL client tools whose major version is at least the server's. The dump uses the connection settings from the configured DSN, including its TLS settings, and ignores `PG*` variables in the calling environment.

## 7. Single-user factor rules

Apply a factor to one client:

```sh
factor enable --email User --inbound-id 1 --factor 1.2
```

`factor 1.2` means new traffic is counted as 120%. `factor 5` means new traffic is counted as 500%. `factor 1` applies no extra traffic and only refreshes baseline behavior during ticks.

Use `--inbound-id` to scope a command to one inbound. On 3.x emails are unique; the flag resolves the client's membership through `clients` and `client_inbounds` and fails when the client is not attached to that inbound.

## 8. Bulk factor rules

Apply a factor to all enabled clients:

```sh
factor enable-all --factor 1.2
```

`enable-all` is persistent by default. Current matching clients are enrolled immediately, and future matching clients are enrolled by the daemon tick from their current counters. Traffic that happened before enrollment is not factored.

Compatible active single-user rules are consolidated into the persistent scope. Existing baselines and remainders are preserved, and the adopted single-user rules become merged metadata that cleanup can prune later.

A client belongs to the first active rule or scope that enrolls it, which matters when a client is attached to several inbounds with their own scopes. Another scope that also matches the client leaves it with the owner and records the skip once as a `scope_auto_enroll_skipped` audit event.

Apply a factor only to clients with a configured traffic limit:

```sh
factor enable-all --factor 1.2 --limited-only
```

Apply a factor only within one inbound:

```sh
factor enable-all --factor 1.2 --inbound-id 1
```

Use snapshot mode to target only clients that exist when the command runs:

```sh
factor enable-all --factor 1.2 --once
```

Paused and disabled bulk scopes do not enroll new clients. Resuming a paused scope refreshes existing baselines before enrollment continues.

Test a single-user rule before using `enable-all` on a production server.

## 9. Policy decisions

Before each tick, XuiFactor resolves one effective factor decision per client. Active excludes have highest precedence and mean no factor. Active user overrides come next and replace broader factors for one exact client. Single-user rules then apply before inbound persistent scopes, and inbound scopes apply before global persistent scopes.

This keeps overlapping metadata from double-applying factors. Suppressed lower-priority targets keep their baselines fresh from current counters, so they do not apply old traffic retroactively if they later become effective.

## 10. Exclude policies

Exclude one client from future factor application while keeping broad rules or scopes active:

```sh
factor exclude --email User --inbound-id 1
```

List active excludes:

```sh
factor excludes
```

Disable an exclude:

```sh
factor unexclude --email User --inbound-id 1
```

An exclude is tied to the client (traffic id and email). It stops future factor updates for that client but does not subtract previously factored traffic. The stored inbound is informational and follows the client when it moves between inbounds. `unexclude` refreshes the client's baselines from the current counters, so matching rules and scopes factor only traffic that arrives afterwards; traffic from the excluded period is not retroactively factored.

## 11. Override policies

Set a specific future factor for one client while keeping broad rules or scopes active:

```sh
factor override --email User --inbound-id 1 --factor 1.2
```

List active overrides:

```sh
factor overrides
```

Remove an override:

```sh
factor remove-override --email User --inbound-id 1
```

An override is tied to the client (traffic id and email). It replaces the broader matched factor for that client and does not stack with a scope factor. Exclude policies still win over overrides. The stored inbound is informational and follows the client when it moves between inbounds. Previously factored traffic remains unchanged, and removing an override does not retroactively change traffic that arrived while the override was active.

## 12. Explain and effective status

Inspect one client's final effective decision:

```sh
factor explain --email User --inbound-id 1
```

Show grouped effective policy totals:

```sh
factor status --effective
```

Show client-level effective factors for one inbound:

```sh
factor status --clients --inbound-id 1
```

Effective views use the same precedence as tick:

```text
exclude > override > single-user rule > inbound scope > global scope
```

Explain and effective status are read-only. They do not create audit events, refresh baselines, or modify 3x-ui counters. Read-only commands open SQLite in read-only mode and PostgreSQL in a read-only session.

## 13. Pause and resume

Pause one rule without changing counters:

```sh
factor pause --email User --inbound-id 1
```

Resume one paused rule from current counters:

```sh
factor resume --email User --inbound-id 1
```

Pause or resume all matching rules:

```sh
factor pause-all
factor resume-all
```

Traffic accumulated while a rule is paused is not factored when the rule resumes.

## 14. Disable behavior

Disable one rule:

```sh
factor disable --email User --inbound-id 1
```

Disable all active or paused rules:

```sh
factor disable-all
```

Disabling a rule does not subtract previously factored traffic. After disabling, future traffic is counted normally by 3x-ui.

## 15. One-shot tick

Run one factor tick manually:

```sh
factor tick
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

The service runs `xui-factor run` in daemon mode and polls at the interval configured in `/etc/xui-factor/config.json`.

Each tick runs in one write transaction. On SQLite the transaction holds the database write lock, so the panel waits up to its busy timeout. On PostgreSQL the tick locks only the traffic rows of the clients it factors and never waits for a row the panel is writing: that client is reported as `deferred`, its baseline stays unchanged, and the next tick factors the full delta. This keeps ticks from deadlocking with the panel's traffic jobs. `busy_timeout` bounds how long XuiFactor waits for any other lock.

A tick error that repeats is logged once; `tick: recovered` is logged when ticks succeed again.

## 17. Master and node panels

3x-ui 3.x can manage other panels as nodes. The master adds each node's counter deltas to its own `client_traffics` rows and pushes the combined usage back to the nodes, about every 30 seconds, for quota enforcement.

Install XuiFactor on the master only. There it sees every client, including clients attached only to node inbounds, and factors local and node traffic once. `--inbound-id` and inbound scopes accept node inbounds by their id on the master. Nodes enforce quotas with the factored totals the master pushes. In a chain of panels, install XuiFactor on the top master.

Running XuiFactor on a node as well would factor node traffic twice: once on the node, and again when the master adds the node's already factored delta. A panel counts as a managed node when a master pushed client usage to it within the last 24 hours. On such a panel ticks are refused with an error, and `doctor` reports `role: node` with `factoring: refused`.

To factor only the traffic that passes through one node, set `"factor_on_managed_node": true` in that node's config and do not factor the same clients on the master.

`doctor` reports the role as `standalone`, `master` (with the number of enabled nodes), or `node`.

## 18. Report, status, and audit

Show a concise management report:

```sh
factor report
factor report --inbound-id 1
```

Report summarizes active scopes, single-user rules, excludes, overrides, effective clients, service state, metadata state, and traffic impact. Traffic impact sums the `extra_up` and `extra_down` bytes of `traffic_applied` audit events within audit retention.

List effective active and paused rules:

```sh
factor status
```

Normal status hides orphaned, merged, and ineffective rules. Persistent scopes remain visible with their current materialized client count.

Normal status summarizes active scopes, active single-user rules, active policies, and effective factored clients. Active excludes and overrides are shown as policies. Scope lines show an `effective=` count when policies reduce the number of clients that currently receive the scope factor.

Use `status --effective` to inspect grouped final decisions, and `status --clients --inbound-id ID` to inspect client-level effective factors without large unfiltered output.

List all rules, including inactive metadata:

```sh
factor status --all
```

Show audit events for one client:

```sh
factor audit --email User --inbound-id 1
```

Filter audit events:

```sh
factor audit --event override_enabled --limit 20
factor audit --email User --inbound-id 1
factor audit --since 24h
```

Use status and audit after lifecycle changes to confirm the intended rule state.

## 19. Reconcile rules and policies

Reconcile active single-user rules and policies after a panel upgrade, bulk client changes, or manual database repair:

```sh
factor reconcile --dry-run
factor reconcile
```

Use `--inbound-id 1` to limit the repair to one inbound. Reconcile adopts compatible single-user rules into matching persistent scopes, marks ineffective active rules as orphaned, and never modifies 3x-ui counters. Orphaned rules are inactive and are pruned later by cleanup retention. The daemon runs the same reconciliation at the start of every tick.

When a client is deleted and recreated under the same email, the panel gives it a new traffic row. Reconcile follows the email to that row and re-links the rule (`client_relinked` audit event) instead of orphaning it. Active excludes and overrides move onto the current row too, counted as `policies repaired`; when a policy row already targets the current traffic row, the outdated one is disabled. Missing rule clients whose row has returned are revived with a refreshed baseline.

Renaming a client in the panel keeps its traffic row but changes the email. XuiFactor identifies a client by traffic id and email, so it treats the renamed client as a different client: single-user rules for the old email are orphaned, excludes and overrides for the old email stop matching, and persistent scopes enroll the renamed client from its current counters once cleanup has pruned the old tracking after `missing_client_grace`. Re-issue excludes and overrides for the new email.

Re-issuing `exclude` or `override` for a client whose stored row was replaced moves the existing row onto the current traffic row instead of creating a duplicate, and `unexclude` and `remove-override` still find the moved row.

## 20. Metadata cleanup

The daemon runs lightweight cleanup automatically when `auto_cleanup` is enabled. Deleted or replaced clients are first marked missing by traffic id and email. Missing client tracking is pruned after the grace period.

Run a dry run before manual cleanup on production servers:

```sh
factor cleanup --dry-run
```

Run cleanup with configured retention:

```sh
factor cleanup
```

Override disabled-rule and audit retention for one run:

```sh
factor cleanup --older-than 24h
```

Cleanup prunes only XuiFactor metadata, including inactive excludes and overrides after retention. It never modifies 3x-ui counters and does not subtract previously factored traffic. `VACUUM` is explicit only:

```sh
factor cleanup --vacuum
```

On SQLite this compacts the database file; on PostgreSQL it runs standard `VACUUM` maintenance.

## 21. Update workflow

From a new release package, run:

```sh
sudo ./scripts/update.sh
```

The update workflow preserves existing config, refreshes installed package files, enables `xui-factor.service`, and restarts it by default. Use `--no-start` or `--no-restart` to skip restart, and `--no-enable` to avoid enabling service startup.

When upgrading from an older 3x-ui version, upgrade the panel first. Stop `xui-factor.service`, upgrade 3x-ui, let it boot once so its database migration completes, install the new XuiFactor package, and then run:

```sh
factor doctor
factor reconcile --dry-run
factor reconcile
```

## 22. Uninstall workflow

Remove the installed binary, service, and shared package files while preserving config and backups:

```sh
sudo ./scripts/uninstall.sh
```

Remove XuiFactor-owned config and shared files:

```sh
sudo ./scripts/uninstall.sh --purge
```

Uninstall and purge do not remove `/etc/x-ui/x-ui.db` and do not touch a PostgreSQL panel database.

## 23. Recovery expectations

XuiFactor does not automatically restore backups. To recover from a selected backup:

1. Stop `xui-factor.service`.
2. Stop 3x-ui.
3. Restore the selected backup manually: replace the SQLite database file, or run `pg_restore --clean --if-exists --dbname "$XUI_DB_DSN" x-ui-<timestamp>.dump` on PostgreSQL.
4. Start 3x-ui.
5. Start `xui-factor.service`.
6. Run `factor doctor`.
7. Check `factor status` and `factor audit`.

Keep backup files until the recovered server has been verified.

## 24. Safety checklist

- Upgrade 3x-ui to 3.x and let the panel boot once before upgrading XuiFactor.
- With master and node panels, install XuiFactor on the master only.
- Run `factor doctor` first.
- Run `factor backup` before broad changes.
- Test a single user before `enable-all`.
- Use `factor exclude --email User --inbound-id 1` for clients that must not receive a broad factor.
- Use `factor override --email User --inbound-id 1 --factor 1.2` when one exact client needs a different future factor.
- Use `factor explain --email User --inbound-id 1` when the final decision is unclear.
- Use `--limited-only` if unlimited clients should be skipped.
- Run `factor reconcile --dry-run` after panel upgrades or bulk client changes.
- Run `factor cleanup --dry-run` before manual metadata cleanup.
- Verify `factor status` and `factor audit` after changes.
- Keep backup files before uninstall or purge.
