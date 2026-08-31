package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/profile"
)

// A profile that will not load is reported once. The startup loop in the root
// command prints these for every other command; here the row is the report,
// and one file naming one fault must not read as two.
func TestProviders_ABrokenProfileIsReportedOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	must(t, os.MkdirAll(filepath.Join(dir, "shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "shhh", "providers.toml"),
		[]byte("[[provider]]\nname = \"broken\"\n"), 0o644))

	done := captureStderr(t)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"providers"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shhh providers: %v", err)
	}
	combined := out.String() + done()
	if n := strings.Count(combined, "base_url is required"); n != 1 {
		t.Fatalf("the fault is stated %d times, want 1:\n%s", n, combined)
	}
	if strings.Contains(combined, "shhh: provider profile:") {
		t.Fatalf("the startup warning was not suppressed for the command that owns it:\n%s", combined)
	}
	if !strings.Contains(combined, "✗ ") {
		t.Fatalf("the failure has no row:\n%s", combined)
	}
}

// Every other command still hears about a broken profile at startup: a
// session resolving providers off a half-loaded set should say so.
func TestProviders_OtherCommandsStillWarnAtStartup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	must(t, os.MkdirAll(filepath.Join(dir, "shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "shhh", "providers.toml"),
		[]byte("[[provider]]\nname = \"broken\"\n"), 0o644))

	done := captureStderr(t)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"memory", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shhh memory list: %v", err)
	}
	if warned := done(); !strings.Contains(warned, "shhh: provider profile:") {
		t.Fatalf("a command that does not own the failure should still be warned: %q", warned)
	}
}

// captureStderr swaps os.Stderr for a pipe, because the startup warning is
// written to the process's own stderr rather than the command's. It returns
// the reader: calling it puts stderr back and answers what was written.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	read := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		read <- b.String()
	}()
	var text string
	var closed bool
	return func() string {
		if !closed {
			closed = true
			os.Stderr = saved
			_ = w.Close()
			text = <-read
			_ = r.Close()
		}
		return text
	}
}

// The three shapes `shhh providers` takes, at eighty columns.
func TestProvidersReport_Goldens(t *testing.T) {
	for _, v := range []string{"SHHH_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CORP_GW_KEY"} {
		t.Setenv(v, "")
	}
	names := []string{"anthropic", "corp-gw", "openai"}
	sources := []report.Row{
		{State: report.Pass, Subject: "~/.config/shhh/providers.toml", Detail: "1 profile"},
		{State: report.Skip, Subject: "~/.config/shhh/providers", Detail: "absent"},
	}
	populated := providersReport(names, sources, []profile.Profile{goldenProfile()}, nil, "")
	assertReportGolden(t, "providers", populated.Render(80))

	broken := providersReport([]string{"anthropic", "openai"}, append(sources[1:], report.Row{
		State: report.Fail, Subject: "~/.config/shhh/providers.toml", Outcome: "would not load",
		Consequence: "provider \"corp-gw\": base_url is required",
		Fix:         []string{"the other profiles still load; this file is skipped until it parses"},
	}), nil, nil, "write ~/.config/shhh/providers.toml to add one")
	assertReportGolden(t, "providers.broken", broken.Render(80))

	none := providersReport([]string{"anthropic", "openai"}, sources[1:], nil, nil,
		"write ~/.config/shhh/providers.toml to add one")
	assertReportGolden(t, "providers.none", none.Render(80))
}

// goldenProfile is one gateway with one endpoint, one declared model and one
// rewrite rule — the shapes a profile listing has to carry.
func goldenProfile() profile.Profile {
	return profile.Profile{
		Name:      "corp-gw",
		API:       "openai-chat",
		BaseURL:   "https://gw.example.com/v1",
		APIKeyEnv: "CORP_GW_KEY",
		Path:      "~/.config/shhh/providers.toml",
		Models: []profile.Model{{
			ID: "gpt-4o", ContextWindow: 128000,
			Cost: profile.Cost{Input: 2.50, Output: 10},
		}},
		Rewrite: []profile.Rule{{
			Direction: "request", Op: "set", Path: "model", Value: "gpt-4o",
			Note: "the gateway rejects a model id it does not host",
		}},
	}
}

// splitProfileError leads a row with the file the failure names, so a source
// row reads the same whichever half of the sentence it came from.
func TestSplitProfileError(t *testing.T) {
	path, detail := splitProfileError(errors.New("/etc/providers.toml: base_url is required"))
	if path != "/etc/providers.toml" || detail != "base_url is required" {
		t.Fatalf("split = %q / %q", path, detail)
	}
	if path, detail := splitProfileError(errors.New("unreadable")); path != "unreadable" || detail != "" {
		t.Fatalf("a sentence with no path stands alone: %q / %q", path, detail)
	}
}
