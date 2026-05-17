#!/bin/sh
set -eu

BINARY_NAME=${BINARY_NAME:-xui-factor}
DISPLAY_NAME=${DISPLAY_NAME:-XuiFactor}
INSTALL_PREFIX=${INSTALL_PREFIX:-/usr/local}
BIN_DIR=${BIN_DIR:-$INSTALL_PREFIX/bin}
SHARE_DIR=${SHARE_DIR:-$INSTALL_PREFIX/share/$BINARY_NAME}
CONFIG_DIR=${CONFIG_DIR:-/etc/xui-factor}
CONFIG_FILE=${CONFIG_FILE:-$CONFIG_DIR/config.json}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/xui-factor}
SERVICE_NAME=${SERVICE_NAME:-xui-factor.service}
SYSTEMD_DIR=${SYSTEMD_DIR:-/etc/systemd/system}
SERVICE_FILE=${SERVICE_FILE:-$SYSTEMD_DIR/$SERVICE_NAME}
SYSTEMCTL=${SYSTEMCTL:-systemctl}
JOURNALCTL=${JOURNALCTL:-journalctl}
DESTDIR=${DESTDIR:-}

die() {
	printf '%s\n' "error: $*" >&2
	exit 1
}

info() {
	printf '%s\n' "info: $*"
}

warn() {
	printf '%s\n' "warning: $*" >&2
}

