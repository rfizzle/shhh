package cli

// `shhh cmd`: one prompt, one command, one decision — the smallest of the
// four sizes and the one the product is named for.
// See docs/product.md#the-four-sizes.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/stdin"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui"
	"github.com/spf13/cobra"
)

// oneShotOutcome is how the one-shot's single turn ended, in the closed set
// every surface's turns end in. A command the user walked away from is
// cancelled and not done: the request was answered, and the answer was
// refused, and an outcome mix that could not tell those apart is the one
// figure this record exists to make readable.
func oneShotOutcome(r ui.GenerateResult) string {
	switch {
	case r.Err != nil:
		return observe.TurnFailed
	case r.Cancelled || r.Action == ui.ActionCancel:
		return observe.TurnCancelled
	}
	return observe.TurnDone
}

// runAction reports whether an action from the result surface is one that
// executes the command rather than showing, editing or saving it.
func runAction(a ui.Action) bool {
	return a == ui.ActionRun || a == ui.ActionRunAll || a == ui.ActionRunStep
}

// pendingRecord is the store and the session row, opened away from the path
// to the first token. Opening the store is a SQLite connection, a schema
// check and a history purge, and none of that is needed until there is
// something to write down — while the token is needed the moment the process
// starts. What does not move is the record itself: the row is still opened
// and stamped for every run, the piped one included, because every path that
// writes anything goes through wait first.
// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
type pendingRecord struct {
	done chan struct{}
	db   *storage.DB
	rec  *observeRecorder
}

// startRecord runs open on a goroutine of its own. open is everything the
// record costs: the store, the row and the stamp on it.
func startRecord(open func() (*storage.DB, *observeRecorder)) *pendingRecord {
	p := &pendingRecord{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.db, p.rec = open()
	}()
	return p
}

// wait blocks until the store has answered and hands back what it opened.
// Closing done is what publishes the two fields to the caller's goroutine,
// so every reader has to come through here — reading a field beside it is a
// race, and the race detector is the only thing that would ever say so.
func (p *pendingRecord) wait() (*storage.DB, *observeRecorder) {
	<-p.done
	return p.db, p.rec
}

