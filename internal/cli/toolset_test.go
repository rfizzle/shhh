package cli

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// toolsetCmd is a command carrying a config, which is all the registration
// asks of the one it is handed.
func toolsetCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(withConfig(context.Background(), config.Config{}))
	cmd.SetErr(&strings.Builder{})
	return cmd
}

// toolsetNames is what the model would be offered, in a form two of them can
// be compared in.
func toolsetNames(defs []provider.Tool) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

// codeToolset is `shhh code`'s registration as both surfaces build it: the
// full toolset, the web tools, the quality gate and the process supervisor.
func codeToolset() chatSession {
	return chatSession{
		kind:      "code",
		toolDefs:  tools.DefinitionsFull(),
		web:       web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil),
		gate:      true,
		processes: true,
	}
}

// One registration means one answer. A session and a run behind --print offer
// the model the same tools under the same conditions because there is only
// one place that decides, and the two cannot drift apart because there is
// nothing left to drift from.
func TestBuildToolsetRegistersTheSameNamesOnBothSurfaces(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}

	interactive := codeToolset()
	its, err := buildToolset(toolsetCmd(t), &interactive, "code", toolsetOpts{scope: sc, browser: true})
	if err != nil {
		t.Fatalf("session registration: %v", err)
	}
	defer its.close()

	headless := codeToolset()
	hts, err := buildToolset(toolsetCmd(t), &headless, "print", toolsetOpts{scope: sc})
	if err != nil {
		t.Fatalf("headless registration: %v", err)
	}
	defer hts.close()

	session, unattended := toolsetNames(interactive.toolDefs), toolsetNames(headless.toolDefs)
	if strings.Join(session, ",") != strings.Join(unattended, ",") {
		t.Fatalf("the two surfaces registered different tools\n session %v\nheadless %v", session, unattended)
	}
	// And the pieces behind those names are opened on the same conditions:
	// whether a call runs at all is what a wrap in the chain decides, so a
	// surface holding one fewer of them offers a tool it cannot dispatch.
	if (its.gate == nil) != (hts.gate == nil) ||
		(its.proc == nil) != (hts.proc == nil) ||
		(its.evidence == nil) != (hts.evidence == nil) ||
		(its.reports == nil) != (hts.reports == nil) {
		t.Error("the two surfaces opened a different set of pieces for the same session")
	}
	// Every mutating tool is gated on both, which is the condition the
	// approval policy is written against.
	for _, name := range unattended {
		if tools.IsMutating(name) && !headlessGate(name) {
			t.Errorf("%s writes and is not gated", name)
		}
	}
}

// What a surface did not register, it does not offer. The conditions are the
// point of the shared registration, so turning one off has to take exactly
// its own names away.
func TestBuildToolsetOffersOnlyWhatWasRegistered(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}

	// `shhh chat`'s shape: it changes nothing, so it registers neither the
	// gate nor the supervisor nor the web tools.
	bare := chatSession{kind: "chat", toolDefs: tools.Definitions()}
	bts, err := buildToolset(toolsetCmd(t), &bare, "chat", toolsetOpts{scope: sc})
	if err != nil {
		t.Fatalf("conversation registration: %v", err)
	}
	defer bts.close()

	have := map[string]bool{}
	for _, name := range toolsetNames(bare.toolDefs) {
		have[name] = true
	}
	for _, name := range []string{web.FetchToolName, quality.ToolName, process.ToolName} {
		if have[name] {
			t.Errorf("a conversation was offered %s, which it never registered", name)
		}
	}
	if bts.gate != nil || bts.proc != nil {
		t.Error("a conversation opened a gate or a supervisor it does not register")
	}
}

// The notebook is not a conversation's. A coding session's children are the
// case it was always for — a fan-out of four finding the same thing four
// times is the cost it removes — so both kinds of session open one and
// register the same two tools.
func TestEverySessionOpensANotebook(t *testing.T) {
	for _, tc := range []struct {
		what    string
		session chatSession
	}{
		{"a coding session", chatSession{kind: "code", toolDefs: tools.DefinitionsFull()}},
		{"a conversation", chatSession{kind: "chat", conversation: true, toolDefs: tools.Definitions()}},
	} {
		s := tc.session
		s.openNotebook(nil)
		if s.notebook == nil {
			t.Fatalf("%s opened no notebook", tc.what)
		}
		have := map[string]bool{}
		for _, name := range toolsetNames(s.toolDefs) {
			have[name] = true
		}
		if !have[notebook.WriteToolName] || !have[notebook.ReadToolName] {
			t.Errorf("%s was not offered the notebook: %v", tc.what, toolsetNames(s.toolDefs))
		}
	}
}

// The vault's rewrite is on the store before the first note can be written,
// because the copy a note leaves behind outlives the turn that wrote it.
func TestANotebookOpensWithTheSessionsScrub(t *testing.T) {
	v := secret.New()
	if err := v.Add("API_KEY", "hunter2"); err != nil {
		t.Fatalf("declare: %v", err)
	}
	s := chatSession{kind: "code", vault: v}
	s.openNotebook(nil)
	n, _, err := s.notebook.Write("researcher-1", "The key", "it is hunter2")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if strings.Contains(n.Body, "hunter2") {
		t.Errorf("a declared secret was written into the notebook: %q", n.Body)
	}
}
