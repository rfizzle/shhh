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
	"github.com/rfizzle/shhh/internal/hook"
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
	// resumeName is a chat already chosen, by whoever could choose one:
	// `shhh chats` runs the browser before it builds a session, so a reader
	// with no provider configured can still tidy the store and nobody pays a
	// provider resolve to browse, and `--resume=<name>` names one outright,
	// which is the only way to say it to a run with no browser to draw.
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
	// requireSandbox is --require-sandbox: the flag half of sandbox.require,
	// which it can only turn on. A session that requires containment and has
	// none refuses the assistant's commands rather than running them bare.
	requireSandbox bool
	// secretFlags are the --secret specs; vault is what they and
	// secrets.env resolved to, opened by openSecrets before anything that
	// runs a command. Both `shhh chat` and `shhh code`, headless included.
	secretFlags []string
	vault       *secret.Vault
	// promptExtra is appended to the system prompt after config and project
	// context (e.g. the recalled-memory block).
	promptExtra string
	// sibling asks whether another session already has this checkout open.
	// It is pointed at the store as soon as one is open, because the system
	// prompt, the start screen and the tree reading all state the answer and
	// must state the same one.
	sibling sessionSibling
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
	// notebook is the shared channel between the session's agents, opened
	// by openNotebook; nil registers no notebook tools.
	notebook *notebook.Store
	// sources is the session's record of what it read, opened by
	// openSourceLedger; nil is a session with no web tools.
	sources *web.Ledger
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
	cfg := ConfigFrom(cmd.Context())
	v, err := loadSecrets(cfg, s.secretFlags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	s.vault = v
	// The mask goes on before the pairs do, so a declared secret whose name
	// the mask would have dropped is put back by the pairs: the user asked
	// for that one to be reachable, and the mask is for the ones nobody
	// asked for.
	mask := maskForSession(cfg)
	v.SetEnvMask(mask != nil)
	runner.SetEnvMask(mask)
	runner.SetSessionEnv(v.Environ())
	// The executor chain scrubs what the model reads; these three write the
	// copies that stay on disk after the turn — the evidence store's full
	// original, a process's spool on its way there, and the web response
	// cache the fetcher fills from under every one of those doors — and a
	// wrap around any of them sees the text only once it is already written.
	red.SetScrub(v.Scrub)
	if procSup != nil {
		procSup.SetEnv(v.Environ())
		procSup.SetScrub(v.Scrub)
	}
	scrubWebCache(s.web, v.Scrub)
	s.promptExtra = prompt.CombineExtra(s.promptExtra, secret.PromptBlock(v))
	return nil
}

// openNotebook opens the session's shared notebook and registers its two
// tools. Every session gets one — a conversation's colleagues and a coding
// session's children alike, because what one child learns is what the next
// should not have to find again, and that is as true of a fan-out over a
// repository as of one over the web. It persists under the session slot when
// storage is open and lives in memory for the session otherwise.
//
// It runs after the secrets are open, because the vault's rewrite has to be
// on the store before anything can be written into it: a note outlives the
// turn that wrote it, so a wrap around the store would see a value only once
// the backend had already kept it.
// See docs/capabilities/subagents.md#what-they-share.
func (s *chatSession) openNotebook(db *storage.DB) {
	var backend notebook.Backend
	if db != nil {
		backend = db
	}
	s.notebook = notebook.New(backend)
	s.notebook.SetScrub(s.vault.Scrub)
	s.toolDefs = append(append([]provider.Tool{}, s.toolDefs...), notebook.Definitions()...)
}

