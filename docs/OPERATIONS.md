# XuiFactor Operations Guide

## 1. Overview

XuiFactor is a Linux sidecar for 3x-ui servers. It applies temporary traffic factor rules to 3x-ui client counters stored in SQLite without modifying 3x-ui source code.

Rule metadata is stored in the same SQLite database using `xui_factor_*` metadata tables. Factored traffic remains in the 3x-ui counters after a rule is disabled, and future traffic after disabling is counted normally.

Repository: https://github.com/PdYrust/XuiFactor

## 2. Installation from release package

Download the release archive that matches the server architecture, then install from the extracted package:

```sh
tar -xzf xui-factor_v0.1.0-beta_linux_amd64.tar.gz
cd xui-factor_v0.1.0-beta_linux_amd64
sudo ./scripts/install.sh
```

Use the `linux_arm64` archive on ARM64 hosts.

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

Apply a factor only to clients with a configured traffic limit:

```sh
xui-factor enable-all --factor 1.2 --limited-only
```

Apply a factor only within one inbound:

```sh
xui-factor enable-all --factor 1.2 --inbound-id 1
```

Test a single-user rule before using `enable-all` on a production server.

## 9. Pause and resume

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

## 10. Disable behavior

Disable one rule:

```sh
xui-factor disable --email User --inbound-id 1
```

Disable all active or paused rules:

```sh
xui-factor disable-all
```

Disabling a rule does not subtract previously factored traffic. After disabling, future traffic is counted normally by 3x-ui.

## 11. One-shot tick

Run one factor tick manually:

```sh
xui-factor tick
```

This is useful after enabling a rule, during maintenance checks, or before starting the daemon service.

## 12. Daemon service operation

Enable and start the service:

```sh
sudo systemctl enable --now xui-factor.service
```

Restart the service:

```sh
sudo systemctl restart xui-factor.service
```

Check service state:

```sh
sudo systemctl status xui-factor.service
```

Follow service logs:

```sh
journalctl -u xui-factor.service -f
```

The service runs `xui-factor` in daemon mode and polls at the interval configured in `/etc/xui-factor/config.json`.

## 13. Status and audit

List active and paused rules:

```sh
xui-factor status
```

List all rules, including disabled rules:

```sh
xui-factor status --all
```

Show audit events for one client:

```sh
xui-factor audit --email User --inbound-id 1
```

Use status and audit after lifecycle changes to confirm the intended rule state.

## 14. Update workflow

From a new release package, run:

```sh
sudo ./scripts/update.sh
```

The update workflow preserves existing config, refreshes installed package files, and restarts `xui-factor.service` only when it was active before the update.

## 15. Uninstall workflow

Remove the installed binary, service, and shared package files while preserving config and backups:

```sh
sudo ./scripts/uninstall.sh
```

Remove XuiFactor-owned config and shared files:

```sh
sudo ./scripts/uninstall.sh --purge
```

Uninstall and purge must not remove `/etc/x-ui/x-ui.db`.

## 16. Recovery expectations

XuiFactor does not automatically restore backups. To recover from a selected backup:

1. Stop `xui-factor.service`.
2. Stop 3x-ui.
3. Restore the selected backup database manually.
4. Start 3x-ui.
5. Start `xui-factor.service`.
6. Run `xui-factor doctor`.
7. Check `xui-factor status` and `xui-factor audit`.

Keep backup files until the recovered server has been verified.

## 17. Safety checklist

- Run `xui-factor doctor` first.
- Run `xui-factor backup` before broad changes.
- Test a single user before `enable-all`.
- Use `--limited-only` if unlimited clients should be skipped.
- Verify `xui-factor status` and `xui-factor audit` after changes.
- Keep backup files before uninstall or purge.
