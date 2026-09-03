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
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rfizzle/shhh/internal/clipboard"
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

			stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())

			cfg := ConfigFrom(cmd.Context())
			// The config half of a resolution, the way every other command
			// that reaches a provider fills it in (session.go): the root
			// carries no model flags to fill in for anyone now.
			flags.ConfigProvider = cfg.Provider.Default
			flags.ConfigModel = cfg.Provider.Model
			flags.ConfigReasoning = cfg.Provider.Reasoning

			maxChars := cfg.EffectiveContextMaxTokens() * 4

			var userPrompt string
			var pipeMode bool

			switch {
			case !stdinIsTTY && len(args) > 0:
				stdinContent, err := stdin.Read(os.Stdin, maxChars)
				if err != nil {
					return err
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
					return fmt.Errorf("reading stdin: %w", err)
				}
				userPrompt = strings.TrimSpace(strings.Join(lines, "\n"))
				if userPrompt == "" {
					return fmt.Errorf("no prompt provided on stdin")
				}
				pipeMode = true
			case len(args) > 0:
				userPrompt = strings.Join(args, " ")
				pipeMode = rawMode
			default:
				return cmd.Help()
			}

			resolved := resolve.Resolve(flags)

			// A session with no provider gets the card that says where shhh
			// looked, not the dialect's own one-line complaint.
			p, req, err := resolveProvider(cmd.Context(), cfg, providerRequest{
				Provider: resolved.Provider,
				Model:    resolved.Model,
				APIKey:   flags.FlagAPIKey,
			})
			if err != nil {
				return err
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
					return err
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
			p = meter.WithFallbackModel(ledger.For(p, meter.SourceOneShot), resolved.Model)

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
				db, _ := openStore()
				rec := startObserveRecorder(db, "cmd", p.Name(), resolved.Model, prices)
				rec.stamp(sysPrompt, 0, projectFingerprintRoot(), settings)
				return db, rec
			})
			// The store is let go of after the row in it has been closed, so
			// it is deferred first: defers run in reverse.
			defer func() {
				if db, _ := pending.wait(); db != nil {
					db.Close()
				}
			}()

			started := time.Now()
			outcome := observe.TurnDone
			// Closing the row is a call and not only a defer: every action
			// that runs the generated command ends the process, and os.Exit
			// does not run deferred functions. It is idempotent, so the
			// deferred call is the ordinary path and the explicit ones are
			// the exits. Waiting for the store is the first thing it does —
			// the row has to exist before it can be closed, and a run that
			// ends before the store answered is still a run that happened.
			closed := false
			finish := func() {
				if closed {
					return
				}
				closed = true
				_, recorder := pending.wait()
				if recorder == nil {
					return
				}
				t := ledger.Total()
				recorder.usagePriced(1, t.In, t.Out, t.Cost, t.Priced)
				recorder.turn(1, 0, time.Since(started), outcome)
				recorder.end()
			}
			defer finish()

			if pipeMode {
				err := raw.Run(cmd.Context(), raw.Opts{
					Provider:          p,
					Model:             resolved.Model,
					Prompt:            userPrompt,
					SystemPromptExtra: promptExtra,
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
					outcome = observe.TurnFailed
					finish()
					os.Exit(1)
				}
				return nil
			}

			messages := []provider.Message{
				{Role: provider.RoleSystem, Content: sysPrompt},
				{Role: provider.RoleUser, Content: userPrompt},
			}

			compOpts := provider.CompletionOpts{Model: resolved.Model, Effort: effort}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			events, err := p.StreamCompletion(ctx, messages, compOpts)
			if err != nil {
				outcome = observe.TurnFailed
				return reportFailure(err, resolved.Model)
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
			model := ui.NewGenerateModel(events, cancel, messages, newStream, newExplain, info.Shell).WithExplain(explain)
			program := newProgram(model)
			finalModel, err := program.Run()
			if err != nil {
				outcome = observe.TurnFailed
				return err
			}

			result := finalModel.(ui.GenerateModel).Result()
			outcome = oneShotOutcome(result)

			// Everything below writes, so this is where the store is caught
			// up with. It has had the whole interaction to open.
			db, _ := pending.wait()

			var requestID int64
			if db != nil {
				actionName := ""
				switch result.Action {
				case ui.ActionRun:
					actionName = "run"
				case ui.ActionRunAll:
					actionName = "run-all"
				case ui.ActionRunStep:
					actionName = "run-step"
				case ui.ActionCopy:
					actionName = "copy"
				case ui.ActionRevise:
					actionName = "revise"
				case ui.ActionEdit:
					actionName = "edit"
				case ui.ActionSave:
					actionName = "save"
				case ui.ActionCancel:
					actionName = "cancel"
				}
				requestID, _ = db.RecordRequest(storage.RequestRecord{
					Provider: p.Name(),
					Model:    resolved.Model,
					Prompt:   userPrompt,
					Command:  result.Command,
					Action:   actionName,
					TTFT:     metrics.TTFT,
					Duration: metrics.Duration,
					// Timing belongs to the first request; the tokens are
					// every request the interaction made — revisions and
					// explanations included — because that is what the user
					// paid to get this command.
					TokensIn:  ledgerTokens(ledger.Total().In),
					TokensOut: ledgerTokens(ledger.Total().Out),
					Success:   metrics.Success,
				})
			}

			if result.Err != nil {
				// Classified, never raw: the one-shot renders the
				// same failure row the session does, with the way out stated as
				// a command rather than as a key nothing is listening for.
				return reportFailure(result.Err, resolved.Model)
			}

			// The result surface already moves the safe default on a
			// destructive command and takes a deliberate `y` for it,
			// so asking the same question again here is a second prompt for
			// one decision. It still runs for anything that reached this
			// point without being asked.
			if cfg.SafetyWarningsEnabled() && !result.Confirmed {
				if result.Action == ui.ActionRun || result.Action == ui.ActionRunAll || result.Action == ui.ActionRunStep {
					if warnings := safety.Check(result.Command); len(warnings) > 0 {
						fmt.Fprintln(os.Stderr, "\n⚠ Safety warning:")
						for _, w := range warnings {
							fmt.Fprintf(os.Stderr, "  • %s\n", w.Risk)
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
							outcome = observe.TurnCancelled
							return nil
						}
					}
				}
			}

			switch result.Action {
			case ui.ActionRun:
				code := runner.Run(result.Command)
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, code)
				}
				finish()
				os.Exit(code)
			case ui.ActionRunAll:
				cmds := ui.SplitCommands(result.Command)
				for _, c := range cmds {
					code := runner.Run(c)
					if code != 0 {
						if db != nil && requestID > 0 {
							_ = db.RecordExitCode(requestID, code)
						}
						finish()
						os.Exit(code)
					}
				}
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, 0)
				}
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
						if db != nil && requestID > 0 {
							_ = db.RecordExitCode(requestID, code)
						}
						finish()
						os.Exit(code)
					}
				}
				if db != nil && requestID > 0 {
					_ = db.RecordExitCode(requestID, 0)
				}
			case ui.ActionSave:
				if db != nil && result.SaveName != "" {
					if err := db.SaveSnippet(result.SaveName, result.Command); err != nil {
						fmt.Fprintf(os.Stderr, "Error saving snippet: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "Saved snippet %q.\n", result.SaveName)
						if desc := generateDescription(cmd.Context(), p, result.Command); desc != "" {
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

			// Saving a snippet writes a description, and writing it is
			// another request. It lands after the row above, so the row is
			// revised rather than left understating the interaction.
			if db != nil && requestID != 0 {
				total := ledger.Total()
				_ = db.UpdateRequestTokens(requestID, ledgerTokens(total.In), ledgerTokens(total.Out))
			}

			return nil
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
