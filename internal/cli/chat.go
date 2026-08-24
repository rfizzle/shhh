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
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
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
	// gate registers the quality-gate tool and /gate command (S-067);
	// `shhh code` only.
	gate bool
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
}

func newChatCmd() *cobra.Command {
	var flags resolve.Opts
	var continueLast bool
	var resumePick bool

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
			})
		},
	}

	cmd.Flags().BoolVarP(&continueLast, "continue", "c", false, "resume the most recent chat session")
	cmd.Flags().BoolVarP(&resumePick, "resume", "r", false, "pick a saved chat to resume")
	cmd.Flags().StringVar(&flags.FlagProvider, "provider", "", "LLM provider")
	cmd.Flags().StringVar(&flags.FlagModel, "model", "", "model name to use")
	cmd.Flags().StringVar(&flags.FlagAPIKey, "api-key", "", "API key (overrides env var)")

	return cmd
}

// sessionEnv is the provider-and-prompt setup shared by the interactive chat
// TUI and headless print mode: resolved model, initial messages, and a stream
// closure over the session's provider.
type sessionEnv struct {
	cfg         config.Config
	prov        provider.Provider
	modelName   string
	sysPrompt   string
	messages    []provider.Message
	stream      agent.StreamFunc
	switchModel func(string)
}

func buildSessionEnv(cmd *cobra.Command, session chatSession) (*sessionEnv, error) {
	cfg := ConfigFrom(cmd.Context())

	flags := session.flags
	flags.ConfigProvider = cfg.Provider.Default
	flags.ConfigModel = cfg.Provider.Model

	resolved := resolve.Resolve(*flags)

	p, err := provider.Resolve(resolved.Provider, provider.ResolveOpts{
		APIKey:        flags.FlagAPIKey,
		Model:         resolved.Model,
		ConfigAPIKey:  cfg.ProviderAPIKey(),
		ConfigBaseURL: cfg.ProviderBaseURL(),
		ConfigName:    cfg.ProviderDisplayName(),
	})
	if err != nil {
		return nil, err
	}

	info := shell.Detect()
	promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra, project.FindContext(), session.promptExtra)
	sysPrompt := session.buildPrompt(info, promptExtra)

	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: sysPrompt},
	}

	compOpts := provider.CompletionOpts{
		Model:      resolved.Model,
		Tools:      session.toolDefs,
		ToolChoice: "auto",
	}

	// /model switches this mid-session; the stream closure runs in a
	// background goroutine, so guard the read.
	var modelMu sync.Mutex
	currentModel := resolved.Model

	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ctx, cancel := context.WithCancel(cmd.Context())
		opts := compOpts
		modelMu.Lock()
		opts.Model = currentModel
		modelMu.Unlock()
		ev, sErr := p.StreamCompletion(ctx, msgs, opts)
		if sErr != nil {
			cancel()
			return nil, nil, sErr
		}
		return ev, cancel, nil
	}

	return &sessionEnv{
		cfg:       cfg,
		prov:      p,
		modelName: resolved.Model,
		sysPrompt: sysPrompt,
		messages:  messages,
		stream:    stream,
		switchModel: func(name string) {
			modelMu.Lock()
			currentModel = name
			modelMu.Unlock()
		},
	}, nil
}

func runChatSession(cmd *cobra.Command, args []string, session chatSession) error {
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
	// Quality gate (S-067): the model can run the project's own checks by
	// suite name; command text only ever comes from trusted config.
	var gate *quality.Runner
	if session.gate {
		gate = openQualityGate(ConfigFrom(cmd.Context()), red)
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

	env, err := buildSessionEnv(cmd, session)
	if err != nil {
		return err
	}
	cfg := env.cfg

	prices, _ := pricing.Load()

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
	containment, err := buildContainment(cfg)
	if err != nil {
		return err
	}

	baseExecutor := agent.ToolExecutor(tools.Execute)
	if session.web != nil {
		baseExecutor = session.web.WrapExecutor(tools.Execute)
	}
	if gate != nil {
		baseExecutor = gate.WrapExecutor(baseExecutor)
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
		sup = buildSupervisor(cmd.Context(), cfg, session, env, red, recorder, db, prices)
		executor = sup.WrapExecutor(executor)
		defer sup.Close()
	}

	model := chat.New(env.messages, env.stream).
		WithTitle(session.title).
		WithObserver(recorder.observer()).
		WithToolTokenEstimate(estimateToolDefTokens(session.toolDefs)).
		WithToolExecutor(executor).
		WithDB(db).
		WithPricing(prices, env.modelName).
		WithRunner(runner.RunCapture).
		WithContainment(containment).
		WithMaxToolRounds(cfg.Behavior.MaxToolRounds).
		WithCommandAllowlist(cfg.Behavior.CommandAllowlist).
		WithApprovalMode(mode, cycle).
		WithClassifier(classifier).
		WithModelSwitcher(env.switchModel).
		WithGitSnapshots(gitSnapshot)
	if red != nil {
		model = model.WithEvidence(chat.Evidence{Reduce: red.Process, Manage: evidenceManager(red)})
	}
	if gate != nil {
		model = model.WithGate(chat.Gate{Manage: gateManager(gate)})
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
			return chat.GatedPreview{Action: "fetch", Summary: summary}, nil
		}
	}
	if sup != nil {
		gatedPreviews[subagent.SpawnToolName] = func(args json.RawMessage) (chat.GatedPreview, error) {
			summary, err := subagent.SpawnSummary(args)
			if err != nil {
				return chat.GatedPreview{}, err
			}
			return chat.GatedPreview{Action: "spawn", Summary: summary}, nil
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

	program := tea.NewProgram(model, programOpts...)
	if _, err := program.Run(); err != nil {
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
