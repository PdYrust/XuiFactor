# Changelog

All notable changes to XuiFactor are documented in this file.

## v0.4.0 - Stable

### Added

- First stable release notes.
- GitHub issue templates for bug reports, feature requests, installation/update problems, configuration/server environment issues, documentation issues, and support requests.

### Changed

- Release metadata, package examples, and release workflow defaults now point to v0.4.0 stable package names.
- Documentation was polished for the stable operator workflow across install, update, service lifecycle, policies, explain/status/report/audit, cleanup, and reconcile.
- README status badge now reflects the stable release line.

### Safety

- No runtime factor behavior changes were added in this stable pass.
- Existing migration, policy precedence, package, installer, and read-only command safety tests remain part of the release validation.

## v0.3.5-beta - Unreleased

### Added

- Migration hardening tests for representative older beta metadata schemas.
- Broad CLI smoke coverage across major operator commands before the stable release.
- v0.3.5-beta release notes.

### Changed

- Normal `status` now reports active scopes, active single-user rules, and effective factored clients directly.
- Metadata migration now adds missing columns before creating dependent indexes.
- Metadata migration backfills materialized client inbound and email identity from current 3x-ui rows or rule metadata when upgrading older schemas.

### Safety

- Repeated migrations are no-ops and preserve existing XuiFactor metadata.
- Migration hardening does not modify `client_traffics` counters.
- Read-only command smoke coverage now exercises status, explain, report, audit, doctor, and client status paths.

## v0.3.4-beta - Unreleased

### Added

- `report` command for concise management-level policy, client, metadata, service, and traffic impact summaries.
- Audit filters for event type, email, inbound, rule id, policy id, limit, and relative time windows.
- Traffic impact aggregation from `traffic_applied` events that include extra applied bytes.
- v0.3.4-beta release notes.

### Changed

- Audit output now uses the shared sectioned output style and concise event lines.
- Tick traffic audit events now include inbound and email metadata for forward-looking report and audit filters.

### Safety

- Report and audit are read-only and do not create audit events.
- Traffic impact reporting aggregates existing event metadata without rewriting historical events or modifying counters.

## v0.3.3-beta - Unreleased

### Added

- `explain` command for one-client effective decision inspection.
- `status --effective` summary view for final policy outcomes.
- `status --clients` client-level effective factor view with inbound filtering.
- v0.3.3-beta release notes.

### Changed

- Explain and effective status reuse the policy decision layer used by tick.
- Status client output shows effective factor, source, traffic id, inbound, and state.
- `status --clients` without an inbound filter limits output and prints a hint to avoid noisy terminal output.

### Safety

- Explain and effective status are read-only and do not create audit events.
- Effective views preserve the same precedence order as tick: exclude, override, single-user rule, inbound scope, global scope.
- Effective views do not mutate counters or baselines.

## v0.3.2-beta - Unreleased

### Added

- `override`, `remove-override`, and `overrides` commands for exact-client factor override policies.
- Dedicated `xui_factor_overrides` metadata table using traffic id, inbound id, and email identity.
- Status, doctor, cleanup, and audit support for override policy lifecycle.
- v0.3.2-beta release notes.

### Changed

- Effective policy decisions now apply precedence as exclude, user override, single-user rule, inbound scope, global scope.
- Active overrides replace broader scope factors for the exact client instead of stacking with them.
- Status shows active override counts and policy lines, with scope `effective=` counts adjusted when overrides suppress scope factor ownership.

### Safety

- Enabling, updating, or removing an override refreshes matching active baselines from current counters.
- Removing an override does not retroactively factor traffic that arrived while the override was active.
- Cleanup can prune inactive override metadata after retention and never prunes active overrides.

## v0.3.1-beta - Unreleased

### Added

- `exclude`, `unexclude`, and `excludes` commands for client exception policies.
- Dedicated `xui_factor_excludes` metadata table using traffic id, inbound id, and email identity.
- Status and doctor policy counts for active exclude policies.
- v0.3.1-beta release notes.

### Changed

- Effective policy decisions now include active excludes as the highest-precedence no-factor decision.
- Status shows effective client counts when excludes suppress materialized scope clients.

### Safety

- Excluded clients do not receive factor updates, and suppressed baselines are refreshed from current counters.
- Removing an exclude does not retroactively factor traffic that arrived while the exclude was active.
- Cleanup can prune inactive exclude metadata after retention and never prunes active excludes.

