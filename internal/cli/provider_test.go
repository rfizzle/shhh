package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/resolve"
)

// surfaceModels is a settings file that names a different model for every
// surface and one more for provider.model, so a resolution that read the
// wrong key cannot land on the right name by accident.
func surfaceModels() config.Config {
	var cfg config.Config
	cfg.Provider.Default = oneShotProviderName
	cfg.Provider.Model = "shared-model"
	cfg.Provider.CmdModel = "cmd-model"
	cfg.Provider.ChatModel = "chat-model"
	cfg.Provider.CodeModel = "code-model"
	return cfg
}

// Each command reads its own key ahead of provider.model, and a command whose
// key is unset — or a surface with no key at all — lands on provider.model as
// it did before the keys existed. The session kinds are the surface names, so
// a `--print` run and a served session (both a kind of chat or code) resolve
// through the same fill.
// See docs/capabilities/configuration.md#each-surface-can-have-a-model-of-its-own.
func TestFillConfigHalf_EachSurfaceReadsItsOwnKey(t *testing.T) {
	t.Setenv("SHHH_PROVIDER", "")
	t.Setenv("SHHH_MODEL", "")
	for _, c := range []struct {
		surface, want, from string
	}{
		{config.SurfaceCmd, "cmd-model", "provider.cmd_model"},
		{config.SurfaceChat, "chat-model", "provider.chat_model"},
		{config.SurfaceCode, "code-model", "provider.code_model"},
		{"print", "shared-model", "provider.model"},
	} {
		var flags resolve.Opts
		fillConfigHalf(&flags, surfaceModels(), c.surface)
		if got := resolve.Resolve(flags).Model; got != c.want {
			t.Errorf("%s runs on %q, want %q", c.surface, got, c.want)
		}
		if got := resolve.ModelFrom(flags); got != c.from {
			t.Errorf("%s says %q chose its model, want %q", c.surface, got, c.from)
		}
	}

	// An unconfigured file changes nothing: every surface is on
	// provider.model, and nothing is said to outrank it.
	var bare config.Config
	bare.Provider.Model = "shared-model"
	for _, s := range []string{config.SurfaceCmd, config.SurfaceChat, config.SurfaceCode} {
		var flags resolve.Opts
		fillConfigHalf(&flags, bare, s)
		if got := resolve.Resolve(flags).Model; got != "shared-model" {
			t.Errorf("%s moved off provider.model with no key of its own: %q", s, got)
		}
		if over := resolve.ModelOutranks(flags); over != "" {
			t.Errorf("%s reports %q outranking provider.model with no key of its own", s, over)
		}
	}
}

// The doctor's model row reports provider.model's resolution, so a surface
// key that is set is named beside it — the command reading it runs on a model
// the row would otherwise never mention — and the variable that overrules
// them all names every key it overrules.
func TestProbeModel_NamesTheSurfaceKeys(t *testing.T) {
	t.Setenv("SHHH_PROVIDER", "")
	t.Setenv("SHHH_MODEL", "")
	var cfg config.Config
	cfg.Provider.Model = "shared-model"
	cfg.Provider.CodeModel = "code-model"

	f := probeModel(context.Background(), cfg)
	if !strings.Contains(f.Detail, "provider.code_model = code-model ahead of provider.model") {
		t.Fatalf("the row should name the surface key that is set, got %q", f.Detail)
	}

	t.Setenv("SHHH_MODEL", "env-model")
	f = probeModel(context.Background(), cfg)
	if !strings.Contains(f.Detail, "SHHH_MODEL is set to env-model, overruling provider.model = shared-model, provider.code_model = code-model") {
		t.Fatalf("the variable should be said to overrule every key it beats, got %q", f.Detail)
	}

	// A file with no surface key reads as it always did.
	t.Setenv("SHHH_MODEL", "")
	cfg.Provider.CodeModel = ""
	if f = probeModel(context.Background(), cfg); strings.Contains(f.Detail, "_model") {
		t.Fatalf("no surface key is set, yet the row names one: %q", f.Detail)
	}
}

// A one-shot started under a file that names a model for every surface runs
// on the one-shot's own, and the record says so: the session row carries the
// model the request went to, not provider.model and not the coding agent's.
func TestOneShotRecordsTheModelItsOwnKeyNamed(t *testing.T) {
	data, _ := oneShotFixture(t, "ls -la")

	pipe, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	stdout := os.Stdout
	os.Stdout = pipe
	defer func() { os.Stdout = stdout }()

	cmd := newCmdCmd()
	cmd.SetArgs([]string{"--raw", "list files"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(withConfig(context.Background(), surfaceModels())); err != nil {
		t.Fatalf("the one-shot failed: %v", err)
	}

	sessions := oneShotSessions(t, data)
	if len(sessions) != 1 {
		t.Fatalf("the one-shot wrote %d session rows, want 1", len(sessions))
	}
	if got := sessions[0].Model; got != "cmd-model" {
		t.Fatalf("the one-shot's row says it ran on %q, want cmd-model", got)
	}
}