// openSourceLedger gives the web toolset the session's sources ledger: one
// row per fetch and per search, persisted under the session slot where
// storage is open and living for the session otherwise. It registers no
// tool — nothing the model calls reaches it, because a record the model
// could write is a record it could write anything into.
//
// It runs after the secrets are open, for the notebook's reason: a URL
// carries whatever the model put in its query string, and a row outlives
// the turn that made it.
// See docs/capabilities/chat.md#what-was-read.
func (s *chatSession) openSourceLedger(db *storage.DB) {
	if s.web == nil {
		return
	}
	var backend web.LedgerBackend
	if db != nil {
		backend = db
	}
	s.sources = web.NewLedger(backend)
	s.sources.SetScrub(s.vault.Scrub)
	s.web.UseLedger(s.sources)
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
	survey project.Info
	// workspace is the workspace prompt block over the checkout as it stands
	// when it is called: the survey the session opened on with its git half
	// asked again, and whoever else is in the checkout asked again with it.
	// It is here rather than at either of its readers because a conversation
	// rebuilt by a compaction and a child spawned an hour in are describing
	// the same tree, and two readings taken separately would be two answers.
	workspace   func() string
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

// systemPrompt builds the session's system prompt from the checkout as it
// stands, and answers with the cost of the instruction files inside it and
// the survey it was built from — the same reading the start screen draws, so
// the tree is walked once rather than twice. configExtra is the standing
// addition from the config file; everything else the session has to say has
// already been folded into its own extra by the time this is called.
//
// It is called at launch and again at a session boundary, which is why it is
// a function rather than the top of buildSessionEnv: a new conversation opens
// on the checkout it is in now — a branch switched, an instruction file
// edited, a tree that has moved — and a second copy of this assembly is how
// the two would come to disagree about what a session starts from.
func (s chatSession) systemPrompt(configExtra string) (text string, projectTokens int64, survey project.Info) {
	// DetectExec, not Detect: this session's model is told the shell its own
	// commands will run through, which is the execution shell rather than the
	// user's (internal/shell).
	info := shell.DetectExec()
	// The instruction files the session's own directory would read: the
	// user's own, then the project's from its root down to here. Read here
	// because the system prompt is built here — a session that re-read them
	// per turn would be paying for a file nobody edited.
	instructions := project.Instructions(info.Cwd, userInstructionsPath())
	// One survey per prompt, read by both the model and the start screen.
	// It shells out to git and walks the tree, so the two readers share the
	// answer rather than each asking.
	survey = project.Survey("")
	// The one thing the survey cannot see for itself. It rides on the survey
	// so the workspace block and the start screen, which are both written
	// from it, cannot state two different answers.
	survey.Sibling = s.sibling.since()
	block := project.InstructionBlock(instructions, prompt.InstructionBudget)
	extra := prompt.CombineExtra(configExtra, block, project.PromptBlock(survey), s.promptExtra)
	return s.buildPrompt(info, extra), agent.EstimateTokens(block), survey
}

// workspaceBlock is the checkout read again, or nothing where the session was
// assembled without a survey to read it from — a host with no reading of the
// tree says nothing about it rather than stopping the child that asked.
func (e *sessionEnv) workspaceBlock() string {
	if e == nil || e.workspace == nil {
		return ""
	}
	return e.workspace()
}

// addBuiltPrompt joins a block to a system prompt that has already been
// built, and to the message carrying it. Most of what the model is told is
// folded in before the prompt is built, but a few facts are not known that
// early — what contains this session's commands is resolved after the
// provider is, and a session-start hook has not run yet — and a block that
// waited for the next prompt to be built would be a block the session it was
// resolved for never sees.
func (e *sessionEnv) addBuiltPrompt(block string) {
	if e == nil || block == "" {
		return
	}
	e.sysPrompt = prompt.CombineExtra(e.sysPrompt, block)
	if len(e.messages) > 0 && e.messages[0].Role == provider.RoleSystem {
		e.messages[0].Content = e.sysPrompt
	}
}

func buildSessionEnv(cmd *cobra.Command, session chatSession, ledger *meter.Ledger) (*sessionEnv, error) {
	// --require-sandbox is folded in here rather than where containment is
	// built, because it is not only containment that reads it: a sub-agent's
	// command path asks the config too, and a flag that reached one builder
	// would be a flag the other silently ignored (sandbox.go).
	cfg := withRequiredContainment(ConfigFrom(cmd.Context()), session.requireSandbox)

	// What the checkout was not allowed to put into this session, said once
	// before it starts. Both the interactive and the headless session come
	// through here, and the headless one has no screen to read it off later
	// (trust.go).
	if note := trustStartupNote(); note != "" {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: note})
	}

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

	sysPrompt, projectTokens, survey := session.systemPrompt(cfg.Behavior.SystemPromptExtra)

	// Before anything is built on them: a named wording that cannot be read
	// stops the session here rather than letting it run on the built-in one.
	prompts, err := loadPrompts(cfg.Prompts, projectPrompts())
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

	// The launch survey with its git half asked again, and whoever else is in
	// the checkout asked again with it. Everything else it holds is the tree
	// walk, which is the expensive question and the one that does not go
	// stale while somebody is working in the directory.
	workspace := func() string {
		info := project.RereadGit(survey)
		info.Sibling = session.sibling.since()
		return project.PromptBlock(info)
	}

	return &sessionEnv{
		cfg:           cfg,
		prov:          p,
		provName:      resolved.Provider,
		modelName:     resolved.Model,
		sysPrompt:     sysPrompt,
		prompts:       prompts,
		survey:        survey,
		workspace:     workspace,
		projectTokens: projectTokens,
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

	// The changeset store is named here and opened below, where the local
	// store it persists into is open. The git stager reads it through this
	// variable rather than through a copy of what it held at registration:
	// what may be staged is what the session has changed by the time the
	// call is made, not what it had changed when the toolset was built.
	var changes *changeset.Store
	var gitWrites *structural.Writes
	if !session.conversation {
		gitWrites = &structural.Writes{
			Files: func() []string { return changes.Paths() },
			// A commit hook is a program git runs as whoever opened the
			// session, and a checkout can point git at one inside itself, so
			// it is behind the same answer every other thing a checkout
			// declares is behind.
			// See docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.
			Hooks: projectTrust().RunsOwnPrograms(),
		}
	}

	// Everything a session and an unattended run both register, on the
	// conditions they both register it under: the reducer, the web tools, the
	// language server, the structural tools, the quality gate, the process
	// supervisor, the report publisher and the vault (toolset.go). A session
	// pops a browser for a page the model published, because somebody is here
	// to read it.
	ts, err := buildToolset(cmd, &session, session.kind, toolsetOpts{scope: sc, browser: true, gitWrites: gitWrites})
	if err != nil {
		return err
	}
	defer ts.close()
	red, gate, procSup := ts.evidence, ts.gate, ts.proc

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

	db, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: chat persistence unavailable: %v\n", err)
	}
	if db != nil {
		defer db.Close()
	}
	// Pointed at the store before the prompt and the screen are built from
	// it, because both of them state whether anybody else is here.
	session.sibling = readSibling(db)

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

	session.openNotebook(db)
	session.openSourceLedger(db)

	registerSkills(&session)

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
	proj := ProjectConfigFrom(cmd.Context())

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
		// …and what the model is told about it, beside where it was told the
		// work is (scope.go). It is joined to the prompt already built rather
		// than folded in with the scope block, because the containment is
		// resolved after the provider is and the prompt had to exist for
		// that; it is joined to the session's own extra too, so the next
		// /new builds a conversation that was told the same thing.
		commandEnv := commandEnvironmentBlock(commandEnvironment{
			Mechanism: containment.Mechanism,
			Profile:   containment.Profile,
			Network:   containment.Network,
			Refused:   containment.Refusal != "",
			Ceiling:   cfg.CommandTimeout(),
			// A ceiling backgrounds a command that is still printing only
			// where there is a supervisor to hand it to (process.go).
			Backgrounds: procSup != nil,
		})
		env.addBuiltPrompt(commandEnv)
		session.promptExtra = prompt.CombineExtra(session.promptExtra, commandEnv)
	}

	// The person's own commands at this session's seams (hooks.go). They are
	// assembled here, after the containment, because what the session contains
	// its commands with is what it contains a hook with: a hook is a command
	// the session runs, and one that ran on the host while the assistant's ran
	// in a container would be the hole the containment was turned on to close.
	hookCwd, _ := os.Getwd()
	hooked := hookSet(cfg)
	hooks := buildHooks(cfg, hooked, containment.Wrap, hookCwd)
	for _, note := range hookNotes(hooked) {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
	}
	// A session opening is the first seam, and the one place where adding to
	// what the model has been told is still free. The prompt is already built
	// — it had to be, for the provider to be resolved — so what the hook says
	// is joined to it here, and to the extra the next `/new` will build one
	// from, so both conversations are told the same thing.
	start := hooks.SessionStart(cmd.Context())
	for _, note := range start.Notes {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
	}
	if start.Context != "" {
		session.promptExtra = prompt.CombineExtra(session.promptExtra, start.Context)
		env.sysPrompt = prompt.CombineExtra(env.sysPrompt, start.Context)
		if len(env.messages) > 0 && env.messages[0].Role == provider.RoleSystem {
			env.messages[0].Content = env.sysPrompt
		}
	}

	executor := ts.executor(session)

	// Session observability: content-free events (usage, tool calls,
	// mode decisions) are recorded to storage; failure just disables recording.
	recorder := startObserveRecorder(db, session.kind, env.prov.Name(), env.modelName, prices)
	defer recorder.end()
	hooks.SetSession(hookSession(recorder.sessionID()))
	// The starting mode is stamped here, as a setting, rather than left to
	// the mode-change signal: that signal fires only on a change, so a
	// session that ran start to finish in the configured default would
	// record no mode at all, and absence is also what a session that
	// recorded nothing looks like.
	settings := sessionSettings(cfg, runSettings{
		mode:       mode.String(),
		effort:     env.effort,
		rounds:     roundCapFor(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)),
		sandbox:    containment.Profile,
		model:      auxiliaryModel(env.provName, env.modelName),
		summary:    !cfg.Summary.Disabled,
		classifier: true,
	})
	recorder.stamp(env.prompts.fingerprintOf(env.sysPrompt), session.skills.Len(), projectFingerprintRoot(), settings)

	// The session boundary: /new ends this session and begins another without
	// the process restarting, so the two halves of a launch the front end
	// cannot do for itself are done here. The record is closed and another
	// opened — one row per conversation, or every rate computed over the row
	// is an average of two sittings — and the prompt is built again from the
	// checkout as it stands now. The settings are the ones this process
	// resolved and are stamped again as they were: the second row is the same
	// session assembled the same way, and what changed between the two
	// conversations is the tree, which is what the prompt hash records.
	//
	// The row the new session opens on names the same resume command the exit
	// banner does, read from one place so the two cannot come to name
	// different ones.
	resume := "shhh " + session.kind + " --continue"
	newSession := func() chat.SessionStart {
		text, projectTokens, _ := session.systemPrompt(cfg.Behavior.SystemPromptExtra)
		if recorder.restart() {
			recorder.stamp(env.prompts.fingerprintOf(text), session.skills.Len(), projectFingerprintRoot(), settings)
		}
		return chat.SessionStart{Prompt: text, Resume: resume, ProjectTokens: projectTokens}
	}
	// The gate's verdict is the record's one objective reading of whether
	// the work was right. It is wired here rather than where the runner is
	// built because the runner has no session to report to until one is
	// open.
	recordGateVerdicts(gate, recorder)
	recordSearches(session.web, recorder)

	// The changeset store is opened here rather than where the chat model
	// takes it below, because a writer child starts from the parent's
	// uncommitted work and the supervisor is built first. A conversation
	// never wires it up and leaves it empty.
	changes = changeset.New(changeset.DefaultMaxBytes)
	if db != nil {
		// The store is where those records outlive the sitting, so a
		// conversation opened again can still take one of its turns back.
		// Without one they end with the process, which makes closing the
		// terminal the same act as accepting every edit the session made.
		changes.Persist(db)
	}

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
		WithNewSession(newSession).
		WithWorkspaceBlock(env.workspaceBlock).
		WithToolDefinitions(toolDefTokens(session.toolDefs)).
		WithProjectContextTokens(env.projectTokens).
		WithToolExecutor(executor).
		WithDB(db).
		WithPricing(prices, env.modelName).
		WithLedger(ledger).
		WithSecrets(chat.Secrets{Manage: secretsManager(session.vault), Scrub: session.vault.ScrubMessage}).
		WithScope(sc).
		WithMaxToolRounds(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)).
		WithConfigWriter(configWriter(proj)).
		WithMouse(cfg.MouseEnabled()).
		WithPasteThresholds(cfg.Appearance.PasteLines, cfg.Appearance.PasteColumns).
		WithRailWidth(components.RailWidthOrAuto(cfg.Appearance.RailWidth)).
		WithNotify(cfg.NotifyEnabled()).
		WithWindowTitle(cfg.WindowTitleEnabled()).
		WithDefaults(chat.Defaults{
			Model:      cfg.Provider.Model,
			AgentModel: cfg.Agents.Model,
			Outranked:  outranking(resolve.ModelOutranks(*session.flags), proj, "provider.model"),
		}).
		WithApprovalMode(mode, cycle).
		WithSteering(steering(cfg, env.prompts)).
		WithRetryLimit(cfg.Behavior.ProviderRetries).
		WithClassifier(classifier).
		WithSummarizer(summarizer).
		WithTitler(titler, cfg.TitlesEnabled()).
		WithModelSwitcher(env.switchModel).
		WithReasoning(env.effort, env.switchReasoning).
		WithReasoningDefault(cfg.Provider.Reasoning,
			outranking(resolve.ReasoningOutranks(*session.flags), proj, "provider.reasoning")).
		WithProvider(env.provName, env.replaceKey, env.switchProvider).
		WithModelOptions(provider.KnownModels(env.prov.Name())).
		WithModelLister(modelListerFor(env.prov)).
		WithEndpointWindows(endpointWindowsFor(env.prov))
	model = model.WithNotebook(session.notebook).WithSources(session.sources)
	if session.conversation {
		model = model.WithConversation()
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
			WithCommandDenylist(cfg.Behavior.CommandDenylist).
			WithCommandTimeout(cfg.CommandTimeout()).
			WithReadOnlyCommands(cfg.Behavior.ReadOnlyCommands, !cfg.ReadOnlyAutoEnabled()).
			WithGitSnapshots(gitSnapshot).
			WithChangeset(changes, changeset.NewTracker(".")).
			WithTreeCheck(withSibling(treeCheck(cfg), session.sibling)).
			// First contact: the empty session's start screen, surveyed
			// once here rather than assembled per frame.
			WithStartScreen(buildStartInfo(env.survey, db, gate != nil, chatTrust(db), proj,
				projectWordings(cfg.Prompts, projectPrompts()))).
			// The one thing the screen offers that writes: scaffolding the
			// checkout's own context file, behind a card.
			WithScaffold(buildScaffold(db, cwd))
	}
	if red != nil {
		model = model.WithEvidence(chat.Evidence{
			Reduce: red.Process,
			Manage: evidenceManager(red),
			// The window trim writes into the same store, so what it elides
			// is retrievable the way a reduced result is.
			Keep: red.Keep,
			// And the sources screen reads back out of it: a ledger row
			// that names an entry can show the page it named.
			Read: evidenceReader(red),
		})
	}
	// The mutation seam: the language server's diagnostics, then the
	// post-tool hooks. It is one field and two things want it, because a
	// write and an edit reach no executor and this is where they can be seen
	// (hooks.go).
	if mutation := chainMutation(lspMutationHook(session.lsp), hookPostMutation(hooks)); mutation != nil {
		model = model.WithMutationHook(mutation)
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
	// The backlog is wired into both sessions. It is not a coding surface:
	// one file per item, a status, a ready rule and an archive say nothing
	// about code, and a conversation that keeps a reading list wants them.
	// What a conversation cannot do is asked of a run's own steps when one
	// is asked for, one step at a time (internal/todo/run/pipeline.go).
	// See docs/capabilities/chat.md#the-backlog-is-here-too.
	if cwd != "" {
		root := todo.Root(cwd)
		// The vocabulary the backlog is written in is resolved once, for the
		// process, and handed to every reader of it rather than reached for:
		// which words an item carries is the project's answer, so there is
		// no default to fall back on (todoprofile.go).
		profile := todoProfile()
		// What the reading is a reading of, in the words the person would
		// use for it. A prompt that called a conversation a coding session
		// would be asking the model to read something that did not happen.
		reading := todo.ExtractConfig{Model: env.modelName, Session: todo.CodingSession}
		if session.conversation {
			reading.Session = todo.Conversation
		}
		model = model.WithTodos(chat.Todos{
			Root: root, Manage: todoManager(root), Detail: todoDetail,
			Profile: profile,
			// The session's own model reads the session: extraction is a
			// judgement about the whole conversation, not a status line, and
			// the cheap summary model is the wrong price point for it.
			Extractor: todo.NewExtractor(ledger.For(env.prov, meter.SourceBacklog), reading, profile),
			// Drafting an item from a sentence is the same judgement in one
			// paragraph rather than over a whole session, so it goes to the
			// same model and is metered against the same source.
			Drafter:     todo.NewDrafter(ledger.For(env.prov, meter.SourceBacklog), reading, profile),
			NoCommit:    !cfg.TodoCommitEnabled(),
			ItemTimeout: cfg.TodoItemTimeout(),
			GroomStale:  cfg.TodoGroomStale(),
			// A closed sprint's report is a page, and it goes through the
			// same publisher the model's own pages do — one report store,
			// one server, one retention rule, whoever asked for the page.
			PublishReport: sprintReportPublisher(ts.reports),
			Wordings:      env.prompts.todo,
			Pipeline:      todoPipeline(),
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
	// What this session may fetch without asking, which a spawn card states
	// and a host grant grows. It is read and written on the UI goroutine
	// alone — the fetcher keeps its own copy behind its own lock, because
	// that one is read while a fetch is in flight.
	reach := &hostReach{hosts: cfg.Web.AllowHosts}
	if session.web != nil {
		webTools := session.web
		// The two config lists and the sink the session's own grants go to:
		// a fetch is decided on its host, and the fetcher is what the
		// decision has to reach for a redirect to be answered by it too.
		model = model.
			WithHostRules(cfg.Web.AllowHosts, cfg.Web.DenyHosts).
			// The pacing a fan-out puts on one host is the fetcher's, and so
			// is the wait a refusal costs; the session only says it on the
			// row and gives it up when the turn is cancelled.
			WithFetchWaits(webTools.Fetcher.Waiting, webTools.Fetcher.AbandonWaits).
			WithHostGrants(func(hosts []string) {
				reachable := append([]string(nil), hosts...)
				reach.hosts = reachable
				webTools.Fetcher.SetGrantedHosts(func(host string) bool {
					return agent.HostMatches(reachable, host)
				})
			})
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
			// Host is the card's own domain row, and it is what [a] grants:
			// the reader answers for the site the card named.
			return chat.GatedPreview{Action: "fetch", Summary: summary, Host: plan.Host, Fields: []chat.GatedField{
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
			fields := []chat.GatedField{
				{Label: "touches", Value: plan.Scope, Detail: "in its own worktree, not this checkout", Open: plan.Writer},
				{Label: "undo", Value: "reviewed", Detail: undo},
				{Label: "budget", Value: plan.Budget, Detail: "counted in the session totals"},
			}
			// A child that reaches the web arrives with the hosts this
			// session has answered for and can never add to them, so what it
			// may reach without asking is part of this decision.
			if childReachesWeb(session, agents, plan.Role) {
				fields = append(fields, chat.GatedField{
					Label: "reaches", Value: reach.value(),
					Detail: "the hosts granted here; any other asks you", Open: true,
				})
			}
			return chat.GatedPreview{Action: "spawn", Summary: summary, Fields: fields}, nil
		}
		model = model.WithSubagents(sup).WithPersonas(buildPersonas(session, env, agents, sup, ledger))
	}
	// The writing half of git is gated at the write tier: it proceeds where
	// an edit proceeds and is asked where an edit is asked, because what it
	// changes is the repository rather than the world outside the machine.
	if session.structural != nil && session.structural.Has(structural.GitWriteToolName) {
		st := session.structural
		gatedPreviews[structural.GitWriteToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			return gitWriteGatedPreview(st, args)
		}
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
			Manage:   mcpManager(mcpTools, session.mcpCatalog),
			Sources:  mcpToolSources(mcpTools),
			Prompts:  mcpTools.Prompts,
			Render:   mcpTools.Render,
			Refresh:  mcpTools.Refresh,
		})
	}
	if len(gatedPreviews) > 0 {
		model = model.WithGatedTools(gatedPreviews)
	}
	// Last, because the tool seams ask the model which calls it gates and
	// every registration above is part of that answer (chat/hooks.go).
	model = model.WithHooks(hooks, executor)

	if session.wantsResume() {
		reopened, err := session.resumeChat(db)
		if err != nil {
			return err
		}
		if reopened.cancelled {
			return nil
		}
		if reopened.slot != "" {
			// Refresh the system prompt so shell/cwd context is current.
			if len(reopened.messages) > 0 && reopened.messages[0].Role == provider.RoleSystem {
				reopened.messages[0].Content = env.sysPrompt
			}
			// The conversation is also told what the checkout looks like now,
			// ahead of everything it remembers: the transcript describes the
			// tree as it was, and after a pull or a rebase that is not a
			// stale picture but a misleading one
			// (docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is).
			// A front-end with no model to hang it on asks chat.ResumeContext
			// for the same reading.
			model = model.WithResumedMessages(reopened.slot, reopened.messages)
			// A conversation the slot says was saved mid-turn comes back
			// mid-turn. Without this it would open idle with an unanswered
			// round in front of it, which is the shape a person reads as
			// "it finished" — and the round it is owed would never be asked
			// for (docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held).
			if h, ok, err := db.ChatHold(reopened.slot); err == nil && ok {
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
		printExitBanner(m.ExitBanner(resume))
	}
	// The last seam: the session stopping. It fires here rather than inside
	// the program because there is no screen left to hold it up, and because
	// every way out of a session — the quit chord, an error, the last turn —
	// has already come back through this one return.
	for _, note := range hooks.Stop(cmd.Context(), hook.Pos{}, "").Notes {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: "hooks: " + note})
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

// livePhrase is why a slot another running session is autosaving into
// cannot be opened: the conversation in it is still being written, and that
// session's next save takes the slot straight back from whoever loaded it.
// One mark, one sentence, and every door that draws the mark says it. A
// picker that marks a row and opens it anyway makes the mark mean two
// things in two places, and the reader who trusted it is the one who loses
// the conversation.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
const livePhrase = "open in another session"

// chatBrowseRows is the saved chats as the browser lists them: the name, what
// the conversation was about and how big it is, and what deleting it would
// take along.
func chatBrowseRows(db *storage.DB, entries []storage.ChatListEntry) []components.ChatRow {
	rows := make([]components.ChatRow, len(entries))
	for i, e := range entries {
		rows[i] = components.ChatRow{
			ID:      e.Name,
			Name:    e.Name,
			Title:   e.Title,
			Turns:   countOf(e.Turns, "turn", "turns"),
			When:    e.UpdatedAt.Local().Format("Jan 2 15:04"),
			Updated: e.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		}
		if e.Live {
			// The row keeps its place. Reading it, renaming it and
			// deleting it are all still the reader's to do; the one thing
			// it will not do is open, because the other session's next
			// autosave takes the slot back and the conversation loaded
			// here goes with it. Naming the slot is still the way in, and
			// that is the flag's business rather than this list's.
			rows[i].Mark = livePhrase
			rows[i].Refused = fmt.Sprintf(
				"%q is %s — its conversation is still being written there.", e.Name, livePhrase)
		}
		if n, err := db.CountChatBranches(e.Name); err == nil && n > 0 {
			rows[i].Deleting = "and its " + branchCount(n)
		}
	}
	return rows
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
//
// The record of what the model has been shown is not the front-end's — it is
// one record per process, kept by the tools that do the showing — so it is
// wired here, where both front-ends build their reading from one answer
// rather than two that can drift apart.
func treeCheck(cfg config.Config) *agent.TreeCheck {
	if !cfg.TreeCheckEnabled() {
		return nil
	}
	return &agent.TreeCheck{
		Dir:         ".",
		IsCommand:   func(name string) bool { return name == tools.ExecCommandName },
		ReadChanged: tools.SeenChanged,
		Log:         func(msg string) { logs.Logger().Warn(msg) },
	}
}

// gitSnapshot captures the workspace's git state for rewind checkpoints
// , so /rewind can report what diverged since a checkpoint.
//
// Whether the content was digested travels with the digest. Dropping it here
// would hand the rewind view a hash it cannot read the way the gate reads it:
// past the bound, two of them compare equal over different files, and the
// divergence line would report an unchanged tree on the strength of it.
func gitSnapshot() chat.GitSnapshot {
	fp := quality.TakeFingerprint(".")
	return chat.GitSnapshot{
		Repo: fp.Repo, Head: fp.Head, StatusHash: fp.StatusHash,
		DirtyPaths: fp.DirtyPaths, Unhashed: fp.Unhashed,
	}
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

// hostReach is the session's reachable hosts as a card states them.
type hostReach struct{ hosts []string }

// value is the hosts in one line, or the phrase for a session that has
// answered for none — a child with no grants is not a child with no web, it
// is a child whose every fetch comes back here as a card.
func (h *hostReach) value() string {
	if h == nil || len(h.hosts) == 0 {
		return "no host granted yet — every fetch asks you"
	}
	return strings.Join(h.hosts, ", ")
}

// childReachesWeb reports whether a child of this role is given the fetch
// tool at all: the built-in roles get whatever web toolset the session
// opened, and a profile from a file gets it only where its own permissions
// and tool list say so (subagents.go, profileEnv).
func childReachesWeb(session chatSession, agents *agentProfiles, role subagent.Role) bool {
	if session.web == nil || agents == nil {
		return false
	}
	def, ok := agents.definitions[string(role)]
	if !ok {
		return true
	}
	return def.Has(config.PermissionWeb) && def.Allows(web.FetchToolName)
}

// wantsResume reports whether the session was told to open a stored
// conversation rather than start one.
func (s chatSession) wantsResume() bool {
	return s.continueLast || s.resumePick || s.resumeName != ""
}

// reopenedChat is a conversation taken back out of the store: the slot it
// lives in and the messages it holds. An empty slot is a resume that did not
// happen — nothing has ever been saved — and the front-end opens a fresh
// conversation instead.
type reopenedChat struct {
	slot     string
	messages []provider.Message
	// cancelled is the picker closed without a choice. It is neither a
	// failure nor a session: the person asked to see the list and then said
	// no, and there is nothing left for the command to do.
	cancelled bool
}

// resumeChat resolves --continue, --resume and a conversation `shhh chats`
// has already picked into the one to open on.
//
// It is one function over the store rather than a step inside the session
// runner because both front-ends resume: an interactive session hands what
// comes back to the chat model, and an unattended run puts it in front of
// its prompt. A run told to continue that started from nothing instead is
// the failure this prevents, and unattended is exactly where nobody is
// there to notice it.
// See docs/capabilities/sessions-and-memory.md#an-unattended-run-comes-back-too.
func (s chatSession) resumeChat(db *storage.DB) (reopenedChat, error) {
	if db == nil {
		return reopenedChat{}, fmt.Errorf("chat persistence is unavailable, cannot resume")
	}
	// A conversation that has already been named is the one that opens. The
	// newest slot is the answer to a different question, and answering this
	// one with it opens somebody else's conversation under the name the
	// person typed.
	name := s.resumeName
	switch {
	case s.resumePick:
		picked, err := pickSavedChat(db)
		if err != nil {
			return reopenedChat{}, err
		}
		if picked == "" {
			return reopenedChat{cancelled: true}, nil
		}
		name = picked
	case name == "":
		// --continue is the newest slot, whatever it was called: every
		// session autosaves to a slot of its own, so "the last session" is
		// a query rather than a name.
		recent, ok, err := db.MostRecentChat()
		if err != nil {
			return reopenedChat{}, err
		}
		// The newest slot can be one another running session is autosaving
		// into, and the offer on the start screen quietly takes the one
		// before it. This is not an offer. Somebody asked for the last
		// session, and answering an instruction with a different
		// conversation is the failure that is worst where nobody is
		// watching, so it is named and refused instead. Naming it is also
		// the way through: a slot asked for by name is opened whoever holds
		// it, which is the rule the branch above already follows.
		if recent.Held != "" {
			return reopenedChat{}, fmt.Errorf(
				"%q is %s; resume it by name to open it anyway", recent.Held, livePhrase)
		}
		if ok {
			name = recent.Name
		}
	}
	var (
		messages []provider.Message
		loadErr  error
	)
	if name == "" {
		loadErr = fmt.Errorf("no saved chats")
	} else {
		messages, loadErr = db.LoadChat(name)
	}
	if loadErr != nil {
		// A name that is not there is worth stopping for; "the most recent"
		// on a machine with no history is a first run, and starting is the
		// answer it wanted.
		if !s.continueLast {
			return reopenedChat{}, loadErr
		}
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Skip,
			Subject: "no previous session", Detail: "starting fresh"})
		return reopenedChat{}, nil
	}
	tools.NoteRestoredReads(messages)
	return reopenedChat{slot: name, messages: messages}, nil
}

// defaultChatsWidth is what the browser is drawn at before the terminal has
// said how wide it is — the working width the artboard is drawn at.
const defaultChatsWidth = 130

// chatsModel hosts the saved-chat browser
// (docs/interface/surfaces.md#the-supporting-screens). It owns everything the
// screen deliberately does not: what a conversation is, what holds a slot,
// how many branches go with one, and when any of it reaches the store.
//
// The screen resolves a key to a components.ChatCommand; the host carries it
// out, says so in the notice line, and hands back fresh rows. `[enter]` is the
// exception and closes the screen, because opening a conversation hands the
// terminal to a session.
type chatsModel struct {
	db      *storage.DB
	entries []storage.ChatListEntry
	result  components.ChatResult

	screen components.ChatScreen
}

func newChatsModel(db *storage.DB, entries []storage.ChatListEntry) *chatsModel {
	m := &chatsModel{db: db, entries: entries}
	m.refresh()
	return m
}

// answer carries out the housekeeping a key asked for and keeps what the
// screen closed with, which is read once the terminal has been given back.
// The notice is not cleared here, unlike the other screens' hosts: this
// screen writes its own on the key it refused, and clearing after its Update
// has run would wipe the sentence that key just produced. It clears it
// itself, on the next keystroke.
func (m *chatsModel) answer(done bool, result components.ChatResult) tea.Cmd {
	if result.Do != nil {
		m.apply(*result.Do)
	}
	if !done {
		return nil
	}
	m.result = result
	return tea.Quit
}

// apply carries out one command against the store and re-reads the listing,
// so the screen redraws from the store rather than from what it thinks
// changed.
func (m *chatsModel) apply(command components.ChatCommand) {
	switch command.Act {
	case components.ChatRename:
		if err := m.db.RenameChat(command.ID, command.Name); err != nil {
			m.screen.Notice = "rename: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("renamed %q to %q", command.ID, command.Name)
	case components.ChatDelete:
		if err := m.db.DeleteChat(command.ID); err != nil {
			m.screen.Notice = "delete: " + err.Error()
			return
		}
		m.screen.Notice = fmt.Sprintf("deleted %q", command.ID)
	}
	entries, err := m.db.ListChats()
	if err != nil {
		// The rows it already has stay: a browser that emptied itself on a
		// failed read would look like a store that had lost everything.
		m.screen.Notice = "list: " + err.Error()
		return
	}
	m.entries = entries
	if m.screen.Focus >= len(m.entries) {
		m.screen.Focus = max(len(m.entries)-1, 0)
	}
	m.refresh()
}

// refresh rebuilds every row and the header subject from the listing the host
// is holding.
func (m *chatsModel) refresh() {
	m.screen.Rows = chatBrowseRows(m.db, m.entries)
	m.screen.Subject = countOf(len(m.entries), "conversation", "conversations")
}

// pickSavedChat shows the saved-chat browser and returns the chosen session
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

	m := newChatsModel(db, entries)
	if _, err := newProgram(newScreenModel(&m.screen, defaultChatsWidth, m.answer)).Run(); err != nil {
		return "", err
	}
	if !m.result.Open {
		return "", nil
	}
	return m.result.ID, nil
}

// outranking is what a session tells a slash command that writes a default:
// the flag or the environment variable that beats the file, and where
// neither does, the checkout's own settings file where it decided that key.
// A default written into the user's file and then overruled reads exactly
// like one that was never saved, which is how `/model default` came to look
// broken while writing the file correctly every time — and a checkout's file
// is the one rank that stays in force after the terminal is closed.
func outranking(above string, proj config.Project, key string) string {
	if above != "" {
		return above
	}
	if !proj.Sets(key) {
		return ""
	}
	return proj.Display + " in this checkout sets " + key
}

// gitWriteGatedPreview is the card a git write asks through. Its fields are
// the boundaries of the act, and each is something the reader would otherwise
// have to know already: what happens to work that is not the session's,
// whether anything leaves the machine, and — for the one verb that cannot be
// taken back — whether the repository's own checks ran and what the way back
// is.
//
// `push` is stated on every one of them, not only on a commit, because the
// question a person asks when an agent touches git is whether it can reach
// the remote, and an answer that appears on some cards and not others is one
// the reader has to go looking for.
func gitWriteGatedPreview(st *structural.Toolset, args json.RawMessage) (chat.GatedPreview, error) {
	w, err := st.WritePlan(args)
	if err != nil {
		return chat.GatedPreview{}, err
	}
	hooks := chat.GatedField{Label: "hooks", Value: "run", Detail: "the checkout's own commit hooks; a failure cancels and changes nothing"}
	if !w.Hooks {
		hooks = chat.GatedField{Label: "hooks", Value: "skipped", Detail: "this checkout is not trusted, so its own programs do not run"}
	}
	fields := []chat.GatedField{
		{Label: "stages", Value: "this session's files only", Detail: "work that was already in the tree is never staged"},
		{Label: "push", Value: "no", Detail: "shhh never pushes; the remote is yours"},
	}
	if w.Verb == structural.CommitVerb {
		fields = append(fields, hooks,
			chat.GatedField{Label: "undo", Value: "git revert", Detail: components.CommitUndoNote})
	}
	return chat.GatedPreview{
		Title:    w.Title,
		Action:   w.Verb,
		Summary:  w.Summary,
		Fields:   fields,
		Write:    true,
		DenyLine: structural.WriteLine(args),
	}, nil
}