## v0.3.0-beta - Unreleased

### Added

- Internal policy decision foundation for one effective factor decision per client.
- Deterministic precedence for overlapping policy sources: exclude, user override, inbound scope, then global scope.
- Shared CLI output layer for concise sectioned summaries across operational commands.
- v0.3.0-beta release notes.

### Changed

- `status`, `doctor`, lifecycle, bulk, tick, cleanup, reconcile, and backup output now use a cleaner operator-facing format.
- Tick processing now evaluates effective rule ownership before applying deltas, keeping overlapping active metadata from double-applying factors.

### Safety

- Existing persistent scopes, single-user rules, reconciliation, cleanup, and snapshot mode remain compatible.
- Suppressed lower-priority active targets refresh baselines from current counters instead of accumulating retroactive deltas.

## v0.2.3-beta - Unreleased

### Added

- Install and update now enable and start `xui-factor.service` by default on systemd hosts.
- `install.sh` supports `--no-enable` and `--no-start`.
- `update.sh` supports `--no-enable`, `--no-start`, and `--no-restart`.
- Doctor now reports service installed, enabled, and active states separately.
- v0.2.3-beta release notes.

### Fixed

- Update starts the service even when it was previously disabled or inactive, unless explicitly skipped.
- Install and update print service failure diagnostics with `systemctl status` and `journalctl` commands.
- Doctor warns when active rules or persistent scopes require the service but it is not running.

### Safety

- DESTDIR installer runs do not control host systemd unless an explicit test mock is enabled.
- Service lifecycle changes do not touch 3x-ui counters or `/etc/x-ui/x-ui.db`.

## v0.2.2-beta - Unreleased

### Added

- `reconcile` command with dry-run and inbound filtering for legacy active single-user rules.
- Tick-time reconciliation before scope enrollment and traffic factor application.
- v0.2.2-beta release notes.

### Fixed

- Active single-user rules with zero clients, missing clients, mismatched identities, or ineligible disabled clients are reconciled out of the active effective set.
- Normal status now shows only effective active or paused work by default.
- Compatible legacy single-user rules left beside persistent scopes are adopted or superseded without changing traffic counters.

### Safety

- Reconciliation never modifies 3x-ui counters and marks ineffective metadata inactive before any tick updates can run.
- Orphaned legacy rules are retained for audit/status history and are pruned by cleanup retention.

## v0.2.1-beta - Unreleased

### Fixed

- Persistent `enable-all` now consolidates compatible active single-user rules into matching scopes.
- Existing rule-client baselines and remainders are preserved during consolidation.
- Compatible merged rules are no longer active effective rules and can be pruned by cleanup retention.

### Safety

- Consolidation never modifies 3x-ui counters and avoids duplicate active targets for the same traffic row.
- Incompatible, disabled, unlimited, or mismatched clients are skipped and audited instead of being adopted.

## v0.2.0-beta - Unreleased

### Added

- Persistent `enable-all` scopes that auto-enroll future matching clients.
- `enable-all --once` snapshot mode for current clients only.
- `cleanup` command with dry-run, retention override, and explicit VACUUM support.
- Automatic metadata cleanup for missing clients, disabled rules/scopes, and old audit events.
- v0.2.0-beta release notes.

### Safety

- Client tracking uses traffic id, inbound id, and email identity.
- Missing or mismatched clients are marked before pruning.
- Cleanup prunes only XuiFactor metadata and never modifies 3x-ui counters.

## v0.1.0-beta - Unreleased

### Added

- Single-user factor rules for 3x-ui client traffic.
- Bulk factor rules for enabled, limited, and inbound-scoped clients.
- Pause, resume, and disable lifecycle commands with keep-result behavior.
- Daemon tick mode and one-shot tick execution.
- SQLite metadata tables using `xui_factor_*`.
- JSON config file support.
- Doctor diagnostics for config, database, schema, metadata, backups, rules, and service state.
- SQLite-consistent backup command.

### Safety

- Compare-and-swap traffic updates during ticks.
- Reset-aware baseline handling.
- Conflict detection for duplicate active targets.
- Disable behavior that keeps previously factored results and counts future traffic normally.

### Packaging

- Linux release packages for amd64 and arm64.
- systemd service file for daemon operation.
- Install, update, and uninstall scripts.
- Package verification for archive contents, checksums, manifest, config, service file, and staged installer behavior.

### Documentation

- RayLimit-style README.
- Server operations guide.
- First beta release notes.
