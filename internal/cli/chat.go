package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/browse"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/update"
	"github.com/rfizzle/shhh/internal/web"
	"github.com/spf13/cobra"
)

// chatSession parameterizes the shared chat TUI entry point: `shhh chat` and
// `shhh code` run the same Bubble Tea model and differ only in system prompt,
// registered toolset, and title.
type chatSession struct {
	title string
	// kind labels recorded observability sessions (S-065): "chat" or "code".
	kind         string
	buildPrompt  func(shell.Info, ...string) string
	toolDefs     []provider.Tool
	flags        *resolve.Opts
	continueLast bool
	resumePick   bool
	// web is the guarded web toolset (S-066); nil leaves the web tools
	// unregistered (`shhh chat` today).
	web *web.Toolset
	// lsp is the language-server toolset (S-071): after-edit diagnostics plus
	// the definition/references tools; nil (no servers detected, or disabled)
	// is a clean no-op. `shhh code` only.
	lsp *lsp.Toolset
	// structural wraps external code tools (S-072: fd, ast-grep, sd, tokei,
	// jaq), each registered only when its binary is on PATH; nil leaves them
	// unregistered. `shhh code` only.
	structural *structural.Toolset
	// gate registers the quality-gate tool and /gate command (S-067);
	// `shhh code` only.
	gate bool
	// processes registers the long-running process supervisor (S-073): the
	// process tool (start approval-gated) and the /ps command; `shhh code`
	// only. Session end terminates every owned process tree.
	processes bool
	// agents registers the sub-agent orchestration tools and supervisor
	// (S-068); `shhh code` interactive sessions only.
	agents bool
	// memory enables durable memory (S-070): bounded recall into the system
	// prompt plus the confirm-gated remember tool; `shhh code` interactive
	// sessions only (headless runs have nobody to confirm a proposal).
	memory bool
	// promptExtra is appended to the system prompt after config and project
	// context (e.g. the recalled-memory block).
	promptExtra string
	// maxRounds overrides behavior.max_tool_rounds for this session, where 0
	// means no cap at all — the unattended `shhh code --max-rounds 0`, where
	// the S-109 checkpoint has nobody to stop for. maxRoundsSet tells the two
	// zeroes apart exactly as printOpts does; `shhh chat` sets neither and
	// takes the config.
	maxRounds    int
	maxRoundsSet bool
	// addDirs are --add-dir directories: the working scope (S-141) a session
	// starts with beyond the directory it was opened in, on top of
	// behavior.scope_dirs.
	addDirs []string
}

func newChatCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumePick bool
	var addDirs []string

	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Start an interactive chat session",
		Long:  "Open a multi-turn conversation with the LLM to explore complex tasks iteratively.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChatSession(cmd, args, chatSession{
				title:        "shhh chat",
				kind:         "chat",
				buildPrompt:  prompt.BuildChat,
				toolDefs:     tools.DefinitionsWithExec(),
				flags:        &flags,
				continueLast: continueLast,
				resumePick:   resumePick,
				addDirs:      addDirs,
			})
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().BoolVarP(&resumePick, "resume", "r", false, "pick a saved chat to resume")
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")
	addDirFlag(cmd, &addDirs)

	return cmd
}

// sessionEnv is the provider-and-prompt setup shared by the interactive chat
// TUI and headless print mode: resolved model, initial messages, and a stream
// closure over the session's provider.
// modelListTimeout bounds the /model picker's live catalog query (S-083); a
// gateway that is slow or down should cost the user a beat, not the session.
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

type sessionEnv struct {
	cfg       config.Config
	prov      provider.Provider
	provName  string
	modelName string
	sysPrompt string
	// projectTokens is the estimated context cost of the project context
	// injected into the system prompt, which /stats and the inspector rail
	// name as its own occupancy category (S-093).
	projectTokens int64
	messages      []provider.Message
	stream        agent.StreamFunc
	switchModel   func(string)
	// effort is the reasoning level the session resolved to, and
	// switchReasoning is what ctrl+r and /reasoning change it with (S-139).
	// Like the model it is read by the stream closure from another
	// goroutine, so it lives under the same mutex.
	effort          provider.Effort
	switchReasoning func(provider.Effort)
	// reasoning reads the level that is live now, for the streams built once
	// at session start and used for the rest of it — a sub-agent's. Without
	// it a level set with ctrl+r would be true of the session and false of
	// every child it spawns.
	reasoning func() provider.Effort
	// replaceKey and switchProvider are what a provider failure's [k] and
	// [p] do (S-106): both rebuild the provider in place, and both leave the
	// session untouched when the rebuild fails.
	replaceKey     func(string) error
	switchProvider func(string) error
}

