#!/bin/sh
set -eu

PACKAGE_DIR=${1:?package directory is required}
WORK_DIR=${2:?work directory is required}

APP=xui-factor
SERVICE=xui-factor.service

die() {
	printf '%s\n' "error: $*" >&2
	exit 1
}

make_mock_systemctl() {
	dir=$1
	mkdir -p "$dir"
	mock="$dir/systemctl"
	cat > "$mock" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
case "$1" in
	daemon-reload|enable|stop|disable)
		exit 0
		;;
	start)
		if [ "${SYSTEMCTL_FAIL_START:-0}" = "1" ]; then
			exit 1
		fi
		exit 0
		;;
	restart)
		if [ "${SYSTEMCTL_FAIL_RESTART:-0}" = "1" ]; then
			exit 1
		fi
		exit 0
		;;
	is-active)
		if [ "${SYSTEMCTL_ACTIVE:-active}" = "active" ]; then
			exit 0
		fi
		printf '%s\n' "${SYSTEMCTL_ACTIVE:-inactive}"
		exit 3
		;;
	*)
		exit 0
		;;
esac
EOF
	chmod 0755 "$mock"
	printf '%s\n' "$mock"
}

assert_log_has() {
	log=$1
	pattern=$2
	grep -Fqx "$pattern" "$log" || die "systemctl log missing: $pattern"
}

assert_log_lacks_word() {
	log=$1
	word=$2
	if [ -f "$log" ] && grep -Eq "(^| )$word( |$)" "$log"; then
		die "systemctl log unexpectedly contains: $word"
	fi
}

run_install_default() {
	root="$WORK_DIR/install-default-root"
	log="$WORK_DIR/install-default-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/install-default-bin")
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/install.sh" >/dev/null
	assert_log_has "$log" "daemon-reload"
	assert_log_has "$log" "enable $SERVICE"
	assert_log_has "$log" "start $SERVICE"
	assert_log_has "$log" "is-active --quiet $SERVICE"
}

run_install_no_start() {
	root="$WORK_DIR/install-no-start-root"
	log="$WORK_DIR/install-no-start-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/install-no-start-bin")
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/install.sh" --no-start >/dev/null
	assert_log_has "$log" "daemon-reload"
	assert_log_has "$log" "enable $SERVICE"
	assert_log_lacks_word "$log" "start"
}

run_install_no_enable() {
	root="$WORK_DIR/install-no-enable-root"
	log="$WORK_DIR/install-no-enable-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/install-no-enable-bin")
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/install.sh" --no-enable >/dev/null
	assert_log_lacks_word "$log" "enable"
	assert_log_has "$log" "start $SERVICE"
}

run_update_default() {
	root="$WORK_DIR/update-default-root"
	log="$WORK_DIR/update-default-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/update-default-bin")
	mkdir -p "$root/etc/xui-factor"
	printf '%s\n' '{"database_path":"/tmp/preserved-x-ui.db"}' > "$root/etc/xui-factor/config.json"
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/update.sh" >/dev/null
	assert_log_has "$log" "daemon-reload"
	assert_log_has "$log" "enable $SERVICE"
	assert_log_has "$log" "restart $SERVICE"
	assert_log_has "$log" "is-active --quiet $SERVICE"
	grep -Fq '/tmp/preserved-x-ui.db' "$root/etc/xui-factor/config.json" || die "update did not preserve config"
}

run_update_no_restart() {
	root="$WORK_DIR/update-no-restart-root"
	log="$WORK_DIR/update-no-restart-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/update-no-restart-bin")
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/update.sh" --no-restart >/dev/null
	assert_log_has "$log" "daemon-reload"
	assert_log_has "$log" "enable $SERVICE"
	assert_log_lacks_word "$log" "restart"
	assert_log_lacks_word "$log" "start"
}

run_update_no_start_alias() {
	root="$WORK_DIR/update-no-start-root"
	log="$WORK_DIR/update-no-start-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/update-no-start-bin")
	SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/update.sh" --no-start >/dev/null
	assert_log_lacks_word "$log" "restart"
	assert_log_lacks_word "$log" "start"
}

run_failed_install_start() {
	root="$WORK_DIR/install-fail-root"
	log="$WORK_DIR/install-fail-systemctl.log"
	err="$WORK_DIR/install-fail.err"
	mock=$(make_mock_systemctl "$WORK_DIR/install-fail-bin")
	if SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" SYSTEMCTL_FAIL_START=1 XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/install.sh" >"$WORK_DIR/install-fail.out" 2>"$err"; then
		die "install succeeded despite failed service start"
	fi
	grep -Fq "systemctl status $SERVICE --no-pager" "$err" || die "install failure missing status diagnostic"
	grep -Fq "journalctl -u $SERVICE -n 80 --no-pager" "$err" || die "install failure missing journal diagnostic"
}

run_failed_update_restart() {
	root="$WORK_DIR/update-fail-root"
	log="$WORK_DIR/update-fail-systemctl.log"
	err="$WORK_DIR/update-fail.err"
	mock=$(make_mock_systemctl "$WORK_DIR/update-fail-bin")
	if SYSTEMCTL_LOG="$log" SYSTEMCTL="$mock" SYSTEMCTL_FAIL_RESTART=1 XUI_FACTOR_TEST_SYSTEMD=1 DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/update.sh" >"$WORK_DIR/update-fail.out" 2>"$err"; then
		die "update succeeded despite failed service restart"
	fi
	grep -Fq "systemctl status $SERVICE --no-pager" "$err" || die "update failure missing status diagnostic"
	grep -Fq "journalctl -u $SERVICE -n 80 --no-pager" "$err" || die "update failure missing journal diagnostic"
}

run_destdir_skips_systemctl() {
	root="$WORK_DIR/destdir-root"
	log="$WORK_DIR/destdir-systemctl.log"
	mock=$(make_mock_systemctl "$WORK_DIR/destdir-bin")
	PATH="$WORK_DIR/destdir-bin:$PATH" SYSTEMCTL_LOG="$log" DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/install.sh" >/dev/null
	[ -f "$root/etc/systemd/system/$SERVICE" ] || die "DESTDIR service file was not installed"
	PATH="$WORK_DIR/destdir-bin:$PATH" SYSTEMCTL_LOG="$log" DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/update.sh" >/dev/null
	PATH="$WORK_DIR/destdir-bin:$PATH" SYSTEMCTL_LOG="$log" DESTDIR="$root" \
		sh "$PACKAGE_DIR/scripts/uninstall.sh" >/dev/null
	if [ -s "$log" ]; then
		die "DESTDIR scripts called systemctl without explicit mock mode"
	fi
}

mkdir -p "$WORK_DIR"
run_install_default
run_install_no_start
run_install_no_enable
run_update_default
run_update_no_restart
run_update_no_start_alias
run_failed_install_start
run_failed_update_restart
run_destdir_skips_systemctl
