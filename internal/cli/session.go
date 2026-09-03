// The session runner shared by `shhh chat` and `shhh code`: provider
// resolution, the tool chain, persistence, resume, and the program run.
// Each command builds its own chatSession and hands it here; what differs
// between them is what the session registers, and the runner wires only
// what was registered.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/logs"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/browse"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// chatSession parameterizes the shared chat TUI entry point: `shhh chat` and
// `shhh code` run the same Bubble Tea model and differ only in system prompt,
// registered toolset, and title.
type chatSession struct {
	title string
	// kind labels recorded observability sessions: "chat" or "code".
	kind         string
	buildPrompt  func(shell.Info, ...string) string
	toolDefs     []provider.Tool
	flags        *resolve.Opts
	continueLast bool
	resumePick   bool
	// resumeName is a chat already chosen — `shhh chats` runs the browser
	// before it builds a session, so a reader with no provider configured
	// can still tidy the store, and nobody pays a provider resolve to
	// browse.
	resumeName string
	// web is the guarded web toolset; nil leaves the web tools
	// unregistered (`shhh chat` today).
	web *web.Toolset
	// lsp is the language-server toolset: after-edit diagnostics plus
	// the definition/references tools; nil (no servers detected, or disabled)
	// is a clean no-op. `shhh code` only.
	lsp *lsp.Toolset
	// structural wraps external code tools (fd, ast-grep, sd, tokei,
	// jaq), each registered only when its binary is on PATH; nil leaves them
	// unregistered. `shhh code` only.
	structural *structural.Toolset
	// gate registers the quality-gate tool and /gate command;
	// `shhh code` only.
	gate bool
	// processes registers the long-running process supervisor: the
	// process tool (start approval-gated) and the /ps command; `shhh code`
	// only. Session end terminates every owned process tree.
	processes bool
	// agents registers the sub-agent orchestration tools and supervisor
	//; `shhh code` interactive sessions only.
	agents bool
	// memory enables durable memory: bounded recall into the system
	// prompt plus the confirm-gated remember tool; `shhh code` interactive
	// sessions only (headless runs have nobody to confirm a proposal).
	memory bool
	// skills is the catalog of Agent Skills the session discovered; nil
	// registers neither the tool nor the prompt section. Both `shhh chat`
	// and `shhh code`, headless included: activation is a read.
	skills *skill.Catalog
	// secretFlags are the --secret specs; vault is what they and
	// secrets.env resolved to, opened by openSecrets before anything that
	// runs a command. Both `shhh chat` and `shhh code`, headless included.
	secretFlags []string
	vault       *secret.Vault
	// promptExtra is appended to the system prompt after config and project
	// context (e.g. the recalled-memory block).
	promptExtra string
	// memoryOmitted is how many memories the recall budget left out of
	// promptExtra, carried here because recall runs before the chat model is
	// built and the rail is the only party that says so.
	memoryOmitted int
	// maxRounds overrides behavior.max_tool_rounds for this session, where 0
	// means no cap at all — the unattended `shhh code --max-rounds 0`, where
	// the round checkpoint has nobody to stop for. maxRoundsSet tells the two
	// zeroes apart exactly as printOpts does; `shhh chat` sets neither and
	// takes the config.
	maxRounds    int
	maxRoundsSet bool
	// addDirs are --add-dir directories: the working scope a session
	// starts with beyond the directory it was opened in, on top of
	// behavior.scope_dirs.
	addDirs []string
	// conversation marks `shhh chat`: nothing that writes is registered,
	// so the runner, containment, the changeset, git snapshots, the backlog
	// and the start screen's survey are not built, and the TUI draws none
	// of the surfaces that account for them. Sub-agents are offered, but
	// only the roles that read. See docs/capabilities/chat.md#chat-changes-nothing.
	conversation bool
	// notebook is the shared channel between a conversation's agents;
	// nil registers no notebook tools.
	notebook *notebook.Store
	// mcp connects the MCP servers the catalog names; mcpTools and
	// mcpCatalog are what came of it. A conversation takes only servers
	// marked read-only (docs/capabilities/mcp.md#what-a-conversation-may-reach).
	mcp        bool
	mcpTools   *mcp.Toolset
	mcpCatalog *mcp.Catalog
}

// openSecrets resolves the session's vault and hands its values to every
// path a command runs through, and its scrub to everything that writes a
// copy of what came back. It must run before the first command and before
// the prompt is built, and it is the one place the values are put anywhere;
// everything after it works with the vault's name list and scrub.
// See docs/capabilities/secrets.md#a-secret-is-an-environment-variable.
func (s *chatSession) openSecrets(cmd *cobra.Command, red *evidence.Reducer, procSup *process.Supervisor) error {
	v, err := loadSecrets(ConfigFrom(cmd.Context()), s.secretFlags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	s.vault = v
	runner.SetSessionEnv(v.Environ())
	// The executor chain scrubs what the model reads; these two write the
	// copies that stay on disk after the turn — the evidence store's full
	// original, and a process's spool on its way there — and a wrap around
	// either of them sees the text only once it is already written.
	red.SetScrub(v.Scrub)
	if procSup != nil {
		procSup.SetEnv(v.Environ())
		procSup.SetScrub(v.Scrub)
	}
	s.promptExtra = prompt.CombineExtra(s.promptExtra, secret.PromptBlock(v))
	return nil
}

// sessionEnv is the provider-and-prompt setup shared by the interactive chat
// TUI and headless print mode: resolved model, initial messages, and a stream
// closure over the session's provider.
// modelListTimeout bounds a query to the endpoint's own catalog — the
// /model picker's, and the window probe below it; a gateway that is slow or
// down should cost the user a beat, not the session.
const modelListTimeout = 10 * time.Second

// modelListerFor adapts a provider that can enumerate its endpoint into the
// chat model's lazy lister. Providers without the capability return nil, and
// the picker keeps the curated catalog.
func modelListerFor(p provider.Provider) func(context.Context) ([]string, error) {
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil
	}
	return func(ctx context.Context) ([]string, error) {
		ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
		defer cancel()
		return lister.ListModels(ctx)
	}
}

