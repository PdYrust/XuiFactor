#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
PACKAGE_DIR=$(CDPATH= cd "$SCRIPT_DIR/.." && pwd)
PURGE=0

. "$SCRIPT_DIR/installer-common.sh"

usage() {
	printf '%s\n' "usage: sh scripts/uninstall.sh [--purge]"
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--purge)
			PURGE=1
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			die "unknown argument: $1"
			;;
	esac
	shift
done

main() {
	require_root
	require_linux
	disable_and_stop_service
	remove_systemd_service
	remove_installed_binary
	remove_shared_files
	if [ "$PURGE" -eq 1 ]; then
		assert_safe_purge_paths
		rm -rf "$(stage_path "$CONFIG_DIR")" "$(stage_path "$SHARE_DIR")"
		info "removed $CONFIG_DIR and $SHARE_DIR"
	else
		info "preserved config at $CONFIG_DIR"
	fi
	info "preserved backups at $BACKUP_DIR"
	info "did not modify /etc/x-ui/x-ui.db"
}

main
