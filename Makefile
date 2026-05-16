APP := xui-factor
CMD := ./cmd/xui-factor
BUILDINFO_PKG := github.com/PdYrust/XuiFactor/internal/buildinfo
VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GO ?= go
GOFILES := $(shell find cmd internal -type f -name '*.go' 2>/dev/null)
BUILD_DIR := build
DIST_DIR := dist
LDFLAGS := -s -w -X '$(BUILDINFO_PKG).Version=$(VERSION)' -X '$(BUILDINFO_PKG).Commit=$(COMMIT)' -X '$(BUILDINFO_PKG).BuildTime=$(BUILD_TIME)'

.PHONY: help fmt test build check package package-linux-amd64 package-linux-arm64 verify-packages verify-archives verify-installers clean _package

help:
	@printf '%s\n' \
		"Available targets:" \
		"  make fmt                  Format Go files in cmd and internal" \
		"  make test                 Run go test ./..." \
		"  make build                Build build/$(APP)" \
		"  make check                Run fmt, test, and build" \
		"  make package              Build linux amd64 and arm64 release archives" \
		"  make package-linux-amd64  Build linux amd64 release archive" \
		"  make package-linux-arm64  Build linux arm64 release archive" \
		"  make verify-packages      Verify release archives and checksums" \
		"  make verify-installers    Verify staged install, update, and uninstall" \
		"  make clean                Remove build and dist outputs"

fmt:
	@if [ -n "$(GOFILES)" ]; then gofmt -w $(GOFILES); fi

test:
	$(GO) test ./...

build:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BUILD_DIR)/$(APP)" "$(CMD)"

check: fmt test build

package: package-linux-amd64 package-linux-arm64

package-linux-amd64:
	$(MAKE) _package TARGET_OS=linux TARGET_ARCH=amd64

package-linux-arm64:
	$(MAKE) _package TARGET_OS=linux TARGET_ARCH=arm64

_package:
	@set -eu; \
	package_name="$(APP)_v$(VERSION)_$(TARGET_OS)_$(TARGET_ARCH)"; \
	package_dir="$(DIST_DIR)/$$package_name"; \
	archive="$(DIST_DIR)/$$package_name.tar.gz"; \
	rm -rf "$$package_dir" "$$archive" "$$archive.sha256"; \
	mkdir -p "$$package_dir/scripts" "$$package_dir/config" "$$package_dir/systemd" "$$package_dir/docs"; \
	CGO_ENABLED=0 GOOS="$(TARGET_OS)" GOARCH="$(TARGET_ARCH)" $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$$package_dir/$(APP)" "$(CMD)"; \
	cp README.md LICENSE VERSION "$$package_dir/"; \
	cp docs/OPERATIONS.md "$$package_dir/docs/"; \
	cp scripts/install.sh scripts/update.sh scripts/uninstall.sh scripts/installer-common.sh "$$package_dir/scripts/"; \
	cp config/config.json "$$package_dir/config/"; \
	cp systemd/xui-factor.service "$$package_dir/systemd/"; \
	chmod 0755 "$$package_dir/$(APP)" "$$package_dir/scripts/"*.sh; \
	printf '%s\n' \
		"$(APP)" \
		"PACKAGE-MANIFEST.txt" \
		"README.md" \
		"LICENSE" \
		"VERSION" \
		"docs/OPERATIONS.md" \
		"scripts/install.sh" \
		"scripts/update.sh" \
		"scripts/uninstall.sh" \
		"scripts/installer-common.sh" \
		"config/config.json" \
		"systemd/xui-factor.service" > "$$package_dir/PACKAGE-MANIFEST.txt"; \
	tar -C "$(DIST_DIR)" -czf "$$archive" "$$package_name"; \
	(cd "$(DIST_DIR)" && sha256sum "$$package_name.tar.gz" > "$$package_name.tar.gz.sha256"); \
	rm -rf "$$package_dir"

verify-packages: verify-archives verify-installers