func buildSessionEnv(cmd *cobra.Command, session chatSession) (*sessionEnv, error) {
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

	info := shell.Detect()
	projectContext := project.FindContext()
	promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, projectContext, session.promptExtra)
	sysPrompt := session.buildPrompt(info, promptExtra)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
	}

	effort, err := provider.ParseEffort(resolved.Reasoning)
	if err != nil {
		return nil, err
	}

	compOpts := provider.CompletionOpts{
		Model:      resolved.Model,
		Tools:      session.toolDefs,
		ToolChoice: "auto",
		Effort:     effort,
	}

	// /model switches the model mid-session, and a provider failure's [k] and
	// [p] switch the key and the provider under it (S-106). All three are
	// read by the stream closure from a background goroutine, so one mutex
	// guards the model, the provider and the key it was built with.
	var sessionMu sync.Mutex
	currentModel := resolved.Model
	currentEffort := effort
	currentProvider := resolved.Provider
	currentKey := req.APIKey
	currentBaseURL := req.BaseURL

	// rebuild resolves the provider again with whatever the session has
	// changed. It replaces nothing until the new provider is built: a key
	// that cannot be resolved leaves the session exactly as it was.
	//
	// What it swaps is the stream — the turn's own requests. The permission
	// classifier, the observability recorder and the /model lister were
	// wired to the provider this session opened on and keep it; a classifier
	// that fails on a dead key falls back to asking, which is the right
	// answer anyway.
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

	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ctx, cancel := context.WithCancel(cmd.Context())
		opts := compOpts
		sessionMu.Lock()
		opts.Model = currentModel
		opts.Effort = currentEffort
		active := p
		sessionMu.Unlock()
		ev, sErr := active.StreamCompletion(ctx, msgs, opts)
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
		projectTokens: agent.EstimateTokens(projectContext),
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
	// The working scope (S-141): the directory the session was opened in plus
	// whatever config and --add-dir put beside it. Containment writes to it,
	// the approval cards ask before anything leaves it, and /add-dir grows it
	// mid-session. It is built first because everything that runs a command
	// — the gate, sub-agents, the session's own runner — takes it.
	sc, err := sessionScope(ConfigFrom(cmd.Context()), session.addDirs)
	if err != nil {
		return err
	}

	// Tool-output reduction (S-064): bulky tool results are reduced before
	// the model sees them, with the originals retrievable via the evidence
	// tool. No store means no reduction and no evidence tool.
	red := openEvidence()
	if red != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), evidence.ToolDefinition())
	}
	// Guarded web tools (S-066): web_fetch (approval-gated as an external
	// action) and, when a search key is configured, web_search.
	if session.web != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.web.Definitions()...)
	}
	// LSP integration (S-071): definition/references tools when a language
	// server was detected; servers start lazily and shut down with the session.
	if session.lsp != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.lsp.Definitions()...)
		defer session.lsp.Close()
	}
	// Structural code tools (S-072): fd, ast-grep, sd, tokei, jaq — read-only
	// wrappers, each registered only when its binary is on PATH.
	if session.structural != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), session.structural.Definitions()...)
	}
	// Quality gate (S-067): the model can run the project's own checks by
	// suite name; command text only ever comes from trusted config.
	var gate *quality.Runner
	if session.gate {
		gate = openQualityGate(ConfigFrom(cmd.Context()), red, sc)
	}
	if gate != nil {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), quality.ToolDefinition())
	}
	// Sub-agent orchestration (S-068): spawn_agent (approval-gated) and
	// agent_report join the toolset; the supervisor itself is built once the
	// provider is resolved.
	if session.agents {
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), subagent.Definitions()...)
	}
	// Long-running process supervisor (S-073): the process tool (start goes
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

	db, err := storage.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: chat persistence unavailable: %v\n", err)
	}
	if db != nil {
		defer db.Close()
	}

	// Durable memory (S-070): recalled entries join the system prompt under a
	// hard entry/token budget — cited by id, zero model calls — and the
	// remember tool lets the model propose new ones, each confirmed by the
	// user before it persists.
	var mem *memory.Store
	if session.memory && db != nil && !ConfigFrom(cmd.Context()).Behavior.MemoryDisabled {
		mem = openMemoryStore(db)
		session.toolDefs = append(append([]provider.Tool{}, session.toolDefs...), memory.ToolDefinition())
		memCfg := ConfigFrom(cmd.Context())
		if entries, recallErr := mem.Recall(memCfg.EffectiveMemoryMaxEntries(), int64(memCfg.EffectiveMemoryMaxTokens())); recallErr == nil {
			session.promptExtra = prompt.CombineExtra(session.promptExtra, memory.PromptBlock(entries))
		}
	}

	// The model is told where the work is (S-141), so an out-of-scope path is
	// a question it asks rather than a call the user refuses.
	session.promptExtra = prompt.CombineExtra(session.promptExtra, scopePromptBlock(sc))

	env, err := buildSessionEnv(cmd, session)
	if err != nil {
		return err
	}
	cfg := env.cfg

	prices := loadPricing()

	// Permission mode (S-059): starting mode and Shift+Tab cycle come from
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

	// Auto mode's permission classifier (S-060) reuses the session provider;
	// behavior.classifier_model overrides the model (default: session model).
	classifierModel := cfg.Behavior.ClassifierModel
	if classifierModel == "" {
		classifierModel = env.modelName
	}
	classifier := agent.NewClassifier(env.prov, agent.ClassifierConfig{
		Model:     classifierModel,
		Timeout:   time.Duration(cfg.Behavior.ClassifierTimeoutSeconds) * time.Second,
		MaxTokens: cfg.Behavior.ClassifierMaxTokens,
		Retries:   cfg.Behavior.ClassifierRetries,
	})

	// Process containment (S-062): assistant commands run wrapped when a
	// mechanism is available; the confirm prompt shows the state either way.
	containment, err := buildContainment(cfg, sc)
	if err != nil {
		return err
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
	if gate != nil {
		baseExecutor = gate.WrapExecutor(baseExecutor)
	}
	if procSup != nil {
		baseExecutor = procSup.WrapExecutor(baseExecutor)
	}
	executor := baseExecutor
	if red != nil {
		executor = red.WrapExecutor(baseExecutor)
	}

	// Session observability (S-065): content-free events (usage, tool calls,
	// mode decisions) are recorded to storage; failure just disables recording.
	recorder := startObserveRecorder(db, session.kind, env.prov.Name(), env.modelName, prices)
	defer recorder.end()

	// Sub-agent supervisor (S-068): spawn_agent and agent_report short-circuit
	// on the executor chain; Close cancels the child tree and removes
	// leftover worktrees when the session ends.
	var sup *subagent.Supervisor
	if session.agents {
		sup = buildSupervisor(cmd.Context(), cfg, session, env, red, recorder, db, prices, classifier, sc)
		executor = sup.WrapExecutor(executor)
		defer sup.Close()
	}

	model := chat.New(env.messages, env.stream).
		WithTitle(session.title).
		WithObserver(recorder.observer()).
		WithToolTokenEstimate(estimateToolDefTokens(session.toolDefs)).
		WithProjectContextTokens(env.projectTokens).
		WithToolExecutor(executor).
		WithDB(db).
		WithPricing(prices, env.modelName).
		WithRunner(runner.RunCapture).
		WithTailRunner(runner.RunCaptureTail).
		WithContainment(containment).
		WithScope(sc).
		WithMaxToolRounds(maxRoundsFor(cfg, session.maxRounds, session.maxRoundsSet)).
		WithCommandAllowlist(cfg.Behavior.CommandAllowlist).
		WithReadOnlyCommands(cfg.Behavior.ReadOnlyCommands, !cfg.ReadOnlyAutoEnabled()).
		WithConfigWriter(configWriter()).
		WithMouse(cfg.Appearance.Mouse).
		WithDefaults(chat.Defaults{
			Model:      cfg.Provider.Model,
			AgentModel: cfg.Agents.Model,
			Outranked:  resolve.ModelOutranks(*session.flags),
		}).
		WithApprovalMode(mode, cycle).
		WithClassifier(classifier).
		WithModelSwitcher(env.switchModel).
		WithReasoning(env.effort, env.switchReasoning).
		WithReasoningDefault(cfg.Provider.Reasoning, resolve.ReasoningOutranks(*session.flags)).
		WithProvider(env.provName, env.replaceKey, env.switchProvider).
		WithModelOptions(provider.KnownModels(env.prov.Name())).
		WithModelLister(modelListerFor(env.prov)).
		WithGitSnapshots(gitSnapshot).
		WithChangeset(changeset.New(changeset.DefaultMaxBytes), changeset.NewTracker(".")).
		// First contact (S-105): the empty session's start screen, surveyed
		// once here rather than assembled per frame.
		WithStartScreen(buildStartInfo(db, gate != nil))
	if red != nil {
		model = model.WithEvidence(chat.Evidence{Reduce: red.Process, Manage: evidenceManager(red)})
	}
	if session.lsp != nil {
		model = model.WithMutationHook(lspMutationHook(session.lsp))
	}
	if gate != nil {
		model = model.WithGate(chat.Gate{Manage: gateManager(gate)})
	}
	if procSup != nil {
		model = model.WithProcesses(chat.Processes{Manage: processManager(procSup)})
	}
	if mem != nil {
		model = model.WithMemory(chat.Memory{
			Manage:       memoryManager(mem),
			Save:         memorySaver(mem),
			ProjectScope: mem.Project(),
		})
	}
	// web_fetch and spawn_agent go through the approval queue as generic
	// external actions: manual and accept-edits prompt, auto defers to the
	// classifier (S-060).
	gatedPreviews := map[string]chat.GatedPreviewFunc{}
	if session.web != nil {
		webTools := session.web
		gatedPreviews[web.FetchToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			summary, err := webTools.FetchSummary(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			// The card's blast-radius block for an outbound request (S-101):
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
			summary, err := subagent.SpawnSummary(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			plan, err := subagent.SpawnPlan(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			// A child edits in its own worktree; nothing reaches the checkout
			// until its patch is approved on a card of its own (S-101).
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
		model = model.WithSubagents(sup)
	}
	if len(gatedPreviews) > 0 {
		model = model.WithGatedTools(gatedPreviews)
	}

	if session.continueLast || session.resumePick {
		if db == nil {
			return fmt.Errorf("chat persistence is unavailable, cannot resume")
		}
		name := chat.AutosaveName
		if session.resumePick {
			picked, err := pickSavedChat(db)
			if err != nil {
				return err
			}
			if picked == "" {
				return nil
			}
			name = picked
		}
		resumed, err := db.LoadChat(name)
		if err != nil {
			if session.continueLast {
				fmt.Fprintln(os.Stderr, "No previous session found, starting fresh.")
			} else {
				return err
			}
		} else {
			// Refresh the system prompt so shell/cwd context is current.
			if len(resumed) > 0 && resumed[0].Role == provider.RoleSystem {
				resumed[0].Content = env.sysPrompt
			}
			model = model.WithResumedMessages(resumed)
		}
	}
	if r := update.CheckCached(version); r != nil {
		model = model.WithUpdateNotice("update: " + r.Latest)
	}

	initialPrompt := ""
	if len(args) > 0 {
		initialPrompt = args[0]
	}

	// Piped stdin becomes context for the first message; the TUI then
	// reads keys from the terminal directly.
	programOpts := []tea.ProgramOption{tea.WithAltScreen()}
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

	// Ask the terminal to report modified keys, so Shift+Enter arrives as
	// something other than a bare carriage return (S-134, chat/newline.go).
	// A terminal that does not know the request ignores it, and the draft
	// keeps Alt+Enter and Ctrl+J.
	restoreKeys := chat.RequestEnhancedKeys(os.Stdout)
	defer restoreKeys()

	// And ask it to stop turning the wheel into arrow keys, which is what
	// alternate scroll does to a full-screen program on most terminals — and
	// what put hundreds of synthetic Up/Down presses into the draft
	// (chat/altscroll.go). The setting is saved and restored, so a terminal
	// that had it on keeps it after we exit.
	restoreScroll := chat.SuppressAlternateScroll(os.Stdout)
	defer restoreScroll()

	program := tea.NewProgram(model, programOpts...)
	if _, err := program.Run(); err != nil {
		// os.Exit skips the deferred restore, so the terminal is put back
		// here rather than left reporting modified keys to the next program.
		restoreKeys()
		restoreScroll()
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "Chat session ended.")
	return nil
}

// gitSnapshot captures the workspace's git state for rewind checkpoints
// (S-069), so /rewind can report what diverged since a checkpoint.
func gitSnapshot() chat.GitSnapshot {
	fp := quality.TakeFingerprint(".")
	return chat.GitSnapshot{Repo: fp.Repo, Head: fp.Head, StatusHash: fp.StatusHash, DirtyPaths: fp.DirtyPaths}
}

// estimateToolDefTokens roughly estimates the context cost of the registered
// tool definitions, for /stats' occupancy breakdown (S-065).
func estimateToolDefTokens(defs []provider.Tool) int64 {
	b, err := json.Marshal(defs)
	if err != nil {
		return 0
	}
	return agent.EstimateTokens(string(b))
}

// pickSavedChat shows the saved-chat picker and returns the chosen session
// name, or "" if the user backed out.
func pickSavedChat(db *storage.DB) (string, error) {
	entries, err := db.ListChats()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No saved chats yet.")
		return "", nil
	}

	items := make([]browse.Item, len(entries))
	for i, e := range entries {
		items[i] = browse.Item{
			ID:      e.Name,
			Title:   e.Name,
			Preview: fmt.Sprintf("%d turns, %s", e.Turns, e.UpdatedAt.Local().Format("Jan 2 15:04")),
			Detail: fmt.Sprintf("Name:     %s\nTurns:    %d\nUpdated:  %s",
				e.Name, e.Turns, e.UpdatedAt.Local().Format("2006-01-02 15:04:05")),
		}
	}

	model := browse.New(items, []browse.ActionDef{{Label: "Open", Shortcut: "o"}})
	p := tea.NewProgram(model, tea.WithAltScreen())
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