// endpointWindowsFor asks an endpoint that can report the context length it
// serves each model at, and hands the session a lookup over the answer.
// Providers without the capability return nil and the session reads the
// table.
//
// The query runs once, in the background, and the lookup answers "not known"
// until it lands: the session asks for the window on every frame, so it
// cannot be a question that waits on a network — and nothing goes wrong while
// the answer is missing, because the table and the family floor are behind it
// and the first trim is many turns away in any case. A failure is dropped for
// the same reason it is not logged: nobody asked for this.
func endpointWindowsFor(p provider.Provider) func(string) (int64, bool) {
	endpoint, ok := p.(provider.ModelWindower)
	if !ok {
		return nil
	}
	var (
		mu      sync.RWMutex
		windows map[string]int64
	)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
		defer cancel()
		got, err := endpoint.ModelWindows(ctx)
		if err != nil || len(got) == 0 {
			return
		}
		mu.Lock()
		windows = got
		mu.Unlock()
	}()
	return func(model string) (int64, bool) {
		mu.RLock()
		defer mu.RUnlock()
		w, ok := windows[strings.ToLower(model)]
		return w, ok
	}
}

type sessionEnv struct {
	cfg       config.Config
	prov      provider.Provider
	provName  string
	modelName string
	sysPrompt string
	// prompts are the wordings a [prompts] file replaced, read once here so
	// the session, its children and the stamp all see the same set
	// (prompts.go). Empty fields are the built-in wordings.
	prompts sessionPrompts
	// projectTokens is the estimated context cost of the project instruction
	// files injected into the system prompt, which /stats and the inspector
	// rail name as its own occupancy category.
	projectTokens int64
	// survey is the checkout as it stood when the session opened. It is
	// carried rather than taken again because it costs a tree walk and two
	// git invocations, and the model's prompt block and the start screen are
	// two readings of one answer.
	survey      project.Info
	messages    []provider.Message
	stream      agent.StreamFunc
	switchModel func(string)
	// effort is the reasoning level the session resolved to, and
	// switchReasoning is what ctrl+t and /reasoning change it with.
	// Like the model it is read by the stream closure from another
	// goroutine, so it lives under the same mutex.
	effort          provider.Effort
	switchReasoning func(provider.Effort)
	// reasoning reads the level that is live now, for the streams built once
	// at session start and used for the rest of it — a sub-agent's. Without
	// it a level set with ctrl+t would be true of the session and false of
	// every child it spawns.
	reasoning func() provider.Effort
	// replaceTools edits the toolset the next request carries: a profile
	// drafted mid-session changes what spawn_agent may name.
	replaceTools func(func([]provider.Tool) []provider.Tool)
	// replaceKey and switchProvider are what a provider failure's [k] and
	// [p] do: both rebuild the provider in place, and both leave the
	// session untouched when the rebuild fails.
	replaceKey     func(string) error
	switchProvider func(string) error
}

