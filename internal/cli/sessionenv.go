package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/spf13/cobra"
)

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
	// model reads the session's model as it is now, which /model and a
	// provider switch move; modelName is the one it opened on.
	model func() string
	// providerNow reads the provider the session is on as it is now, which
	// a provider switch moves; provName is the one it opened on.
	providerNow func() string
	// endpointModels is the opening provider's own model list, nil where it
	// cannot list one: the picker and the spawn's model check share it.
	endpointModels *endpointModels
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
	// flows holds the models the config screen took for this session alone
	// (summarizer.go). Its zero value holds none, which is every surface but
	// the interactive session.
	flows flowOverrides
	// flowsMoved is told when one of them changes, so the record can say
	// what the rest of the session is asked on. Nil tells nobody.
	flowsMoved func()
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
	block, dropped := project.InstructionBlockCut(instructions, prompt.InstructionBudget)
	// What the cut dropped and what the files name that is gone ride on the
	// survey too, for the screen: the count is this block's, so the screen
	// cannot state a cut the prompt did not make.
	survey.Instruction = project.CheckInstructions(instructions, survey.Root, dropped)
	extra := prompt.CombineExtra(configExtra, block, project.PromptBlock(survey), trustPromptBlock(projectTrust(), s.conversation), s.promptExtra)
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

// currentModel is the session's model as it is now, or the one it opened on
// where the session was assembled without the live reading.
func (e *sessionEnv) currentModel() string {
	if e.model == nil {
		return e.modelName
	}
	return e.model()
}

// childModel is the session layer a child's model resolves to. Children are
// bound to the provider the session opened on, so it is the current model
// only while the session is still on that provider; after a provider switch
// it is the opening model, which that provider can run.
func (e *sessionEnv) childModel() string {
	if e.providerNow != nil && e.providerNow() != e.provName {
		return e.modelName
	}
	return e.currentModel()
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
	ledger.SetBudget(spendBudget(cfg))

	// What the checkout was not allowed to put into this session, or what
	// changed in a checkout trusted before, said once before it starts. Both
	// the interactive and the headless session come through here, and the
	// headless one has no screen to read it off later. The re-stamp is what
	// makes it once: the next session reads the checkout as this one found it
	// (trust.go).
	if note := trustStartupNote(); note != "" {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: note})
	}
	restampProjectTrust()
	// And the tools the checkout declared that its commands will not find,
	// for the same reason and to both kinds of session: the headless one is
	// the one that cannot be offered the install, and says so.
	if note := session.toolchainNote(); note != "" {
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: note})
	}

	flags := session.flags
	// A session reads the model key of the command it is — `chat` or
	// `code` — ahead of provider.model, and so does its `--print` run and a
	// served one. A backlog run's stages are these commands, so each stage
	// takes the key of the command it was started as.
	fillConfigHalf(flags, cfg, session.kind)

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
			APIKey:            key,
			Model:             model,
			BaseURL:           baseURL,
			ConfigAPIKey:      cfg.ProviderAPIKey(),
			ConfigBaseURL:     cfg.ProviderBaseURL(),
			ConfigName:        cfg.ProviderDisplayName(),
			CacheTTL:          cfg.ProviderCacheTTL(),
			StreamIdleSeconds: cfg.ProviderStreamIdle(),
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
		model: func() string {
			sessionMu.Lock()
			defer sessionMu.Unlock()
			return currentModel
		},
		providerNow: func() string {
			sessionMu.Lock()
			defer sessionMu.Unlock()
			return currentProvider
		},
		endpointModels: newEndpointModels(modelListerFor(p)),
		effort:         effort,
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

func spendBudget(cfg config.Config) meter.Budget {
	budget := meter.Budget{
		WarningCents: int64(cfg.Provider.CostWarningCents),
		CapCents:     int64(cfg.Provider.CostCapCents),
	}
	if budget.WarningCents < 0 {
		budget.WarningCents = 0
	}
	if budget.CapCents < 0 {
		budget.CapCents = 0
	}
	return budget
}

// askToolDefs is the toolset with the question tool on it where there is
// somebody to answer one, and unchanged where there is not. It is a function
// of its own because "where" is the whole decision: a run with no reader
// never sees the tool, is never offered it and is never refused it, which is
// a registration decision rather than a gate decision — a tool the run can
// only be refused is worse than one it never saw
// (docs/capabilities/coding-agent.md#nobody-to-ask).
func askToolDefs(session chatSession) []provider.Tool {
	if !session.ask {
		return session.toolDefs
	}
	return append(append([]provider.Tool{}, session.toolDefs...), ask.ToolDefinition())
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
