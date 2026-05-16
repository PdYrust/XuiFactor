package cli

import (
	"bytes"
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
