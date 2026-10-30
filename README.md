<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo/xui-factor-icon-white.svg">
    <img src="assets/logo/xui-factor-icon.svg" alt="XuiFactor logo" width="96" height="96">
  </picture>
</p>

<h1 align="center">XuiFactor</h1>

<p align="center">Traffic factor sidecar for 3x-ui 3.x on Linux, over SQLite or PostgreSQL.</p>

<p align="center">
  <a href="LICENSE"><img alt="License: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-111111"></a>
  <img alt="Go 1.25+" src="https://img.shields.io/badge/go-1.25%2B-00ADD8">
  <img alt="Status: stable" src="https://img.shields.io/badge/status-stable-111111">
  <a href="https://t.me/PdYrust"><img alt="Telegram channel" src="https://img.shields.io/badge/Telegram-%40PdYrust-229ED9?logo=telegram&logoColor=white"></a>
</p>

XuiFactor applies temporary traffic factors to 3x-ui client counters as a sidecar. It does not modify 3x-ui source code. Rule metadata is stored in the same database using `xui_factor_*` metadata tables, whether the panel runs on its SQLite file or a PostgreSQL database.

**Requires 3x-ui 3.x** (tested against 3.10.2). Upgrade 3x-ui first and let the panel finish its boot migration before upgrading XuiFactor. On 3.x a client is identified by email and can be attached to several inbounds; inbound scoped commands resolve that membership through the panel's `clients` and `client_inbounds` tables.

Disabling a rule keeps previously factored traffic results. Future traffic after disabling is counted normally.

With 3x-ui master and node panels, install XuiFactor on the master only. The master already adds node traffic to its counters, so it factors local and node traffic once; on a panel managed by a master, ticks are refused to prevent double factoring.

For server operation workflows, see [docs/OPERATIONS.md](docs/OPERATIONS.md).

## Install

Install from a release package:

```sh
tar -xzf xui-factor_v0.6.0_linux_amd64.tar.gz
cd xui-factor_v0.6.0_linux_amd64
sudo ./scripts/install.sh
```

Use the matching `linux_arm64` package on ARM64 hosts.

After installation, run XuiFactor with the short `factor` command, for example `factor doctor`. The full `xui-factor` name works too. Numeric calls such as `factor 42` still reach the system `factor` command from coreutils.

On systemd hosts, install enables and starts `xui-factor.service` by default. Use `--no-start` or `--no-enable` only when another init workflow will run the daemon.

Install from a local checkout:

```sh
make build
sudo ./scripts/install.sh
```

Defaults:

```text
config: /etc/xui-factor/config.json
database: /etc/x-ui/x-ui.db (SQLite) or the panel PostgreSQL DSN
service: xui-factor.service
```

Point XuiFactor at a PostgreSQL panel with `database_backend` and `database_dsn` in the config file, or leave the defaults: like the panel itself, XuiFactor uses PostgreSQL when `XUI_DB_TYPE` selects it and connects with `XUI_DB_DSN`, read from the environment or the panel service environment file (`/etc/default/x-ui`, `/etc/conf.d/x-ui`, or `/etc/sysconfig/x-ui`). See [docs/OPERATIONS.md](docs/OPERATIONS.md) for backend configuration and upgrade order.

## Common Commands

```sh
factor --help
factor doctor
factor backup
factor enable --email User --inbound-id 1 --factor 1.2
factor enable-all --factor 1.2
factor enable-all --factor 1.2 --limited-only
factor enable-all --factor 1.2 --once
factor exclude --email User --inbound-id 1
factor excludes
factor unexclude --email User --inbound-id 1
factor override --email User --inbound-id 1 --factor 1.2
factor overrides
factor remove-override --email User --inbound-id 1
factor explain --email User --inbound-id 1
factor status --effective
factor status --clients --inbound-id 1
factor report
factor audit --event override_enabled --limit 20
factor audit --since 24h
factor disable --email User --inbound-id 1
factor disable-all
factor reconcile --dry-run
factor cleanup --dry-run
factor status
factor audit --email User --inbound-id 1
factor tick
sudo systemctl enable --now xui-factor.service
sudo systemctl status xui-factor.service --no-pager
```

## Operation Examples

Apply a factor to one client:

```sh
factor enable --email user@example.com --inbound-id 1 --factor 1.2
factor status
```

Apply a factor to enabled clients. By default, `enable-all` creates a persistent scope, so future matching clients are enrolled automatically from their current counters:

```sh
factor enable-all --factor 1.2
```

When possible, persistent `enable-all` also consolidates compatible active single-user rules into the scope while preserving their baselines.

Use `factor reconcile` to repair rule and policy metadata after panel upgrades or client changes. Normal status shows only effective active or paused rules; rules retired by reconcile stay visible through `status --all` until cleanup prunes them after retention.

Apply a factor only to limited clients:

```sh
factor enable-all --factor 1.2 --limited-only
```

Use snapshot mode when only current clients should be targeted:

```sh
factor enable-all --factor 1.2 --once
```

Stop applying factors:

```sh
factor disable --email user@example.com --inbound-id 1
factor disable-all
```

## Factor Behavior

`factor 1.2` means new traffic is counted as 120%. `factor 5` means new traffic is counted as 500%.

Disabling a rule does not revert previously factored traffic. Traffic recorded after disabling is counted normally by 3x-ui.

## Policy Behavior

XuiFactor evaluates one effective decision per client before applying traffic deltas. Excludes win first, then user overrides, then single-user rules, inbound scopes, and global scopes. Overlapping metadata does not double-apply factors.

Use `factor exclude --email User --inbound-id 1` to keep one client out of matching rules and scopes. Previously factored traffic remains unchanged. `factor unexclude --email User --inbound-id 1` resumes normal policy matching from the current counters, without retroactively factoring traffic from the excluded period.

Use `factor override --email User --inbound-id 1 --factor 1.2` to give one exact client a specific future factor while broader scopes remain active. Overrides do not stack with scope factors. `factor remove-override --email User --inbound-id 1` returns the client to matching rules and scopes from the current counters.

Use `factor explain --email User --inbound-id 1` to inspect the final effective decision for one client. Use `factor status --effective` for grouped policy totals and `factor status --clients --inbound-id 1` for client-level effective factors.

Use `factor report` for a concise policy, effective client, service, and traffic impact summary. Use audit filters such as `factor audit --event override_enabled --limit 20` or `factor audit --since 24h` to inspect recent lifecycle events without noisy output.

## Metadata Cleanup

XuiFactor keeps its own metadata bounded with automatic cleanup enabled by default. Missing client tracking is pruned after 30 seconds, disabled rules after 7 days, and audit events after 30 days.

Use `factor cleanup --dry-run` before manual cleanup on production servers. Cleanup only prunes XuiFactor metadata, including orphaned rules after retention, and never modifies 3x-ui counters. `VACUUM` runs only when explicitly requested with `factor cleanup --vacuum`.

## Safety Notes

Run a backup before broad changes:

```sh
factor backup
```

Test single-user rules before bulk rules on production servers. With master and node panels, run `factor doctor` on the master and confirm `role: master`.

## Uninstall

```sh
sudo ./scripts/uninstall.sh
sudo ./scripts/uninstall.sh --purge
```

Uninstall does not remove `/etc/x-ui/x-ui.db` or touch a PostgreSQL panel database.

## Project

Creator: YrustPd  
Repository: https://github.com/PdYrust/XuiFactor  
Channel: https://t.me/PdYrust

## License

XuiFactor is licensed under the GNU Affero General Public License v3.0. See [LICENSE](LICENSE).