// newCmdCmd is the one-shot: a prompt in, a command on screen, and a row of
// keys that decide what happens to it. With no terminal on the other end —
// piped, scripted, in CI — it drops every piece of chrome and writes the bare
// command to stdout instead, which is what `--raw` forces when there is one.
// See docs/capabilities/generation.md.
func newCmdCmd() *cobra.Command {
	var flags resolve.Opts
	var rawMode bool
	var explainMode bool
	var silentMode bool

	cmd := &cobra.Command{
		Use:   "cmd [prompt]",
		Short: "Generate one shell command",
		Long:  "Turn a prompt into a single shell command, shown with what it does and a row of keys — run it, edit it, ask for another, copy it, save it. Nothing runs until you say so.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The model-data table is a file read, a parse of it, and once a
			// day a download, and none of it depends on anything the user
			// typed. It is asked for here and collected below, so it runs
			// beside the piped stdin, the flag resolution and the provider's
			// own rather than in front of them. It cannot be moved past the
			// request: the table is where a model's reasoning ladder and its
			// output cap come from, so the shape of what goes out depends on
			// it.
			// See docs/capabilities/generation.md#the-first-token-waits-for-the-request-and-nothing-else.
			priced := make(chan *pricing.Table, 1)
			go func() { priced <- loadPricing() }()

			cfg := ConfigFrom(cmd.Context())
			// The config half of a resolution, the way every other command
			// that reaches a provider fills it in (session.go): the root
			// carries no model flags to fill in for anyone now.
			// The one-shot reads its own model key ahead of provider.model:
			// the command a person wants back in a second is not the work
			// the coding agent's model was chosen for.
			fillConfigHalf(&flags, cfg, config.SurfaceCmd)

			userPrompt, pipeMode, ok, err := readOneShotInput(cmd, args, cfg.EffectiveContextMaxTokens()*4, rawMode)
			if !ok {
				return err
			}

			run, err := openOneShot(cmd, cfg, flags, userPrompt, pipeMode, priced)
			if err != nil {
				return err
			}
			// The store is opened by openOneShot but outlives it: it is let
			// go of here, after the row in it has been closed, so it is
			// deferred first: defers run in reverse.
			defer func() {
				if db, _ := run.pending.wait(); db != nil {
					db.Close()
				}
			}()
			defer run.finish()

			if pipeMode {
				return run.runPiped()
			}

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: run.sysPrompt},
				{Role: provider.RoleUser, Content: userPrompt},
			}

			compOpts := provider.CompletionOpts{Model: run.resolved.Model, Effort: run.effort}
			p := run.p

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			events, err := p.StreamCompletion(ctx, messages, compOpts)
			if err != nil {
				run.outcome = observe.TurnFailed
				return reportFailure(err, run.resolved.Model)
			}

			var metrics *storage.StreamMetrics
			events, metrics = storage.InstrumentStream(events)

			newStream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				sCtx, sCancel := context.WithCancel(cmd.Context())
				ev, sErr := p.StreamCompletion(sCtx, msgs, compOpts)
				if sErr != nil {
					sCancel()
					return nil, nil, sErr
				}
				return ev, sCancel, nil
			}

			newExplain := func(command string, long bool) (<-chan provider.StreamEvent, context.CancelFunc, error) {
				eCtx, eCancel := context.WithCancel(cmd.Context())
				eMsgs := []provider.Message{
					{Role: provider.RoleSystem, Content: prompt.BuildExplain(long)},
					{Role: provider.RoleUser, Content: command},
				}
				ev, eErr := p.StreamCompletion(eCtx, eMsgs, compOpts)
				if eErr != nil {
					eCancel()
					return nil, nil, eErr
				}
				return ev, eCancel, nil
			}

			// The explanation is on by default: a command you do not
			// understand is a command you should not run. `-e` buys the long
			// form rather than the only form, and silent mode still
			// suppresses both.
			//
			// The brief form usually arrives inside the generation itself,
			// so this stream is what answers `-e`, `[x]`, and a response
			// that came back without one.
			explain := ui.ExplainBrief
			switch {
			case silentMode || cfg.Behavior.SilentMode:
				explain = ui.ExplainNone
			case explainMode:
				explain = ui.ExplainLong
			}
			model := ui.NewGenerateModel(events, cancel, messages, newStream, newExplain, run.info.Shell).WithExplain(explain)
			program := newProgram(model)
			finalModel, err := program.Run()
			if err != nil {
				run.outcome = observe.TurnFailed
				return err
			}

			return run.act(finalModel.(ui.GenerateModel).Result(), metrics)
		},
	}

	// Declaration order is reading order: the flags that shape the one-shot
	// first, then the ones that name a model. Sorting would interleave them,
	// and a `--api-key` above `--explain` says nothing about either.
	cmd.Flags().SortFlags = false
	cmd.Flags().BoolVarP(&explainMode, "explain", "e", false, "explain the generated command at length (one line is shown by default)")
	cmd.Flags().BoolVarP(&silentMode, "silent", "s", false, "suppress explanation output")
	// fang draws one FLAGS section, so the break between the two kinds is a
	// blank line hung off the last one-shot flag's description. A list you
	// scan needs an axis, and this list has two halves.
	// See docs/interface/surfaces.md#outside-the-tui.
	cmd.Flags().BoolVar(&rawMode, "raw", false, "force pipe mode: raw command output, no TUI\n")
	addModelFlags(cmd, &flags)

	return cmd
}

// readOneShotInput settles the prompt and whether the run is piped, from
// stdin, the arguments and --raw. ok is false when the run ends here: err is
// then either the failure or what printing the help returned.
func readOneShotInput(cmd *cobra.Command, args []string, maxChars int, rawMode bool) (userPrompt string, pipeMode, ok bool, err error) {
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())

	switch {
	case !stdinIsTTY && len(args) > 0:
		stdinContent, err := stdin.Read(os.Stdin, maxChars)
		if err != nil {
			return "", false, false, err
		}
		userPrompt = strings.Join(args, " ")
		if stdinContent != "" {
			userPrompt = stdin.FormatPromptWithContext(userPrompt, stdinContent)
		}
		pipeMode = rawMode
	case !stdinIsTTY && len(args) == 0:
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return "", false, false, fmt.Errorf("reading stdin: %w", err)
		}
		userPrompt = strings.TrimSpace(strings.Join(lines, "\n"))
		if userPrompt == "" {
			return "", false, false, fmt.Errorf("no prompt provided on stdin")
		}
		pipeMode = true
	case len(args) > 0:
		userPrompt = strings.Join(args, " ")
		pipeMode = rawMode
	default:
		return "", false, false, cmd.Help()
	}
	return userPrompt, pipeMode, true, nil
}

// oneShotRun is one one-shot past its input: the provider it asks, the ledger
// that counts what it spends, and the record it is written down in.
type oneShotRun struct {
	cmd         *cobra.Command
	cfg         config.Config
	resolved    resolve.Resolved
	p           provider.Provider
	info        shell.Info
	promptExtra string
	sysPrompt   string
	userPrompt  string
	effort      provider.Effort
	ledger      *meter.Ledger
	pending     *pendingRecord
	timer       *oneShotTimer
	outcome     string
	closed      bool
}