verify-archives:
	@set -eu; \
	for arch in amd64 arm64; do \
		package_name="$(APP)_v$(VERSION)_linux_$$arch"; \
		archive="$(DIST_DIR)/$$package_name.tar.gz"; \
		checksum="$$archive.sha256"; \
		tmp_dir=$$(mktemp -d); \
		trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
		test -f "$$archive"; \
		test -f "$$checksum"; \
		(cd "$(DIST_DIR)" && sha256sum -c "$$package_name.tar.gz.sha256"); \
		if tar -tzf "$$archive" | grep -Eq '(^|/)(build|dist|\.git)(/|$$)|(~$$|\.tmp$$|\.temp$$|\.swp$$|\.swo$$|\.DS_Store$$)'; then \
			echo "archive contains generated or local-only files: $$archive" >&2; \
			exit 1; \
		fi; \
		for path in \
			"$(APP)" \
			"PACKAGE-MANIFEST.txt" \
			"README.md" \
			"LICENSE" \
			"VERSION" \
			"docs/OPERATIONS.md" \
			"scripts/install.sh" \
			"scripts/update.sh" \
			"scripts/uninstall.sh" \
			"scripts/installer-common.sh" \
			"config/config.json" \
			"systemd/xui-factor.service"; do \
			tar -tzf "$$archive" | grep -qx "$$package_name/$$path"; \
		done; \
		tar -xzf "$$archive" -C "$$tmp_dir"; \
		test -x "$$tmp_dir/$$package_name/$(APP)"; \
		test "$$(cat "$$tmp_dir/$$package_name/VERSION")" = "$(VERSION)"; \
		python3 -m json.tool "$$tmp_dir/$$package_name/config/config.json" >/dev/null; \
		grep -Fqx "ExecStart=/usr/local/bin/xui-factor --config /etc/xui-factor/config.json run" "$$tmp_dir/$$package_name/systemd/xui-factor.service"; \
		grep -Fq "xui-factor_v" "$$tmp_dir/$$package_name/README.md"; \
		grep -Fq "xui-factor_v" "$$tmp_dir/$$package_name/docs/OPERATIONS.md"; \
		old_dash=$$(printf 'xui-%s' 'multiplier'); \
		old_under=$$(printf 'xui_%s' 'multiplier'); \
		old_display=$$(printf 'Xui%s' 'Multiplier'); \
		if grep -RInE "$$old_dash|$$old_under|$$old_display" \
			"$$tmp_dir/$$package_name/README.md" \
			"$$tmp_dir/$$package_name/docs/OPERATIONS.md" \
			"$$tmp_dir/$$package_name/config/config.json" \
			"$$tmp_dir/$$package_name/systemd/xui-factor.service" \
			"$$tmp_dir/$$package_name/scripts"; then \
			exit 1; \
		fi; \
		expected_manifest="$$tmp_dir/expected-manifest.txt"; \
		printf '%s\n' \
			"$(APP)" \
			"PACKAGE-MANIFEST.txt" \
			"README.md" \
			"LICENSE" \
			"VERSION" \
			"docs/OPERATIONS.md" \
			"scripts/install.sh" \
			"scripts/update.sh" \
			"scripts/uninstall.sh" \
			"scripts/installer-common.sh" \
			"config/config.json" \
			"systemd/xui-factor.service" > "$$expected_manifest"; \
		diff -u "$$expected_manifest" "$$tmp_dir/$$package_name/PACKAGE-MANIFEST.txt"; \
		rm -rf "$$tmp_dir"; \
		trap - EXIT HUP INT TERM; \
	done

verify-installers:
	@set -eu; \
	host_arch=$$(uname -m); \
	case "$$host_arch" in \
		x86_64|amd64) host_arch=amd64 ;; \
		aarch64|arm64) host_arch=arm64 ;; \
		*) host_arch=unknown ;; \
	esac; \
	for arch in amd64 arm64; do \
		package_name="$(APP)_v$(VERSION)_linux_$$arch"; \
		archive="$(DIST_DIR)/$$package_name.tar.gz"; \
		tmp_dir=$$(mktemp -d); \
		trap 'rm -rf "$$tmp_dir"' EXIT HUP INT TERM; \
		tar -xzf "$$archive" -C "$$tmp_dir"; \
		pkg="$$tmp_dir/$$package_name"; \
		root="$$tmp_dir/root"; \
		DESTDIR="$$root" sh "$$pkg/scripts/install.sh" >/dev/null; \
		test -x "$$root/usr/local/bin/$(APP)"; \
		test -f "$$root/usr/local/share/$(APP)/README.md"; \
		test -f "$$root/usr/local/share/$(APP)/LICENSE"; \
		test -f "$$root/usr/local/share/$(APP)/VERSION"; \
		test -f "$$root/usr/local/share/$(APP)/docs/OPERATIONS.md"; \
		test -x "$$root/usr/local/share/$(APP)/scripts/install.sh"; \
		test -x "$$root/usr/local/share/$(APP)/scripts/update.sh"; \
		test -x "$$root/usr/local/share/$(APP)/scripts/uninstall.sh"; \
		test -x "$$root/usr/local/share/$(APP)/scripts/installer-common.sh"; \
		test -f "$$root/etc/xui-factor/config.json"; \
		test -f "$$root/etc/systemd/system/xui-factor.service"; \
		grep -Fqx "ExecStart=/usr/local/bin/xui-factor --config /etc/xui-factor/config.json run" "$$root/etc/systemd/system/xui-factor.service"; \
		if [ "$$arch" = "$$host_arch" ]; then \
			"$$root/usr/local/bin/$(APP)" --version >/dev/null; \
			"$$root/usr/local/bin/$(APP)" --help >/dev/null; \
		fi; \
		printf '%s\n' '{"database_path":"/tmp/preserved-x-ui.db"}' > "$$root/etc/xui-factor/config.json"; \
		printf '%s\n' old > "$$root/usr/local/share/$(APP)/VERSION"; \
		DESTDIR="$$root" sh "$$pkg/scripts/update.sh" >/dev/null; \
		grep -Fq '/tmp/preserved-x-ui.db' "$$root/etc/xui-factor/config.json"; \
		test "$$(cat "$$root/usr/local/share/$(APP)/VERSION")" = "$(VERSION)"; \
		mkdir -p "$$root/etc/x-ui"; \
		printf '%s\n' keep > "$$root/etc/x-ui/x-ui.db"; \
		DESTDIR="$$root" sh "$$pkg/scripts/uninstall.sh" >/dev/null; \
		test ! -e "$$root/usr/local/bin/$(APP)"; \
		test ! -e "$$root/etc/systemd/system/xui-factor.service"; \
		test ! -e "$$root/usr/local/share/$(APP)"; \
		test -f "$$root/etc/xui-factor/config.json"; \
		test -f "$$root/etc/x-ui/x-ui.db"; \
		DESTDIR="$$root" sh "$$pkg/scripts/uninstall.sh" --purge >/dev/null; \
		test ! -e "$$root/etc/xui-factor"; \
		test ! -e "$$root/usr/local/share/$(APP)"; \
		test -f "$$root/etc/x-ui/x-ui.db"; \
		rm -rf "$$tmp_dir"; \
		trap - EXIT HUP INT TERM; \
	done

clean:
	rm -rf "$(BUILD_DIR)" "$(DIST_DIR)"
