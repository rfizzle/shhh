package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
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
	its, err := buildToolset(toolsetCmd(t), &interactive, "code", toolsetOpts{scope: sc, browser: true, resident: true})
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

// A surface that ends with its answer is not handed a link it cannot keep.
// The registration is the one place the lifetime is read, and what it guards
// is a run behind --print quoting a loopback address whose port closed with
// the process seconds later.
func TestBuildToolsetTellsTheModelWhichReportLineItWillGet(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}

	interactive := codeToolset()
	its, err := buildToolset(toolsetCmd(t), &interactive, "code", toolsetOpts{scope: sc, browser: true, resident: true})
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

	session, unattended := reportDescription(t, interactive.toolDefs), reportDescription(t, headless.toolDefs)
	if !strings.Contains(session, "first line is the page URL") {
		t.Errorf("a session is not offered its own report link: %q", session)
	}
	if strings.Contains(unattended, "first line is the page URL") {
		t.Errorf("an unattended run is still told to quote a URL: %q", unattended)
	}

	// And the result agrees with the description, which is the half a
	// description alone cannot promise.
	args, _ := json.Marshal(reports.Document{Title: "t", Blocks: []reports.Block{{Type: reports.BlockProse, Text: "x"}}})
	out, err := hts.reports.ExecuteTool(args)
	if err != nil {
		t.Fatalf("headless report: %v", err)
	}
	if first, _, _ := strings.Cut(out, "\n"); !strings.HasPrefix(first, "shhh reports open rp-") {
		t.Errorf("an unattended run's report answers with %q, want the command that serves it", first)
	}
}

// reportDescription is how the report tool was described to this surface's
// model, which is where the promise about the result lives.
func reportDescription(t *testing.T, defs []provider.Tool) string {
	t.Helper()
	for _, d := range defs {
		if d.Name == reports.ToolName {
			return d.Description
		}
	}
	t.Fatal("the report tool was not registered")
	return ""
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

// A session that has both a store and the web tools hands one to the other:
// the page goes into the store whole and the pipeline leaves the fetch's own
// result alone.
func TestAFetchIsWiredToTheSessionsEvidenceStore(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}
	session := codeToolset()
	ts, err := buildToolset(toolsetCmd(t), &session, "code", toolsetOpts{scope: sc})
	if err != nil {
		t.Fatalf("session registration: %v", err)
	}
	defer ts.close()
	if ts.evidence == nil {
		t.Fatal("this session was supposed to open a store")
	}

	plan, err := session.web.FetchPlan(json.RawMessage(`{"url":"https://example.com/doc"}`))
	if err != nil {
		t.Fatalf("FetchPlan: %v", err)
	}
	if !strings.Contains(plan.Receives, "evidence store") {
		t.Errorf("the fetch was not given the store: %q", plan.Receives)
	}

	page := strings.Repeat("a fetched page, long enough to be worth reducing.\n", 500)
	if got := ts.evidence.Process(web.FetchToolName, page); got != page {
		t.Errorf("a fetch result was reduced a second time: %d of %d bytes", len(got), len(page))
	}
}

// A child fetches through the session's own web toolset, and that object is
// where the per-host pacing lives: three researchers reading one
// documentation site are paced as one session because there is one fetcher,
// not one per child. The call is proved to land there by the refusal it
// comes back with — the fetcher's own policy, which the built-in dispatcher
// knows nothing about.
func TestChildFetchesThroughTheSessionsOwnToolset(t *testing.T) {
	session := codeToolset()
	def := config.AgentDefinition{Name: "reader", Permissions: []string{config.PermissionWeb}}
	gated := map[string]bool{}
	_, defs, exec := profileEnv(def, subagent.Spec{}, shell.Info{}, "", session.web, gated)

	var offered bool
	for _, name := range toolsetNames(defs) {
		if name == web.FetchToolName {
			offered = true
		}
	}
	if !offered {
		t.Fatalf("a child with the web permission was not offered %s", web.FetchToolName)
	}
	if !gated[web.FetchToolName] {
		t.Errorf("%s reached a child ungated", web.FetchToolName)
	}
	if _, err := exec(web.FetchToolName, json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data/"}`)); err == nil ||
		!strings.Contains(err.Error(), "metadata") {
		t.Fatalf("err = %v, want the session fetcher's own refusal", err)
	}
}

// What a tool bounds for itself, the pipeline leaves alone — and the list of
// which tools those are is only knowable where they are registered. A
// language server's outline and an fd search are bounded by the tool that
// answered; a head-and-tail cut through either returns the two ends of an
// answer whose whole value was that it was already shorter than the file.
func TestRegistrationDeclaresWhatBoundsItself(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}
	session := codeToolset()
	session.lsp = lsp.NewToolset(lsp.NewManager(t.TempDir(), nil, lsp.Options{}))
	session.structural = structural.NewToolset(".")
	ts, err := buildToolset(toolsetCmd(t), &session, "code", toolsetOpts{scope: sc})
	if err != nil {
		t.Fatalf("session registration: %v", err)
	}
	defer ts.close()
	if ts.evidence == nil {
		t.Fatal("this session was supposed to open a store")
	}

	big := strings.Repeat("internal/cli/toolset.go:42:1 func buildToolset\n", 500)
	for _, d := range session.lsp.Definitions() {
		if got := ts.evidence.Process(d.Name, big); got != big {
			t.Errorf("%s: an answer the server bounded was reduced again (%d of %d bytes)", d.Name, len(got), len(big))
		}
	}
	for _, d := range session.structural.Definitions() {
		if d.Name == structural.GitToolName {
			continue
		}
		if got := ts.evidence.Process(d.Name, big); got != big {
			t.Errorf("%s: a bounded result was reduced again (%d of %d bytes)", d.Name, len(got), len(big))
		}
	}

	// git is declared per call, because three of its verbs bound themselves
	// and two are deliberately the pipeline's to bound.
	if session.structural == nil || !session.structural.Has(structural.GitToolName) {
		t.Skip("not inside a git repository, so the git tool was not registered")
	}
	exec := ts.evidence.WrapExecutor(func(string, json.RawMessage) (string, error) { return big, nil })
	whole, err := exec(structural.GitToolName, json.RawMessage(`{"verb":"blame","paths":["internal/cli/toolset.go"],"start_line":200,"end_line":400}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if whole != big {
		t.Errorf("the blame window the model asked for was cut: %d of %d bytes", len(whole), len(big))
	}
	cut, err := exec(structural.GitToolName, json.RawMessage(`{"verb":"show","ref":"HEAD"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cut == big {
		t.Error("show has no bound of its own; the pipeline is it")
	}
}