// openOneShot opens the provider, the ledger and the record. The store it
// starts opening is the caller's to close.
func openOneShot(cmd *cobra.Command, cfg config.Config, flags resolve.Opts, userPrompt string, pipeMode bool, priced <-chan *pricing.Table) (*oneShotRun, error) {
	resolved := resolve.Resolve(flags)

	// A session with no provider gets the card that says where shhh
	// looked, not the dialect's own one-line complaint.
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
	promptExtra := prompt.CombineExtra(cfg.Behavior.SystemPromptExtra,
		project.InstructionBlock(project.Instructions(info.Cwd, userInstructionsPath()), prompt.InstructionBudget))

	// The prompt is settled ahead of the branch below because the row
	// is stamped with the one that actually went out, and the row is
	// now opened beside the request rather than in front of it.
	//
	// A one-shot runs under a reasoning level and a config, and
	// nothing else on the list: no mode, no cap, no readings, no
	// classifier, no containment. The piped run sends no reasoning
	// field at all, whatever the flag said, so it is stamped off —
	// the record keeps what was in force, not what was asked for.
	var sysPrompt string
	var effort provider.Effort
	if pipeMode {
		sysPrompt = raw.SystemPrompt(info, promptExtra)
	} else {
		// The interactive one-shot asks for what its surface shows
		// beside the command — the sentence saying what it does and
		// the alternatives it was picked over. The pipe path goes out
		// through prompt.Build and asks for neither, so its stdout is
		// one command, as it has always been.
		sysPrompt = prompt.BuildAlternatives(info, promptExtra)
		// A refused level is a refused flag, and it lands here rather
		// than at the record below on purpose: nothing was asked of
		// anybody and nothing was spent, so there is no run to write
		// down. The row this used to leave said a one-shot completed
		// when what happened was that a word on the command line was
		// not one of six.
		if effort, err = provider.ParseEffort(resolved.Reasoning); err != nil {
			return nil, err
		}
	}
	settings := sessionSettings(cfg, runSettings{effort: effort})

	// The one-shot spends on more than the command it prints: a
	// revision, an explanation and the description written for a
	// saved snippet are all requests too. Gating the provider once,
	// here, is what stops those being free in the record — the
	// alternative is remembering to instrument each of them, and the
	// explanation was already being missed.
	// See docs/architecture.md#spend-is-counted-at-the-provider.
	prices := <-priced
	ledger := meter.New(prices)
	ledger.SetBudget(spendBudget(cfg))
	p = meter.WithFallbackModel(ledger.For(p, meter.SourceOneShot), resolved.Model)
	// And timed, so the turn's row says where the time went: every request
	// the interaction makes marks the clock as it streams (cmdtime.go).
	timer := &oneShotTimer{answered: !pipeMode}
	p = timedProvider{Provider: p, timer: timer}

	// The one-shot is one request, so it is one turn — and recording
	// it as a single-turn session is what lets it join every
	// aggregate without any of them learning what a one-shot is. Its
	// rounds are zero and its tool mix is empty, and both are true
	// rather than missing. The `requests` row the interactive path
	// also writes answers a different question (one prompt, one
	// command, what became of it) and is unchanged.
	//
	// The row is opened above the pipe branch on purpose. A piped
	// one-shot is the composition with nobody in front of it, which
	// is exactly the one the record is least able to do without —
	// and it is the one that until now spent money and left nothing
	// behind at all.
	// See docs/capabilities/sessions-and-memory.md#every-composition-is-one-population.
	pending := startRecord(func() (*storage.DB, *observeRecorder) {
		db, _ := openSessionStore(cmd.Context())
		rec := startObserveRecorder(db, "cmd", p.Name(), resolved.Model, prices)
		// The phases paid before this row existed — the configuration and
		// the store — are written to it now. A one-shot draws no prompt of
		// its own, so there is no first paint to add (startup.go).
		startupFrom(cmd.Context()).Attach(rec.startupRow)
		rec.stamp(sysPrompt, 0, projectFingerprintRoot(), settings)
		return db, rec
	})

	// The turn starts here, where its row's duration always has.
	timer.begin()
	return &oneShotRun{
		cmd:         cmd,
		cfg:         cfg,
		resolved:    resolved,
		p:           p,
		info:        info,
		promptExtra: promptExtra,
		sysPrompt:   sysPrompt,
		userPrompt:  userPrompt,
		effort:      effort,
		ledger:      ledger,
		pending:     pending,
		timer:       timer,
		outcome:     observe.TurnDone,
	}, nil
}

