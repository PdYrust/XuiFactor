#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
PACKAGE_DIR=$(CDPATH= cd "$SCRIPT_DIR/.." && pwd)

. "$SCRIPT_DIR/installer-common.sh"

NO_ENABLE=0
NO_START=0

usage() {
	printf '%s\n' "usage: sh scripts/install.sh [--no-enable] [--no-start]"
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--no-enable)
			NO_ENABLE=1
			;;
		--no-start)
			NO_START=1
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
	validate_package
	install_binary_atomic
	install_shared_files
	install_default_config
	install_systemd_service
	activate_systemd_service "$NO_ENABLE" "$NO_START" start
	show_installed_version
	print_next_steps
}

main
