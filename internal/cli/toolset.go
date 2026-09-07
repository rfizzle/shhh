// What a surface offers the model, and what runs a call it makes. `shhh
// chat`, `shhh code` and a run behind --print register the same pieces on the
// same conditions and dispatch a call through them in the same order, so the
// registration is one function rather than one per surface: two copies of a
// hundred and fifty lines agree on the day they are written and on no day
// after it, and nothing fails when they stop agreeing — the surface with the
// older copy simply offers the model a tool the other one has, or runs a call
// through one wrap fewer.
package cli

import (
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// toolsetOpts is what a surface has to say about itself for the registration
// to differ at all.
type toolsetOpts struct {
	// scope is the working scope the gate's commands run under. It is built
	// before anything here because everything that runs a command takes it.
	scope *scope.Scope
	// browser says a published report may be opened in one. A run with
	// nobody in front of it never does: there is no guarantee of a desktop,
	// and the URL reaches the transcript either way.
	browser bool
	// gitWrites says this surface writes to git, and carries the two things
	// only a surface can answer: the record of what it changed, which is
	// what may be staged, and the checkout's trust answer, which is whether
	// the checkout's own commit hooks may run. Nil is the reading half
	// alone — which is what a conversation gets, having no editor, and what
	// a surface with no record of its own writes gets, having nothing to
	// stage from.
	gitWrites *structural.Writes
}

// toolset is what the registration opened. The pieces are held by name
// because a surface reaches for them again afterwards — the reducer for the
// evidence pane, the gate for its card and its close, the supervisor for what
// containment a start runs under — and only their wiring is common.
type toolset struct {
	evidence *evidence.Reducer
	gate     *quality.Runner
	proc     *process.Supervisor
	reports  *reports.Publisher

	// closers end what was opened, in the reverse order it was opened.
	closers []func()
}

// buildToolset opens what the session registered and puts each piece's
// definitions on it. kind is how the record names the surface asking, which
// is also the origin a published report is filed under.
//
// It stops short of the servers and of anything that needs the store, which
// is where both surfaces open one: an MCP toolset joins the definitions and
// the chain like any other, and cannot until the servers have answered.
func buildToolset(cmd *cobra.Command, session *chatSession, kind string, opts toolsetOpts) (*toolset, error) {
	cfg := ConfigFrom(cmd.Context())
	t := &toolset{}
	register := func(defs ...provider.Tool) {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), defs...)
	}

	// Tool-output reduction: bulky results are reduced before the model
	// reads them, with the originals retrievable through the evidence tool.
	// No store means no reduction and no evidence tool.
	if t.evidence = openEvidence(); t.evidence != nil {
		register(evidence.ToolDefinition())
	}
	// Guarded web tools: web_fetch, approval-gated as an external action,
	// and web_search where a search key is configured.
	if session.web != nil {
		register(session.web.Definitions()...)
		// A page goes into the store whole and the fetch cuts what the
		// conversation carries itself, naming the id that reads on from the
		// cut. Both halves are handed over here, and the tool is declared
		// bounded in the same breath, because a fetch that stores its own
		// page and a pipeline that stores it again disagree about which id
		// holds the page.
		if t.evidence != nil {
			session.web.UseEvidence(t.evidence.Keep, t.evidence.Scrub)
			t.evidence.Exempt(web.FetchToolName)
		}
	}
	// LSP integration: the definition/references tools when a language
	// server was detected, plus after-edit diagnostics. Servers start lazily
	// and shut down with the surface.
	if session.lsp != nil {
		defs := session.lsp.Definitions()
		register(defs...)
		// Every question a language server answers is bounded by the server
		// and by the tool that asked it — an outline is a file's
		// declarations, a references list is capped and says so. Reducing
		// one takes an answer whose whole value is that it is shorter than
		// the file and returns its two ends.
		t.evidence.Exempt(toolNames(defs)...)
		t.closers = append(t.closers, session.lsp.Close)
	}
	// Structural code tools: fd, ast-grep, sd, tokei, jaq — read-only
	// wrappers, each registered only when its binary is on PATH — and the
	// writing half of git where the surface asked for it, which is the one
	// tool here that is approved rather than auto-run.
	if session.structural != nil {
		// The writing half joins before the definitions are read, because
		// whether it is there is what the definitions say.
		if opts.gitWrites != nil {
			session.structural.AllowWrites(*opts.gitWrites)
		}
		defs := session.structural.Definitions()
		register(defs...)
		// The wrapped tools bound their own results the way this package's
		// readers do: fd caps its paths and names the argument that narrows
		// them, ast-grep and sd return a match set, the write half answers
		// in a line. git is the exception, and not a whole one — three of
		// its verbs bound themselves and two are the pipeline's to bound —
		// so it is declared per call rather than by name.
		for _, d := range defs {
			if d.Name == structural.GitToolName {
				continue
			}
			t.evidence.Exempt(d.Name)
		}
		t.evidence.ExemptWhen(structural.GitToolName, structural.GitCallBounded)
	}
	// The quality gate: the model runs the project's own checks by suite
	// name, and command text only ever comes from trusted config.
	if session.gate {
		t.gate = openQualityGate(cfg, t.evidence, opts.scope)
	}
	if t.gate != nil {
		register(quality.ToolDefinition())
	}
	// The long-running process supervisor: start goes through approval like
	// any command, and Close terminates every owned process tree however the
	// surface ends.
	if session.processes {
		t.proc = openProcessSupervisor(t.evidence)
	}
	if t.proc != nil {
		register(process.Definition())
		t.closers = append(t.closers, t.proc.Close)
	}
	// Report pages: an answer that is a page rather than a paragraph. The
	// tool writes only shhh's own report store and serves on loopback, so it
	// rides the auto-run path; no store means no report tool.
	if t.reports = openReportsPublisher(cfg, kind, opts.browser); t.reports != nil {
		register(reports.ToolDefinition())
		pub := t.reports
		t.closers = append(t.closers, func() { _ = pub.Close() })
	}
	// Secrets last, and before the first command can run: this is where the
	// vault's values reach the runner and the supervisor, and everything
	// after it works with the names and the scrub rather than the values.
	if err := session.openSecrets(cmd, t.evidence, t.proc); err != nil {
		return nil, err
	}
	return t, nil
}