require_root() {
	validate_destdir
	if staged_mode; then
		return
	fi
	if [ "$(id -u)" -ne 0 ]; then
		die "run this script as root"
	fi
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require_linux() {
	[ "$(uname -s)" = "Linux" ] || die "$DISPLAY_NAME is supported on Linux only"
}

staged_mode() {
	[ -n "$DESTDIR" ]
}

validate_destdir() {
	if [ -z "$DESTDIR" ]; then
		return
	fi
	case "$DESTDIR" in
		/*) ;;
		*) die "DESTDIR must be an absolute path" ;;
	esac
}

stage_path() {
	if staged_mode; then
		printf '%s%s\n' "${DESTDIR%/}" "$1"
	else
		printf '%s\n' "$1"
	fi
}

package_dir() {
	if [ -n "${PACKAGE_DIR:-}" ]; then
		printf '%s\n' "$PACKAGE_DIR"
		return
	fi
	if [ -n "${SCRIPT_DIR:-}" ]; then
		CDPATH= cd "$SCRIPT_DIR/.." && pwd
		return
	fi
	die "cannot determine package directory"
}

validate_package() {
	pkg=$(package_dir)
	for path in \
		"$BINARY_NAME" \
		"README.md" \
		"LICENSE" \
		"VERSION" \
		"docs/OPERATIONS.md" \
		"scripts/install.sh" \
		"scripts/update.sh" \
		"scripts/uninstall.sh" \
		"scripts/installer-common.sh" \
		"config/config.json" \
		"systemd/$SERVICE_NAME"; do
		[ -f "$pkg/$path" ] || die "package file missing: $path"
	done
	[ -x "$pkg/$BINARY_NAME" ] || die "package binary is not executable"
}

install_binary_atomic() {
	pkg=$(package_dir)
	src="$pkg/$BINARY_NAME"
	dst=$(stage_path "$BIN_DIR/$BINARY_NAME")
	tmp=$(stage_path "$BIN_DIR/.$BINARY_NAME.$$")

	require_command install
	mkdir -p "$(dirname "$dst")"
	rm -f "$tmp"
	install -m 0755 "$src" "$tmp"
	mv -f "$tmp" "$dst"
}

install_shared_files() {
	pkg=$(package_dir)
	share_dir=$(stage_path "$SHARE_DIR")
	require_command install
	mkdir -p "$share_dir/scripts" "$share_dir/config" "$share_dir/systemd" "$share_dir/docs"
	install -m 0644 "$pkg/README.md" "$share_dir/README.md"
	install -m 0644 "$pkg/LICENSE" "$share_dir/LICENSE"
	install -m 0644 "$pkg/VERSION" "$share_dir/VERSION"
	install -m 0644 "$pkg/docs/OPERATIONS.md" "$share_dir/docs/OPERATIONS.md"
	install -m 0755 "$pkg/scripts/install.sh" "$share_dir/scripts/install.sh"
	install -m 0755 "$pkg/scripts/update.sh" "$share_dir/scripts/update.sh"
	install -m 0755 "$pkg/scripts/uninstall.sh" "$share_dir/scripts/uninstall.sh"
	install -m 0755 "$pkg/scripts/installer-common.sh" "$share_dir/scripts/installer-common.sh"
	install -m 0644 "$pkg/config/config.json" "$share_dir/config/config.json"
	install -m 0644 "$pkg/systemd/$SERVICE_NAME" "$share_dir/systemd/$SERVICE_NAME"
}

install_default_config() {
	pkg=$(package_dir)
	config_dir=$(stage_path "$CONFIG_DIR")
	config_file=$(stage_path "$CONFIG_FILE")
	require_command install
	mkdir -p "$config_dir"
	if [ -f "$config_file" ]; then
		info "preserved existing config at $CONFIG_FILE"
		return
	fi
	install -m 0644 "$pkg/config/config.json" "$config_file"
	info "created default config at $CONFIG_FILE"
}

has_systemd() {
	if [ "${XUI_FACTOR_TEST_SYSTEMD:-0}" = "1" ]; then
		command -v "$SYSTEMCTL" >/dev/null 2>&1
		return
	fi
	if staged_mode; then
		return 1
	fi
	command -v "$SYSTEMCTL" >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

systemd_available() {
	has_systemd
}

render_service_file() {
	cat <<EOF
[Unit]
Description=XuiFactor 3x-ui traffic factor sidecar
After=network.target x-ui.service

[Service]
Type=simple
User=root
Group=root
ExecStart=$BIN_DIR/$BINARY_NAME --config $CONFIG_FILE run
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
}

install_systemd_service() {
	SERVICE_CHANGED=0
	service_file=$(stage_path "$SERVICE_FILE")
	mkdir -p "$(dirname "$service_file")"
	tmp="$service_file.$$"
	if ! render_service_file > "$tmp"; then
		rm -f "$tmp"
		die "service file install path is not writable: $(dirname "$service_file")"
	fi
	if [ -f "$service_file" ] && cmp -s "$tmp" "$service_file"; then
		rm -f "$tmp"
	else
		mv -f "$tmp" "$service_file"
		SERVICE_CHANGED=1
		if staged_mode; then
			info "installed staged systemd service at $service_file"
		else
			info "installed systemd service at $SERVICE_FILE"
		fi
	fi
}

systemd_daemon_reload() {
	if ! has_systemd; then
		return 0
	fi
	"$SYSTEMCTL" daemon-reload >/dev/null 2>&1 || die "failed to reload systemd"
}

systemd_enable_service() {
	"$SYSTEMCTL" enable "$SERVICE_NAME" >/dev/null 2>&1 || die "failed to enable $SERVICE_NAME"
	info "enabled $SERVICE_NAME"
}

systemd_start_service() {
	"$SYSTEMCTL" start "$SERVICE_NAME" >/dev/null 2>&1
}

systemd_restart_service() {
	"$SYSTEMCTL" restart "$SERVICE_NAME" >/dev/null 2>&1
}

systemd_assert_active() {
	"$SYSTEMCTL" is-active --quiet "$SERVICE_NAME" >/dev/null 2>&1
}

systemd_failure_diagnostics() {
	action=$1
	printf '%s\n' "error: failed to $action $SERVICE_NAME" >&2
	printf '%s\n' "diagnostic: systemctl status $SERVICE_NAME --no-pager" >&2
	printf '%s\n' "diagnostic: journalctl -u $SERVICE_NAME -n 80 --no-pager" >&2
}

activate_systemd_service() {
	no_enable=$1
	no_start=$2
	action=$3

	if staged_mode && [ "${XUI_FACTOR_TEST_SYSTEMD:-0}" != "1" ]; then
		info "staged install; skipped systemd service control"
		return 0
	fi
	if ! has_systemd; then
		warn "systemd is not available; $SERVICE_NAME was not enabled or started"
		warn "run manually: $BIN_DIR/$BINARY_NAME --config $CONFIG_FILE run"
		return 0
	fi

	systemd_daemon_reload
	if [ "$no_enable" -eq 0 ]; then
		systemd_enable_service
	else
		info "service enable skipped"
	fi

	if [ "$no_start" -ne 0 ]; then
		info "service start skipped"
		return 0
	fi

	if [ "$action" = "restart" ]; then
		if ! systemd_restart_service; then
			systemd_failure_diagnostics "restart"
			return 1
		fi
	else
		if ! systemd_start_service; then
			systemd_failure_diagnostics "start"
			return 1
		fi
	fi
	if ! systemd_assert_active; then
		systemd_failure_diagnostics "$action"
		return 1
	fi
	info "$SERVICE_NAME is active"
}

service_is_active() {
	has_systemd && "$SYSTEMCTL" is-active --quiet "$SERVICE_NAME"
}

stop_service_if_active() {
	SERVICE_WAS_ACTIVE=0
	if service_is_active; then
		SERVICE_WAS_ACTIVE=1
		"$SYSTEMCTL" stop "$SERVICE_NAME"
	fi
}

restart_service_if_was_active() {
	if [ "${SERVICE_WAS_ACTIVE:-0}" -eq 1 ] && has_systemd; then
		"$SYSTEMCTL" start "$SERVICE_NAME"
	fi
}

disable_and_stop_service() {
	if staged_mode; then
		return
	fi
	if has_systemd; then
		"$SYSTEMCTL" stop "$SERVICE_NAME" >/dev/null 2>&1 || true
		"$SYSTEMCTL" disable "$SERVICE_NAME" >/dev/null 2>&1 || true
	fi
}

remove_systemd_service() {
	service_file=$(stage_path "$SERVICE_FILE")
	if [ -f "$service_file" ]; then
		rm -f "$service_file"
		if ! staged_mode && has_systemd; then
			"$SYSTEMCTL" daemon-reload >/dev/null 2>&1 || true
		fi
		info "removed $SERVICE_FILE"
	fi
}

remove_installed_binary() {
	dst=$(stage_path "$BIN_DIR/$BINARY_NAME")
	if [ -e "$dst" ]; then
		rm -f "$dst"
		info "removed $BIN_DIR/$BINARY_NAME"
	else
		info "$DISPLAY_NAME is not installed at $BIN_DIR/$BINARY_NAME"
	fi
}

assert_safe_share_path() {
	case "$SHARE_DIR" in
		*xui-factor) ;;
		*) die "refusing removal of non-XuiFactor share path: $SHARE_DIR" ;;
	esac
	case "$SHARE_DIR" in
		/etc/x-ui|/etc/x-ui/*)
			die "refusing removal path inside 3x-ui data directory: $SHARE_DIR"
			;;
	esac
}

remove_shared_files() {
	assert_safe_share_path
	share_dir=$(stage_path "$SHARE_DIR")
	if [ -e "$share_dir" ]; then
		rm -rf "$share_dir"
		info "removed $SHARE_DIR"
	fi
}

assert_safe_purge_paths() {
	for path in "$CONFIG_DIR" "$SHARE_DIR"; do
		case "$path" in
			*xui-factor) ;;
			*)
				die "refusing purge of non-XuiFactor path: $path"
				;;
		esac
		case "$path" in
			/etc/x-ui|/etc/x-ui/*)
				die "refusing purge path inside 3x-ui data directory: $path"
				;;
		esac
	done
}

show_installed_version() {
	if staged_mode; then
		return
	fi
	dst="$BIN_DIR/$BINARY_NAME"
	if [ -x "$dst" ]; then
		"$dst" version || true
	fi
}

print_next_steps() {
	info "$DISPLAY_NAME installed"
	info "config: $CONFIG_FILE"
	if staged_mode; then
		info "staged root: $DESTDIR"
		return
	fi
	if has_systemd; then
		info "check service: systemctl status $SERVICE_NAME --no-pager"
	else
		info "run manually: $BIN_DIR/$BINARY_NAME --config $CONFIG_FILE run"
	fi
}