// userInstructionsPath is the user's own instructions file: instructions.md
// beside the config file, taken from the first config directory that has
// one. It is beside the config rather than inside a checkout because it is
// the user's own writing — what they want of every session, everywhere —
// so it is read wherever shhh runs and asks nothing of the project.
//
// Empty when there is none, which is the ordinary case: this file exists
// only if someone wrote it.
func userInstructionsPath() string {
	for _, p := range config.Paths() {
		candidate := filepath.Join(filepath.Dir(p), "instructions.md")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

func buildSessionEnv(cmd *cobra.Command, session chatSession, ledger *meter.Ledger) (*sessionEnv, error) {
	cfg := ConfigFrom(cmd.Context())

	flags := session.flags
	flags.ConfigProvider = cfg.Provider.Default
	flags.ConfigModel = cfg.Provider.Model
	flags.ConfigReasoning = cfg.Provider.Reasoning

	resolved := resolve.Resolve(*flags)

	p, req, err := resolveProvider(cmd.Context(), cfg, providerRequest{
		Provider: resolved.Provider,
		Model:    resolved.Model,
		APIKey:   flags.FlagAPIKey,
	})
	if err != nil {
		return nil, err
	}
	resolved.Provider, resolved.Model = req.Provider, req.Model

	// DetectExec, not Detect: this session's model is told the shell its own
	// commands will run through, which is the execution shell rather than the
	// user's (internal/shell).
	info := shell.DetectExec()
	// The instruction files the session's own directory would read: the
	// user's own, then the project's from its root down to here. Read once,
	// here, because the system prompt is built once — a session that re-read
	// them per turn would be paying for a file nobody edited.
	instructions := project.Instructions(info.Cwd, userInstructionsPath())
	// One survey per session, read by both the model and the start screen.
	// It shells out to git and walks the tree, so the two readers share the
	// answer rather than each asking.
	survey := project.Survey("")
	instructionBlock := project.InstructionBlock(instructions, prompt.InstructionBudget)
	promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, instructionBlock,
		project.PromptBlock(survey), session.promptExtra)
	sysPrompt := session.buildPrompt(info, promptExtra)

	// Before anything is built on them: a named wording that cannot be read
	// stops the session here rather than letting it run on the built-in one.
	prompts, err := loadPrompts(cfg.Prompts)
	if err != nil {
		return nil, err
	}

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
	}

	effort, err := provider.ParseEffort(resolved.Reasoning)
	if err != nil {
		return nil, err
	}

	compOpts := provider.CompletionOpts{
		Model:  resolved.Model,
		Tools:  session.toolDefs,
		Effort: effort,
	}

	// /model switches the model mid-session, and a provider failure's [k] and
	// [p] switch the key and the provider under it. All three are
	// read by the stream closure from a background goroutine, so one mutex
	// guards the model, the provider and the key it was built with.
	var sessionMu sync.Mutex
	currentModel := resolved.Model
	currentTools := session.toolDefs
	currentEffort := effort
	currentProvider := resolved.Provider
	currentKey := req.APIKey
	currentBaseURL := req.BaseURL

	// rebuild resolves the provider again with whatever the session has
	// changed. It replaces nothing until the new provider is built: a key
	// that cannot be resolved leaves the session exactly as it was.
	//
	// What it swaps is the stream — the turn's own requests. The permission
	// classifier, the observability recorder, the /model lister and the
	// endpoint's context windows were wired to the provider this session
	// opened on and keep it; a classifier that fails on a dead key falls
	// back to asking, which is the right answer anyway, and a window read
	// off an endpoint nobody is talking to any more is a model name the new
	// provider does not use.
	rebuild := func(name, key string) error {
		sessionMu.Lock()
		baseURL, model := currentBaseURL, currentModel
		sessionMu.Unlock()
		if name != resolved.Provider {
			// A base URL belongs to the endpoint it was chosen for; carrying
			// one across a provider switch points the new dialect at the old
			// gateway.
			baseURL = ""
		}
		next, rErr := provider.Resolve(name, provider.ResolveOpts{
			APIKey:        key,
			Model:         model,
			BaseURL:       baseURL,
			ConfigAPIKey:  cfg.ProviderAPIKey(),
			ConfigBaseURL: cfg.ProviderBaseURL(),
			ConfigName:    cfg.ProviderDisplayName(),
			CacheTTL:      cfg.ProviderCacheTTL(),
		})
		if rErr != nil {
			return rErr
		}
		sessionMu.Lock()
		p, currentProvider, currentKey, currentBaseURL = next, name, key, baseURL
		if name != resolved.Provider {
			if model := provider.Defaults(name).Model; model != "" {
				currentModel = model
			}
		}
		sessionMu.Unlock()
		return nil
	}

	stream := func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ctx, cancel := context.WithCancel(cmd.Context())
		opts := compOpts
		// The caller's, not the session's: a turn wants the tools open and a
		// compaction wants prose, and only the caller knows which it is.
		opts.ToolChoice = choice
		sessionMu.Lock()
		opts.Model = currentModel
		opts.Tools = currentTools
		opts.Effort = currentEffort
		active := p
		sessionMu.Unlock()
		// The last door before the provider: the agent scrubs the
		// conversation it keeps, and this scrubs the request it sends, so
		// a message that reached the stream some other way is caught here.
		msgs = session.vault.ScrubMessages(msgs)
		// The gate is re-applied per request rather than once at startup,
		// because [k] and [p] can swap the provider underneath the session
		// and a gate wrapped around the old one would stop billing.
		ev, sErr := ledger.For(active, meter.SourceAgent).StreamCompletion(ctx, msgs, opts)
		if sErr != nil {
			cancel()
			return nil, nil, sErr
		}
		return ev, cancel, nil
	}

	return &sessionEnv{
		cfg:           cfg,
		prov:          p,
		provName:      resolved.Provider,
		modelName:     resolved.Model,
		sysPrompt:     sysPrompt,
		prompts:       prompts,
		survey:        survey,
		projectTokens: agent.EstimateTokens(instructionBlock),
		messages:      messages,
		stream:        stream,
		switchModel: func(name string) {
			sessionMu.Lock()
			currentModel = name
			sessionMu.Unlock()
		},
		effort: effort,
		switchReasoning: func(e provider.Effort) {
			sessionMu.Lock()
			currentEffort = e
			sessionMu.Unlock()
		},
		reasoning: func() provider.Effort {
			sessionMu.Lock()
			defer sessionMu.Unlock()
			return currentEffort
		},
		replaceTools: func(edit func([]provider.Tool) []provider.Tool) {
			sessionMu.Lock()
			currentTools = edit(currentTools)
			sessionMu.Unlock()
		},
		replaceKey: func(key string) error {
			sessionMu.Lock()
			name := currentProvider
			sessionMu.Unlock()
			return rebuild(name, key)
		},
		switchProvider: func(name string) error {
			sessionMu.Lock()
			key := currentKey
			sessionMu.Unlock()
			// A key resolved for one provider is not a key for another, so
			// the switch resolves the new provider's own credentials rather
			// than carrying the old one's across.
			if name != currentProvider {
				key = ""
			}
			return rebuild(name, key)
		},
	}, nil
}