// finish closes the row. Closing the row is a call and not only a defer:
// every action that runs the generated command ends the process, and os.Exit
// does not run deferred functions. It is idempotent, so the deferred call is
// the ordinary path and the explicit ones are the exits. Waiting for the
// store is the first thing it does — the row has to exist before it can be
// closed, and a run that ends before the store answered is still a run that
// happened.
func (r *oneShotRun) finish() {
	if r.closed {
		return
	}
	r.closed = true
	_, recorder := r.pending.wait()
	if recorder == nil {
		return
	}
	t := r.ledger.Total()
	recorder.usagePriced(1, t.In, t.Out, t.Cost, t.Priced)
	took, split := r.timer.span()
	recorder.turnTimed(1, 0, took, r.outcome, split)
	recorder.end()
}

// runPiped is the run with nobody in front of it: the bare command on
// stdout, and a failure that ends the process.
func (r *oneShotRun) runPiped() error {
	err := raw.Run(r.cmd.Context(), raw.Opts{
		Provider:          r.p,
		Model:             r.resolved.Model,
		Prompt:            r.userPrompt,
		SystemPromptExtra: r.promptExtra,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	})
	if err != nil {
		// Piped output has no chrome by contract, so the failure
		// arrives as one classified line rather than as a row.
		if line, ok := ui.FailureLine(err); ok {
			fmt.Fprintln(os.Stderr, line)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		r.outcome = observe.TurnFailed
		r.finish()
		os.Exit(1)
	}
	return nil
}

// oneShotActionNames is the word each result action is recorded under.
var oneShotActionNames = map[ui.Action]string{
	ui.ActionRun:     "run",
	ui.ActionRunAll:  "run-all",
	ui.ActionRunStep: "run-step",
	ui.ActionCopy:    "copy",
	ui.ActionRevise:  "revise",
	ui.ActionEdit:    "edit",
	ui.ActionSave:    "save",
	ui.ActionCancel:  "cancel",
}

// act carries out what the result surface decided: it records the request,
// refuses what may not run, asks what was not yet asked, and then runs,
// saves or copies the command.
func (r *oneShotRun) act(result ui.GenerateResult, metrics *storage.StreamMetrics) error {
	r.outcome = oneShotOutcome(result)

	// Everything below writes, so this is where the store is caught
	// up with. It has had the whole interaction to open.
	db, _ := r.pending.wait()

	var requestID int64
	if db != nil {
		requestID, _ = db.RecordRequest(storage.RequestRecord{
			Provider: r.p.Name(),
			Model:    r.resolved.Model,
			Prompt:   r.userPrompt,
			Command:  result.Command,
			Action:   oneShotActionNames[result.Action],
			TTFT:     metrics.TTFT,
			Duration: metrics.Duration,
			// Timing belongs to the first request; the tokens are
			// every request the interaction made — revisions and
			// explanations included — because that is what the user
			// paid to get this command.
			TokensIn:  ledgerTokens(r.ledger.Total().In),
			TokensOut: ledgerTokens(r.ledger.Total().Out),
			Success:   metrics.Success,
		})
	}

	if result.Err != nil {
		// Classified, never raw: the one-shot renders the
		// same failure row the session does, with the way out stated as
		// a command rather than as a key nothing is listening for.
		return reportFailure(result.Err, r.resolved.Model)
	}

	if r.refused(result) || !r.confirmed(result) {
		return nil
	}

	r.timer.working()
	r.perform(result, db, requestID)

	// Saving a snippet writes a description, and a save with no
	// sentence to reuse buys it with a request of its own. It lands
	// after the row above, so the row is revised rather than left
	// understating the interaction.
	if db != nil && requestID != 0 {
		total := r.ledger.Total()
		_ = db.UpdateRequestTokens(requestID, ledgerTokens(total.In), ledgerTokens(total.Out))
	}

	return nil
}

// refused reports whether a command the result surface would run is one no
// key may run, saying so on stderr.
func (r *oneShotRun) refused(result ui.GenerateResult) bool {
	// The command on the screen is the model's, not the reader's,
	// so the deny list answers for it here as it does at a card:
	// the point of the list is that this command never becomes a
	// decision, and a key pressed on it is not the answer to one.
	if runAction(result.Action) && agent.DenylistMatches(r.cfg.Behavior.CommandDenylist, result.Command) {
		fmt.Fprintln(os.Stderr, "\n⊘ Refused — this command is on the deny list in your configuration; it was not run.")
		// The request was answered and the answer was refused,
		// which is the bucket a cancel lands in: an outcome mix
		// that could not tell a refusal from a completed run is
		// the one figure this record exists to make readable.
		r.outcome = observe.TurnCancelled
		return true
	}
	// And for the same reason a command pointed at something no
	// proposed command may destroy: the key is not the answer to a
	// decision, and the reader's way through is to type it
	// themselves.
	// See docs/capabilities/approvals-and-safety.md#some-targets-are-never-destroyed.
	if runAction(result.Action) {
		if what := ruleAction(nil, result.Command, true).Irreplaceable; what != "" {
			fmt.Fprintln(os.Stderr, "\n⊘ Refused — this command destroys "+what+
				", which no command shhh proposes may destroy; it was not run. Type it yourself if you mean it.")
			r.outcome = observe.TurnCancelled
			return true
		}
	}
	return false
}

// confirmed reports whether the run may go ahead past the safety prompt,
// asking it only when nothing has asked it yet.
func (r *oneShotRun) confirmed(result ui.GenerateResult) bool {
	// The result surface already moves the safe default on a
	// destructive command and takes a deliberate `y` for it,
	// so asking the same question again here is a second prompt for
	// one decision. It still runs for anything that reached this
	// point without being asked.
	if !r.cfg.SafetyWarningsEnabled() || result.Confirmed || !runAction(result.Action) {
		return true
	}
	warnings := safety.Check(result.Command)
	if len(warnings) == 0 {
		return true
	}
	fmt.Fprintln(os.Stderr, "\n⚠ Safety warning:")
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  ⚠ %s\n", w.Risk)
	}
	fmt.Fprint(os.Stderr, "\nProceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" && input != "yes" {
		fmt.Fprintln(os.Stderr, "Aborted.")
		// Refusing the safety prompt is the same act as
		// pressing esc on the card, so it is the same
		// outcome — two spellings of "the user refused"
		// landing in two buckets would make either one
		// unreadable.
		r.outcome = observe.TurnCancelled
		return false
	}
	return true
}

// perform runs, saves or copies the command. A run that exits non-zero ends
// the process with that code, after the row is closed.
func (r *oneShotRun) perform(result ui.GenerateResult, db *storage.DB, requestID int64) {
	switch result.Action {
	case ui.ActionRun:
		code := runner.Run(result.Command)
		recordExitCode(db, requestID, code)
		r.finish()
		os.Exit(code)
	case ui.ActionRunAll:
		cmds := ui.SplitCommands(result.Command)
		for _, c := range cmds {
			code := runner.Run(c)
			if code != 0 {
				recordExitCode(db, requestID, code)
				r.finish()
				os.Exit(code)
			}
		}
		recordExitCode(db, requestID, 0)
	case ui.ActionRunStep:
		cmds := ui.SplitCommands(result.Command)
		reader := bufio.NewReader(os.Stdin)
		for i, c := range cmds {
			fmt.Fprintf(os.Stderr, "Step %d/%d: %s\n", i+1, len(cmds), c)
			fmt.Fprint(os.Stderr, "Run? [Y/n] ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input == "n" || input == "no" {
				fmt.Fprintln(os.Stderr, "Skipped remaining steps.")
				break
			}
			code := runner.Run(c)
			if code != 0 {
				fmt.Fprintf(os.Stderr, "Step %d exited with code %d. Stop.\n", i+1, code)
				recordExitCode(db, requestID, code)
				r.finish()
				os.Exit(code)
			}
		}
		recordExitCode(db, requestID, 0)
	case ui.ActionSave:
		if db != nil && result.SaveName != "" {
			if err := db.SaveSnippet(result.SaveName, result.Command); err != nil {
				fmt.Fprintf(os.Stderr, "Error saving snippet: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "Saved snippet %q.\n", result.SaveName)
				if desc := snippetDescription(r.cmd.Context(), r.p, r.resolved.Model, resolveFlow(r.cfg, flowDescription, r.p.Name(), r.resolved.Model).model, result.Command, result.Explanation); desc != "" {
					_ = db.UpdateSnippetDescription(result.SaveName, desc)
					fmt.Fprintf(os.Stderr, "Description: %s\n", desc)
				}
			}
		}
	case ui.ActionCopy:
		cr := clipboard.Copy(result.Command)
		if cr.Warning != "" {
			fmt.Fprintln(os.Stderr, cr.Warning)
		} else {
			fmt.Fprintln(os.Stderr, "Copied to clipboard.")
		}
	}
}

// recordExitCode writes the run's exit code against its request row, when
// there is a row to write it on.
func recordExitCode(db *storage.DB, requestID int64, code int) {
	if db != nil && requestID > 0 {
		_ = db.RecordExitCode(requestID, code)
	}
}
