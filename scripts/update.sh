#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
PACKAGE_DIR=$(CDPATH= cd "$SCRIPT_DIR/.." && pwd)

. "$SCRIPT_DIR/installer-common.sh"

main() {
	require_root
	require_linux
	validate_package
	stop_service_if_active
	install_binary_atomic
	install_shared_files
	install_default_config
	install_systemd_service
	restart_service_if_was_active
	show_installed_version
	info "$DISPLAY_NAME updated"
}

main "$@"
