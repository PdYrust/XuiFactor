# Changelog

All notable changes to XuiFactor are documented in this file.

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