func runChatSession(cmd *cobra.Command, args []string, session chatSession) error {
	// The working scope: the directory the session was opened in plus
	// whatever config and --add-dir put beside it. Containment writes to it,
	// the approval cards ask before anything leaves it, and /add-dir grows it
	// mid-session. It is built first because everything that runs a command
	// — the gate, sub-agents, the session's own runner — takes it.
	sc, err := sessionScope(ConfigFrom(cmd.Context()), session.addDirs)
	if err != nil {
		return err
	}

	// Tool-output reduction: bulky tool results are reduced before
	// the model sees them, with the originals retrievable via the evidence
	// tool. No store means no reduction and no evidence tool.
	red := openEvidence()
	if red != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), evidence.ToolDefinition())
	}
	// Guarded web tools: web_fetch (approval-gated as an external
	// action) and, when a search key is configured, web_search.
	if session.web != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.web.Definitions()...)
	}
	// LSP integration: definition/references tools when a language
	// server was detected; servers start lazily and shut down with the session.
	if session.lsp != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.lsp.Definitions()...)
		defer session.lsp.Close()
	}
	// Structural code tools: fd, ast-grep, sd, tokei, jaq — read-only
	// wrappers, each registered only when its binary is on PATH.
	if session.structural != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.structural.Definitions()...)
	}
	// Quality gate: the model can run the project's own checks by
	// suite name; command text only ever comes from trusted config.
	var gate *quality.Runner
	if session.gate {
		gate = openQualityGate(ConfigFrom(cmd.Context()), red, sc)
	}
	if gate != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), quality.ToolDefinition())
	}
	// Sub-agent orchestration: spawn_agent (approval-gated) and
	// agent_report join the toolset; the supervisor itself is built once the
	// provider is resolved.
	// The roles it can spawn are the built-in two plus whatever profiles the
	// user wrote to the agents directory; a profile that does not load is a
	// startup error naming the file, not a role that quietly went missing.
	var agents *agentProfiles
	if session.agents {
		agents, err = loadAgentProfiles(!session.conversation)
		if err != nil {
			return err
		}
		if session.conversation {
			// A conversation offers the roles that read; a profile that
			// could write is left out rather than offered and refused
			// (docs/capabilities/chat.md#colleagues-not-workers).
			agents = agents.readers()
		}
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), subagent.Definitions(agents.profiles)...)
	}
	// Long-running process supervisor: the process tool (start goes
	// through the approval queue like any command) plus /ps; Close terminates
	// every owned process tree when the session ends, however it ends.
	var procSup *process.Supervisor
	if session.processes {
		procSup = openProcessSupervisor(red)
	}
	if procSup != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), process.Definition())
		defer procSup.Close()
	}
	// Report pages: the model can publish an answer that is a page rather
	// than a paragraph as a local graphical view. The tool writes only shhh's
	// own report store and serves on loopback, so it rides the auto-run path
	// like evidence and the quality gate; no store means no report tool.
	pub := openReportsPublisher(ConfigFrom(cmd.Context()), session.kind, true)
	if pub != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), reports.ToolDefinition())
		defer pub.Close()
	}
	if err := session.openSecrets(cmd, red, procSup); err != nil {
		return err
	}

	db, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: chat persistence unavailable: %v\n", err)
	}
	if db != nil {
		defer db.Close()
	}

	// MCP servers: every definition the catalog holds is connected at
	// once, and the tools of the ones that answered join the toolset. A
	// server that did not answer is a line before the session starts and
	// a row in /mcp, never a reason not to start
	// (docs/capabilities/mcp.md#a-server-that-did-not-answer-is-a-row).
	if session.mcp {
		defer session.attachMCP(cmd.Context(), db, session.conversation)()
	}

	// Durable memory: recalled entries join the system prompt under a
	// hard entry/token budget — cited by id, zero model calls — and the
	// remember tool lets the model propose new ones, each confirmed by the
	// user before it persists.
	var mem *memory.Store
	if session.memory && db != nil && !ConfigFrom(cmd.Context()).Behavior.MemoryDisabled {
		mem = openMemoryStore(db)
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), memory.ToolDefinition())
		memCfg := ConfigFrom(cmd.Context())
		if entries, omitted, recallErr := mem.Recall(memCfg.EffectiveMemoryMaxEntries(), int64(memCfg.EffectiveMemoryMaxTokens())); recallErr == nil {
			session.promptExtra = prompt.CombineExtra(session.promptExtra, memory.PromptBlock(entries))
			session.memoryOmitted = omitted
		}
	}

	// The shared notebook: every agent in a conversation reads and
	// writes it. It persists under the session slot when storage is open,
	// and lives in memory for the session otherwise.
	if session.conversation {
		var backend notebook.Backend
		if db != nil {
			backend = db
		}
		session.notebook = notebook.New(backend)
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), notebook.Definitions()...)
	}

	// Skills: the catalog's names and descriptions join the prompt and
	// the activation tool joins the toolset, only when something loaded.
	if session.skills.Len() > 0 {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), skill.ToolDefinition(session.skills))
		session.promptExtra = prompt.CombineExtra(session.promptExtra, skill.PromptBlock(session.skills))
	}

	// The model is told where the work is, so an out-of-scope path is
	// a question it asks rather than a call the user refuses.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, scopePromptBlock(sc))

	// …and what it has to work with. Every optional tool above is
	// registered on a condition — a language server was found, a binary is on
	// PATH, a key is configured — so this is the last point where the whole
	// toolset is known, and it has to be said after the last one joins.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, prompt.Toolbox(session.toolDefs))

	// The spend ledger is opened before the session's provider, because the
	// provider is handed out through it: every request shhh makes is billed
	// at the gate rather than by the feature that made it.
	// See docs/architecture.md#spend-is-counted-at-the-provider.
	prices := loadPricing()
	ledger := meter.New(prices)

	env, err := buildSessionEnv(cmd, session, ledger)
	if err != nil {
		return err
	}
	cfg := env.cfg

	// Permission mode: starting mode and Shift+Tab cycle come from
	// config; the default is manual (everything prompts).
	mode := agent.ModeManual
	if s := cfg.Behavior.DefaultMode; s != "" {
		if mode, err = agent.ParseMode(s); err != nil {
			return fmt.Errorf("config behavior.default_mode: %w", err)
		}
	}
	cycle, err := agent.ParseCycle(cfg.Behavior.ModeCycle)
	if err != nil {
		return fmt.Errorf("config behavior.mode_cycle: %w", err)
	}

	// Auto mode's permission classifier reuses the session provider;
	// behavior.classifier_model overrides the model, and unset means the
	// provider's small model (summarizer.go).
	classifierModel := modelOr(cfg.Behavior.ClassifierModel, auxiliaryModel(env.provName, env.modelName))
	classifier := agent.NewClassifier(ledger.For(env.prov, meter.SourceClassifier), agent.ClassifierConfig{
		Model:     classifierModel,
		Timeout:   time.Duration(cfg.Behavior.ClassifierTimeoutSeconds) * time.Second,
		MaxTokens: cfg.Behavior.ClassifierMaxTokens,
		Retries:   cfg.Behavior.ClassifierRetries,
		Prompt:    env.prompts.classifier,
	})

	// The session summary resolves its model the same way: summary.model
	// overrides, and empty takes the provider's small model. It is still the
	// setting in that section worth changing, because a provider that names
	// no small model leaves the readings on the session's own.
	summarizer := newSummarizer(cfg, env, ledger, !cfg.Summary.Disabled)
	summaryModel := modelOr(cfg.Summary.Model, auxiliaryModel(env.provName, env.modelName))
	// Session titles ask the same model. Off unless a summary model is
	// configured or the config says so outright; a name the user gives
	// wins either way.
	titler := agent.NewTitler(ledger.For(env.prov, meter.SourceSummary), agent.TitleConfig{
		Model:    summaryModel,
		Timeout:  time.Duration(cfg.Summary.TimeoutSeconds) * time.Second,
		Disabled: !cfg.TitlesEnabled(),
	})

	// Process containment: assistant commands run wrapped when a
	// mechanism is available; the confirm prompt shows the state either way.
	var containment chat.Containment
	if !session.conversation {
		containment, err = buildContainment(cfg, sc, procSup)
		if err != nil {
			return err
		}
	}

	baseExecutor := agent.ToolExecutor(tools.Execute)
	if session.web != nil {
		baseExecutor = session.web.WrapExecutor(tools.Execute)
	}
	if session.lsp != nil {
		baseExecutor = session.lsp.WrapExecutor(baseExecutor)
	}
	if session.structural != nil {
		baseExecutor = session.structural.WrapExecutor(baseExecutor)
	}
	if session.mcpTools != nil {
		baseExecutor = session.mcpTools.WrapExecutor(baseExecutor)
	}
	if gate != nil {
		baseExecutor = gate.WrapExecutor(baseExecutor)
	}
	if procSup != nil {
		baseExecutor = procSup.WrapExecutor(baseExecutor)
	}
	if pub != nil {
		baseExecutor = pub.WrapExecutor(baseExecutor)
	}
	if session.skills.Len() > 0 {
		baseExecutor = session.skills.WrapExecutor(baseExecutor)
	}
	if session.notebook != nil {
		baseExecutor = session.notebook.WrapExecutor("assistant", baseExecutor)
	}
	executor := baseExecutor
	if red != nil {
		executor = red.WrapExecutor(baseExecutor)
	}
	// Secrets are scrubbed inside the reducer, before it stores anything, so
	// what the evidence store keeps and what the model reads are the same
	// text. This wrap stays outside it as the second door rather than the
	// mechanism: it is what catches a tool's error, a result the reducer
	// exempts from reduction, and the evidence tool's own paged output.
	executor = session.vault.WrapExecutor(executor)

	// Session observability: content-free events (usage, tool calls,
	// mode decisions) are recorded to storage; failure just disables recording.
	recorder := startObserveRecorder(db, session.kind, env.prov.Name(), env.modelName, prices)
	defer recorder.end()
	// The starting mode is stamped here, as a setting, rather than left to
	// the mode-change signal: that signal fires only on a change, so a
	// session that ran start to finish in the configured default would
	// record no mode at all, and absence is also what a session that
	// recorded nothing looks like.
	recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(), sessionSettings(cfg, runSettings{
		mode:       mode.String(),
		effort:     env.effort,
		rounds:     roundCapFor(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)),
		sandbox:    containment.Profile,
		model:      auxiliaryModel(env.provName, env.modelName),
		summary:    !cfg.Summary.Disabled,
		classifier: true,
	}))
	// The gate's verdict is the record's one objective reading of whether
	// the work was right. It is wired here rather than where the runner is
	// built because the runner has no session to report to until one is
	// open.
	recordGateVerdicts(gate, recorder)

	// The changeset store is opened here rather than where the chat model
	// takes it below, because a writer child starts from the parent's
	// uncommitted work and the supervisor is built first. A conversation
	// never wires it up and leaves it empty.
	changes := changeset.New(changeset.DefaultMaxBytes)

	// Sub-agent supervisor: spawn_agent and agent_report short-circuit
	// on the executor chain; Close cancels the child tree and removes
	// leftover worktrees when the session ends.
	var sup *subagent.Supervisor
	if session.agents {
		sup = buildSupervisor(cmd.Context(), cfg, session, env, agents, red, recorder, db, prices, classifier, sc, ledger, changes)
		executor = sup.WrapExecutor(executor)
		defer sup.Close()
	}

	// Repeat detection goes on last, so it sees every tool the chain
	// can dispatch and the result the model will actually read.
	executor = agent.NewRepeatDetector().WrapExecutor(executor)

	// The directory the session's own paths belong to, read once: shhh never
	// chdirs, so a session that asked again would be re-answering a settled
	// question. An unreadable working directory leaves it empty, which is
	// the same relative resolution the surface did before it was told.
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		cwd = ""
	}

	model := chat.New(env.messages, env.stream).
		WithTitle(session.title).
		WithWorkspace(cwd).
		WithObserver(recorder.observer()).
		WithToolDefinitions(toolDefTokens(session.toolDefs)).
		WithProjectContextTokens(env.projectTokens).
		WithToolExecutor(executor).
		WithDB(db).
		WithPricing(prices, env.modelName).
		WithLedger(ledger).
		WithSecrets(chat.Secrets{Manage: secretsManager(session.vault), Scrub: session.vault.ScrubMessage}).
		WithScope(sc).
		WithMaxToolRounds(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)).
		WithConfigWriter(configWriter()).
		WithMouse(cfg.MouseEnabled()).
		WithPasteThresholds(cfg.Appearance.PasteLines, cfg.Appearance.PasteColumns).
		WithRailWidth(components.RailWidthOrAuto(cfg.Appearance.RailWidth)).
		WithNotify(cfg.NotifyEnabled()).
		WithWindowTitle(cfg.WindowTitleEnabled()).
		WithDefaults(chat.Defaults{
			Model:      cfg.Provider.Model,
			AgentModel: cfg.Agents.Model,
			Outranked:  resolve.ModelOutranks(*session.flags),
		}).
		WithApprovalMode(mode, cycle).
		WithSteering(steering(cfg, env.prompts)).
		WithRetryLimit(cfg.Behavior.ProviderRetries).
		WithClassifier(classifier).
		WithSummarizer(summarizer).
		WithTitler(titler, cfg.TitlesEnabled()).
		WithModelSwitcher(env.switchModel).
		WithReasoning(env.effort, env.switchReasoning).
		WithReasoningDefault(cfg.Provider.Reasoning, resolve.ReasoningOutranks(*session.flags)).
		WithProvider(env.provName, env.replaceKey, env.switchProvider).
		WithModelOptions(provider.KnownModels(env.prov.Name())).
		WithModelLister(modelListerFor(env.prov)).
		WithEndpointWindows(endpointWindowsFor(env.prov))
	if session.conversation {
		model = model.WithConversation().WithNotebook(session.notebook)
	} else {
		// The coding agent's machinery for acting and accounting for it:
		// the command runners, containment, the changeset behind review
		// and undo, git snapshots behind rewind, and the start screen's
		// survey of the checkout. A conversation has no act to account for.
		model = model.
			WithRunner(scrubRunner(session.vault, runner.RunCapture)).
			WithTailRunner(scrubTailRunner(session.vault, runner.RunCaptureTail)).
			WithContainment(scrubContainment(session.vault, containment)).
			WithCommandAllowlist(cfg.Behavior.CommandAllowlist).
			WithCommandTimeout(cfg.CommandTimeout()).
			WithReadOnlyCommands(cfg.Behavior.ReadOnlyCommands, !cfg.ReadOnlyAutoEnabled()).
			WithGitSnapshots(gitSnapshot).
			WithChangeset(changes, changeset.NewTracker(".")).
			WithTreeCheck(treeCheck(cfg)).
			// First contact: the empty session's start screen, surveyed
			// once here rather than assembled per frame.
			WithStartScreen(buildStartInfo(env.survey, db, gate != nil)).
			// The one thing the screen offers that writes: scaffolding the
			// checkout's own context file, behind a card.
			WithScaffold(buildScaffold(db, cwd))
	}
	if red != nil {
		model = model.WithEvidence(chat.Evidence{Reduce: red.Process, Manage: evidenceManager(red)})
	}
	if session.lsp != nil {
		model = model.WithMutationHook(lspMutationHook(session.lsp))
	}
	if gate != nil {
		model = model.WithGate(chat.Gate{Manage: gateManager(gate), Run: gate.Run})
	}
	if procSup != nil {
		model = model.WithProcesses(chat.Processes{
			Manage:    processManager(procSup),
			Contained: procSup.Contained,
		})
	}
	if session.skills != nil {
		model = model.WithSkills(session.skills, skillsListing)
	}
	if cwd != "" && !session.conversation {
		root := todo.Root(cwd)
		model = model.WithTodos(chat.Todos{
			Root: root, Manage: todoManager(root), Detail: todoDetail,
			// The session's own model reads the session: extraction is a
			// judgement about the whole conversation, not a status line, and
			// the cheap summary model is the wrong price point for it.
			Extractor: todo.NewExtractor(ledger.For(env.prov, meter.SourceBacklog), todo.ExtractConfig{Model: env.modelName}),
			NoCommit:  !cfg.TodoCommitEnabled(),
		})
	}
	if mem != nil {
		model = model.WithMemory(chat.Memory{
			Manage:       memoryManager(mem),
			Save:         memorySaver(mem),
			ProjectScope: mem.Project(),
			EntryText:    memoryText(mem),
			Rewrite:      memoryRewriter(mem),
			Omitted:      session.memoryOmitted,
		})
	}
	// web_fetch and spawn_agent go through the approval queue as generic
	// external actions: manual and accept-edits prompt, auto defers to the
	// classifier.
	gatedPreviews := map[string]chat.GatedPreviewFunc{}
	if session.web != nil {
		webTools := session.web
		gatedPreviews[web.FetchToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			summary, err := webTools.FetchSummary(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			// The card's blast-radius block for an outbound request:
			// where it goes, what leaves with it, what comes back.
			plan, err := webTools.FetchPlan(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			return chat.GatedPreview{Action: "fetch", Summary: summary, Fields: []chat.GatedField{
				{Label: "domain", Value: plan.Host, Detail: "the request leaves this machine", Open: true},
				{Label: "sends", Value: plan.Sends, Detail: "no file contents, no credentials"},
				{Label: "receives", Value: plan.Receives, Detail: "it counts against the context window"},
			}}, nil
		}
	}
	if sup != nil {
		gatedPreviews[subagent.SpawnToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			summary, err := subagent.SpawnSummary(agents.profiles, args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			plan, err := subagent.SpawnPlan(agents.profiles, args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			// A child edits in its own worktree; nothing reaches the checkout
			// until its patch is approved on a card of its own.
			undo := "the child changes nothing on this checkout"
			if plan.Writer {
				undo = "its patch is a decision of its own before anything lands"
			}
			return chat.GatedPreview{Action: "spawn", Summary: summary, Fields: []chat.GatedField{
				{Label: "touches", Value: plan.Scope, Detail: "in its own worktree, not this checkout", Open: plan.Writer},
				{Label: "undo", Value: "reviewed", Detail: undo},
				{Label: "budget", Value: plan.Budget, Detail: "counted in the session totals"},
			}}, nil
		}
		model = model.WithSubagents(sup).WithPersonas(buildPersonas(session, env, agents, sup, ledger))
	}
	// A server call the user did not mark read-only goes through the
	// queue the same way: the card says where it goes and what leaves
	// with it (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
	if session.mcpTools != nil {
		mcpTools := session.mcpTools
		for _, name := range mcpTools.Gated() {
			name := name
			gatedPreviews[name] = func(args json.RawMessage) (chat.GatedPreview, error) {
				return mcpGatedPreview(mcpTools, name, args)
			}
		}
		model = model.WithMCP(chat.MCP{
			Has:      mcpTools.Has,
			ReadOnly: mcpTools.ReadOnly,
			Manage:   mcpManager(mcpTools, session.mcpCatalog, db),
			Sources:  mcpToolSources(mcpTools),
		})
	}
	if len(gatedPreviews) > 0 {
		model = model.WithGatedTools(gatedPreviews)
	}

	if session.continueLast || session.resumePick || session.resumeName != "" {
		if db == nil {
			return fmt.Errorf("chat persistence is unavailable, cannot resume")
		}
		// --continue is the newest slot, whatever it was called: every
		// session autosaves to a slot of its own, so "the last session" is
		// a query rather than a name.
		name := session.resumeName
		if session.resumePick {
			picked, err := pickSavedChat(db)
			if err != nil {
				return err
			}
			if picked == "" {
				return nil
			}
			name = picked
		} else if recent, ok, err := db.MostRecentChat(); err != nil {
			return err
		} else if ok {
			name = recent.Name
		}
		var (
			resumed []provider.Message
			loadErr error
		)
		if name == "" {
			loadErr = fmt.Errorf("no saved chats")
		} else {
			resumed, loadErr = db.LoadChat(name)
		}
		if loadErr != nil {
			if session.continueLast {
				_ = report.Fprintln(os.Stderr, report.Row{State: report.Skip,
					Subject: "no previous session", Detail: "starting fresh"})
			} else {
				return loadErr
			}
		} else {
			// Refresh the system prompt so shell/cwd context is current.
			if len(resumed) > 0 && resumed[0].Role == provider.RoleSystem {
				resumed[0].Content = env.sysPrompt
			}
			model = model.WithResumedMessages(name, resumed)
			// A conversation the slot says was saved mid-turn comes back
			// mid-turn. Without this it would open idle with an unanswered
			// round in front of it, which is the shape a person reads as
			// "it finished" — and the round it is owed would never be asked
			// for (docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held).
			if h, ok, err := db.ChatHold(name); err == nil && ok {
				model = model.WithHeldTurn(h.Rounds, h.Granted)
			}
		}
	}
	if r := update.CheckCached(version); r != nil {
		model = model.WithUpdateNotice("update: " + r.Latest)
	}
	if keymapNoticeDue() {
		model = model.WithKeysNotice(chat.KeysChangedNotice())
	}

	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}

	// Piped stdin becomes context for the first message; the TUI then
	// reads keys from the terminal directly.
	var programOpts []tea.ProgramOption
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !stdinIsTTY {
		maxChars := cfg.EffectiveContextMaxTokens() * 4
		content, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return err
		}
		if content != "" {
			if initialPrompt == "" {
				initialPrompt = "Take a look at this."
			}
			initialPrompt = stdin.FormatPromptWithContext(initialPrompt, content)
		}
		tty, err := os.Open("/dev/tty")
		if err != nil {
			return fmt.Errorf("chat needs a terminal for input: %w", err)
		}
		defer tty.Close()
		programOpts = append(programOpts, tea.WithInput(tty))
	}
	if initialPrompt != "" {
		model = model.WithInitialPrompt(initialPrompt)
	}

	// Ask the terminal to stop turning the wheel into arrow keys, which is what
	// alternate scroll does to a full-screen program on most terminals — and
	// what put hundreds of synthetic Up/Down presses into the draft
	// (chat/altscroll.go). The setting is saved and restored, so a terminal
	// that had it on keeps it after we exit.
	restoreScroll := chat.SuppressAlternateScroll(os.Stdout)
	defer restoreScroll()

	// Wheel floods are merged before they enter the update queue, so a key
	// pressed mid-fling never waits behind one frame per notch (chat/wheel.go).
	// The filter needs the program's own Send for its flush probe, which the
	// program cannot hand out before it exists — hence the two steps.
	wheel := chat.NewWheelFilter()
	programOpts = append(programOpts, tea.WithFilter(wheel.Filter))
	program := newProgram(model, programOpts...)
	wheel.SetSend(program.Send)
	final, err := program.Run()
	if err != nil {
		// os.Exit skips the deferred restore, so the terminal is put back
		// here rather than left reporting modified keys to the next program.
		restoreScroll()
		// And it skips the deferred close, which would leave the row saying
		// the session came out the way its last turn did. It came out as
		// this error.
		recorder.endWith(observe.SessionError)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// The alt screen took the whole session with it on the way out (
	// docs/interface/surfaces.md#outside-the-tui). The banner is what the
	// terminal keeps: the slot the conversation is in, what the sitting cost,
	// and how to reopen it. The resume command is this command — `shhh chat` and
	// `shhh code` read the same autosave slot but not the same toolset, so the
	// one that comes back is the one that was running.
	if m, ok := final.(chat.Model); ok {
		printExitBanner(m.ExitBanner("shhh " + session.kind + " --continue"))
	}
	return nil
}

// printExitBanner writes the exit banner on stderr, beside everything else
// this command says about itself, so a redirected stdout still carries only
// what the session produced. A session with nothing to resume renders empty
// and prints nothing at all.
func printExitBanner(b components.ExitBanner) {
	// stderr is what is being written to, so stderr is what is measured; a
	// redirected one has no width to give and takes the same 80 columns the
	// rest of the CLI falls back to.
	width, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil || width <= 0 {
		width = 80
	}
	if view := b.View(width); view != "" {
		fprintStyled(os.Stderr, view)
	}
}

// chatBrowseItems is the saved chats as the browser lists them: the name,
// its title and size beside it, and what deleting it would take along.
func chatBrowseItems(db *storage.DB, entries []storage.ChatListEntry) []browse.Item {
	items := make([]browse.Item, len(entries))
	for i, e := range entries {
		when := e.UpdatedAt.Local().Format("Jan 2 15:04")
		preview := fmt.Sprintf("%d turns, %s", e.Turns, when)
		detail := fmt.Sprintf("Name:     %s\nTurns:    %d\nUpdated:  %s",
			e.Name, e.Turns, e.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
		if e.Title != "" {
			preview = e.Title + " · " + preview
			detail = fmt.Sprintf("Name:     %s\nTitle:    %s\nTurns:    %d\nUpdated:  %s",
				e.Name, e.Title, e.Turns, e.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
		}
		items[i] = browse.Item{ID: e.Name, Title: e.Name, Preview: preview, Detail: detail}
		if n, err := db.CountChatBranches(e.Name); err == nil && n > 0 {
			items[i].Deleting = "and its " + branchCount(n)
		}
	}
	return items
}

// branchCount is n branches, in words.
func branchCount(n int) string {
	if n == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", n)
}

// treeCheck is the reading that tells a turn the tree moved under it, or nil
// when the config turned it off. The subtrahend is the front-end's: a
// session hands in its changeset, a headless run the paths its calls wrote.
func treeCheck(cfg config.Config) *agent.TreeCheck {
	if !cfg.TreeCheckEnabled() {
		return nil
	}
	return &agent.TreeCheck{
		Dir:       ".",
		IsCommand: func(name string) bool { return name == tools.ExecCommandName },
		Log:       func(msg string) { logs.Logger().Warn(msg) },
	}
}

// gitSnapshot captures the workspace's git state for rewind checkpoints
// , so /rewind can report what diverged since a checkpoint.
func gitSnapshot() chat.GitSnapshot {
	fp := quality.TakeFingerprint(".")
	return chat.GitSnapshot{Repo: fp.Repo, Head: fp.Head, StatusHash: fp.StatusHash, DirtyPaths: fp.DirtyPaths}
}

// toolDefTokens roughly estimates what each registered tool definition costs
// the context window, for the occupancy breakdown and the context surface's
// itemisation of it.
//
// Each definition is measured on its own rather than the whole set at once,
// which loses the punctuation between them — a few tokens across the toolset,
// against a per-tool answer the sum alone cannot give.
func toolDefTokens(defs []provider.Tool) []chat.ToolTokens {
	out := make([]chat.ToolTokens, 0, len(defs))
	for _, def := range defs {
		b, err := json.Marshal(def)
		if err != nil {
			continue
		}
		out = append(out, chat.ToolTokens{Name: def.Name, Tokens: agent.EstimateTokens(string(b))})
	}
	return out
}

// pickSavedChat shows the saved-chat picker and returns the chosen session
// name, or "" if the user backed out.
func pickSavedChat(db *storage.DB) (string, error) {
	entries, err := db.ListChats()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		_ = report.Fprintln(os.Stderr, report.Empty("nothing saved yet", "shhh chat"))
		return "", nil
	}

	model := browse.New(chatBrowseItems(db, entries), []browse.ActionDef{{Label: "Open", Shortcut: "o"}}).
		WithOps(browse.Ops{Delete: db.DeleteChat, Rename: db.RenameChat})
	p := newProgram(model)
	result, err := p.Run()
	if err != nil {
		return "", err
	}
	m := result.(browse.Model)
	if m.Result == nil {
		return "", nil
	}
	return m.Result.Item.ID, nil
}
