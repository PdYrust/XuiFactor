# Changelog

All notable changes to XuiFactor are documented in this file.

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
