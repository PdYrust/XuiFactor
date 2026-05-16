<p align="center">
  <img src="assets/logo/xui-factor-icon.svg" alt="XuiFactor logo" width="96" height="96">
</p>

<h1 align="center">XuiFactor</h1>

<p align="center">Traffic factor sidecar for 3x-ui on Linux.</p>

<p align="center">
  <a href="LICENSE"><img alt="License: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-111111"></a>
  <img alt="Go 1.22+" src="https://img.shields.io/badge/go-1.22%2B-00ADD8">
  <img alt="Status: beta" src="https://img.shields.io/badge/status-beta-111111">
  <a href="https://t.me/PdYrust"><img alt="Telegram channel" src="https://img.shields.io/badge/Telegram-%40PdYrust-229ED9?logo=telegram&logoColor=white"></a>
</p>

XuiFactor applies temporary traffic factors to 3x-ui client counters as a sidecar. It does not modify 3x-ui source code. Rule metadata is stored in the same SQLite database using `xui_factor_*` metadata tables.

Disabling a rule keeps previously factored traffic results. Future traffic after disabling is counted normally.

For server operation workflows, see [docs/OPERATIONS.md](docs/OPERATIONS.md).

## Install

Install from a release package:

```sh
tar -xzf xui-factor_v0.1.0-beta_linux_amd64.tar.gz
cd xui-factor_v0.1.0-beta_linux_amd64
sudo ./scripts/install.sh
```

Use the matching `linux_arm64` package on ARM64 hosts.

Install from a local checkout:

```sh
make build
sudo ./scripts/install.sh
```

Defaults:

```text
config: /etc/xui-factor/config.json
database: /etc/x-ui/x-ui.db
service: xui-factor.service
```

## Common Commands

```sh
xui-factor --help
xui-factor doctor
xui-factor backup
xui-factor enable --email User --inbound-id 1 --factor 1.2
xui-factor enable-all --factor 1.2
xui-factor enable-all --factor 1.2 --limited-only
xui-factor disable --email User --inbound-id 1
xui-factor disable-all
xui-factor status
xui-factor audit --email User --inbound-id 1
xui-factor tick
sudo systemctl enable --now xui-factor.service
sudo systemctl status xui-factor.service
```

## Operation Examples

Apply a factor to one client:

```sh
xui-factor enable --email user@example.com --inbound-id 1 --factor 1.2
xui-factor status
```

Apply a factor to enabled clients:

```sh
xui-factor enable-all --factor 1.2
```

Apply a factor only to limited clients:

```sh
xui-factor enable-all --factor 1.2 --limited-only
```

Stop applying factors:

```sh
xui-factor disable --email user@example.com --inbound-id 1
xui-factor disable-all
```

## Factor Behavior

`factor 1.2` means new traffic is counted as 120%. `factor 5` means new traffic is counted as 500%.

Disabling a rule does not revert previously factored traffic. Traffic recorded after disabling is counted normally by 3x-ui.

## Safety Notes

Run a backup before broad changes:

```sh
xui-factor backup
```

Test single-user rules before bulk rules on production servers.

## Uninstall

```sh
sudo ./scripts/uninstall.sh
sudo ./scripts/uninstall.sh --purge
```

Uninstall does not remove `/etc/x-ui/x-ui.db`.

## Project

Creator: YrustPd  
Repository: https://github.com/PdYrust/XuiFactor  
Channel: https://t.me/PdYrust

## License

XuiFactor is licensed under the GNU Affero General Public License v3.0. See [LICENSE](LICENSE).