// toolNames is the names of a set of definitions, which is what the
// reduction pipeline is declared in.
func toolNames(defs []provider.Tool) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

// close ends what the toolset opened.
func (t *toolset) close() {
	for i := len(t.closers) - 1; i >= 0; i-- {
		t.closers[i]()
	}
}

// executor is the chain one tool call is dispatched through, built inside
// out: the plain tools at the bottom, each registered piece's wrap over them,
// the reducer over all of it, and the vault outside everything.
//
// It is built after the servers have answered rather than beside the
// registration, because the MCP toolset is a wrap like the others and is not
// known until then. What a surface adds for itself — a sub-agent supervisor,
// the repeat detector — goes on outside this, at the call site that builds it.
func (t *toolset) executor(session chatSession) agent.ToolExecutor {
	exec := agent.ToolExecutor(tools.Execute)
	if session.web != nil {
		exec = session.web.WrapExecutor(web.Orchestrator, exec)
	}
	if session.lsp != nil {
		exec = session.lsp.WrapExecutor(exec)
	}
	if session.structural != nil {
		exec = session.structural.WrapExecutor(exec)
	}
	if session.mcpTools != nil {
		exec = session.mcpTools.WrapExecutor(exec)
	}
	if t.gate != nil {
		exec = t.gate.WrapExecutor(exec)
	}
	if t.proc != nil {
		exec = t.proc.WrapExecutor(exec)
	}
	if t.reports != nil {
		exec = t.reports.WrapExecutor(exec)
	}
	if session.skills.Len() > 0 {
		exec = session.skills.WrapExecutor(exec)
	}
	if session.notebook != nil {
		exec = session.notebook.WrapExecutor(notebook.Orchestrator, exec)
	}
	if t.evidence != nil {
		exec = t.evidence.WrapExecutor(exec)
	}
	// Secrets are scrubbed inside the reducer, before it stores anything, so
	// what the evidence store keeps and what the model reads are the same
	// text. This wrap stays outside it as the second door rather than the
	// mechanism: it is what catches a tool's error, a result the reducer
	// exempts from reduction, and the evidence tool's own paged output.
	return session.vault.WrapExecutor(exec)
}

// registerSkills puts the catalog's activation tool on the toolset and its
// names and descriptions in the prompt, and does neither when nothing
// loaded. It is not part of buildToolset because it runs after the servers
// have answered on both surfaces, so that the prompt states the tools in the
// order the model is offered them.
//
// Activation is a read, so no surface gates it — which is what lets a run
// with nobody to ask have skills at all.
func registerSkills(session *chatSession) {
	if session.skills.Len() == 0 {
		return
	}
	session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), skill.ToolDefinition(session.skills))
	session.promptExtra = prompt.CombineExtra(session.promptExtra, skill.PromptBlock(session.skills))
}
