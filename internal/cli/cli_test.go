package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PdYrust/XuiFactor/internal/buildinfo"
)

func TestRunHelp(t *testing.T) {
	var out bytes.Buffer
	app := New(&out, nil)

	if code := app.Run([]string{"--help"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	output := out.String()
	for _, want := range []string{
		"Usage:",
		"xui-factor enable --email user@example.com --factor 1.2",
		"xui-factor enable-all --factor 1.2",
		"xui-factor enable-all --factor 1.2 --limited-only",
		"xui-factor disable --email user@example.com",
		"xui-factor disable-all",
		"xui-factor backup",
		"xui-factor doctor",
		"xui-factor tick",
		"xui-factor run",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected help output to contain %q, got %q", want, output)
		}
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	app := New(&out, nil)
	app.Info = buildinfo.Info{
		Version:   "1.2.3",
		Commit:    "abc123",
		BuildTime: "2026-05-16T00:00:00Z",
	}

	if code := app.Run([]string{"version"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	output := out.String()
	for _, want := range []string{"XuiFactor 1.2.3", "commit: abc123", "built: 2026-05-16T00:00:00Z"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected version output to contain %q, got %q", want, output)
		}
	}
}

func TestHelpIncludesDaemonCommands(t *testing.T) {
	var out bytes.Buffer
	app := New(&out, nil)

	if code := app.Run([]string{"help"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	for _, want := range []string{"tick", "run"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected help output to contain %q, got %q", want, out.String())
		}
	}
}

func TestHelpListsMajorCommands(t *testing.T) {
	var out bytes.Buffer
	app := New(&out, nil)

	if code := app.Run([]string{"help"}); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	output := out.String()
	for _, want := range []string{
		"doctor",
		"backup",
		"status",
		"audit",
		"enable",
		"disable",
		"pause",
		"resume",
		"enable-all",
		"disable-all",
		"pause-all",
		"resume-all",
		"tick",
		"run",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected help output to contain %q, got %q", want, output)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	var err bytes.Buffer
	app := New(nil, &err)

	if code := app.Run([]string{"missing"}); code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", err.String())
	}
}

func TestStableReleaseMetadata(t *testing.T) {
	version := strings.TrimSpace(readRepoFile(t, "VERSION"))
	if version != "0.4.0" {
		t.Fatalf("expected VERSION to be 0.4.0, got %q", version)
	}

	readme := readRepoFile(t, "README.md")
	for _, want := range []string{
		"xui-factor_v0.4.0_linux_amd64.tar.gz",
		"license-AGPL--3.0-111111",
		"status-stable-111111",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("expected README to contain %q", want)
		}
	}
	if strings.Contains(readme, "xui-factor_v0.3.5-beta") {
		t.Fatalf("README still contains stale v0.3.5-beta package examples")
	}

	operations := readRepoFile(t, "docs/OPERATIONS.md")
	if !strings.Contains(operations, "xui-factor_v0.4.0_linux_amd64.tar.gz") {
		t.Fatalf("operations guide does not contain v0.4.0 package example")
	}
	if strings.Contains(operations, "xui-factor_v0.3.5-beta") {
		t.Fatalf("operations guide still contains stale v0.3.5-beta package examples")
	}

	releaseWorkflow := readRepoFile(t, ".github/workflows/release.yml")
	for _, want := range []string{"v0.4.0", "docs/releases/v0.4.0.md", "default: false"} {
		if !strings.Contains(releaseWorkflow, want) {
			t.Fatalf("expected release workflow to contain %q", want)
		}
	}
	if strings.Contains(releaseWorkflow, "v0.3.5-beta") {
		t.Fatalf("release workflow still contains stale v0.3.5-beta examples")
	}

	releaseNotes := readRepoFile(t, "docs/releases/v0.4.0.md")
	for _, want := range []string{
		"# XuiFactor v0.4.0",
		"xui-factor_v0.4.0_linux_amd64.tar.gz",
		"xui-factor_v0.4.0_linux_arm64.tar.gz",
	} {
		if !strings.Contains(releaseNotes, want) {
			t.Fatalf("expected v0.4.0 release notes to contain %q", want)
		}
	}
	for _, stale := range []string{"v0.4.0-beta", "xui-factor_v0.4.0-beta"} {
		if strings.Contains(releaseNotes, stale) {
			t.Fatalf("v0.4.0 release notes contain stale beta marker %q", stale)
		}
	}

	changelog := readRepoFile(t, "CHANGELOG.md")
	if !strings.Contains(changelog, "## v0.4.0 - Stable") {
		t.Fatalf("changelog is missing v0.4.0 stable entry")
	}
}

func TestIssueTemplates(t *testing.T) {
	expected := []string{
		".github/ISSUE_TEMPLATE/config.yml",
		".github/ISSUE_TEMPLATE/bug-report.yml",
		".github/ISSUE_TEMPLATE/feature-request.yml",
		".github/ISSUE_TEMPLATE/installation-update.yml",
		".github/ISSUE_TEMPLATE/configuration-server.yml",
		".github/ISSUE_TEMPLATE/documentation.yml",
		".github/ISSUE_TEMPLATE/support.yml",
	}
	for _, path := range expected {
		if strings.TrimSpace(readRepoFile(t, path)) == "" {
			t.Fatalf("issue template %s is empty", path)
		}
	}

	config := readRepoFile(t, ".github/ISSUE_TEMPLATE/config.yml")
	for _, want := range []string{"blank_issues_enabled: false", "https://t.me/PdYrust"} {
		if !strings.Contains(config, want) {
			t.Fatalf("expected issue template config to contain %q", want)
		}
	}

	bug := readRepoFile(t, ".github/ISSUE_TEMPLATE/bug-report.yml")
	for _, want := range []string{
		"XuiFactor version",
		"3x-ui version",
		"xui-factor doctor",
		"xui-factor status",
		"systemctl status xui-factor.service --no-pager",
		"full database files",
	} {
		if !strings.Contains(bug, want) {
			t.Fatalf("expected bug template to contain %q", want)
		}
	}

	install := readRepoFile(t, ".github/ISSUE_TEMPLATE/installation-update.yml")
	for _, want := range []string{
		"scripts/install.sh",
		"scripts/update.sh",
		"linux_amd64",
		"linux_arm64",
		"journalctl -u xui-factor.service -n 80 --no-pager",
	} {
		if !strings.Contains(install, want) {
			t.Fatalf("expected install template to contain %q", want)
		}
	}

	configIssue := readRepoFile(t, ".github/ISSUE_TEMPLATE/configuration-server.yml")
	for _, want := range []string{"Sanitized config details", "3x-ui version", "full database files"} {
		if !strings.Contains(configIssue, want) {
			t.Fatalf("expected config template to contain %q", want)
		}
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
