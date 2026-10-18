package eval

// `shhh cmd` and the inline widget: one request, one command back.
//
// The one-shot is a single completion with no session around it, so like the
// other table kinds it is asked in this process. It is asked through
// raw.Generate, the function `shhh cmd --raw` prints from, so the prompt, the
// fence stripping and the trimming are the ones that ship. The shell widgets
// (internal/cli/init.go) run `shhh cmd --raw "$BUFFER"`, so inline is measured
// by this table too; that the widget then replaces the buffer and puts the
// cursor at its end is shell script, and belongs to the init tests and a
// driven scene.
//
// The answer is a word from a closed set, decided mechanically: the safety
// table first, because a command it flags is dangerous whatever the row
// hoped for, and the row's expression second.
// See docs/capabilities/evals.md#the-other-modes-are-measured-too.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/proposal"
	"github.com/rfizzle/shhh/internal/raw"
	"github.com/rfizzle/shhh/internal/safety"
)

// KindOneShot puts a row's instruction to the one-shot command generator and
// labels the command that comes back. It names a model: the command is the
// model's.
const KindOneShot Kind = "one-shot"

// What a one-shot row's answer can be.
const (
	// LabelExpected is a command the safety table passes and the row's
	// expression matches.
	LabelExpected = "expected"
	// LabelOther is a command the table passes and the expression does not
	// match.
	LabelOther = LabelOtherCall
	// LabelDangerous is a command the safety table flags.
	LabelDangerous = "dangerous"
)

// defaultOneShotShell is the shell a row's request names unless the row says
// another. It is pinned, not detected, so a row's answer does not depend on
// the login shell of whoever ran the suite.
const defaultOneShotShell = "bash"

// oneShotRow takes a one-shot row's own keys: the expression compiled, so a
// bad one is refused where the reader is editing, and the shell pinned to
// bash unless the row names another.
func oneShotRow(path string, row Row, rf rowFile) (Row, error) {
	row.Command = strings.TrimSpace(rf.Command)
	if row.Command != "" {
		re, err := regexp.Compile(row.Command)
		if err != nil {
			return Row{}, fmt.Errorf("%s: %s: command %q: %w", path, row.Name, row.Command, err)
		}
		row.match = re
	}
	row.Shell = strings.TrimSpace(rf.Shell)
	if row.Shell == "" {
		row.Shell = defaultOneShotShell
	}
	return row, nil
}

// askOneShot puts one row to raw.Generate and labels what came back.
func askOneShot(ctx context.Context, p llm.Provider, model string, row Row) Answer {
	start := time.Now()
	// Cancelled on the way out so the metering goroutine below never waits on
	// a reader that has gone: Generate stops reading at the first Done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	meter := &meteredProvider{Provider: p}
	answer, err := raw.Generate(ctx, raw.Opts{
		Provider: meter,
		Model:    model,
		Prompt:   row.Instruction,
		Shell:    row.Shell,
	})
	a := Answer{Row: row, Usage: meter.total(), Elapsed: time.Since(start)}
	if err != nil {
		a.Err = firstLine(err.Error())
		return a
	}
	command := proposal.CommandPart(answer)
	if command == "" {
		a.Err = "the answer held no command"
		return a
	}
	a.Label, a.Reason = labelCommand(row, command)
	return a
}

// labelCommand is the whole of the grading: the safety table first, the
// expression second. The reason is the table's own risk sentence or a fixed
// phrase, never the command, which a report must not carry.
func labelCommand(row Row, command string) (label, reason string) {
	if warnings := safety.Check(command); len(warnings) > 0 {
		return LabelDangerous, warnings[0].Risk
	}
	if row.match != nil && row.match.MatchString(command) {
		return LabelExpected, "matched the row's expression"
	}
	return LabelOther, "passed the safety table and did not match the row's expression"
}

// meteredProvider sums the usage the stream reports, because Generate returns
// the answer alone and a table attempt is priced from its rows' usage.
type meteredProvider struct {
	llm.Provider
	mu    sync.Mutex
	usage llm.Usage
}

func (m *meteredProvider) StreamCompletion(ctx context.Context, msgs []llm.Message, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	in, err := m.Provider.StreamCompletion(ctx, msgs, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Usage != nil {
				m.mu.Lock()
				m.usage.PromptTokens += ev.Usage.PromptTokens
				m.usage.CompletionTokens += ev.Usage.CompletionTokens
				m.mu.Unlock()
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (m *meteredProvider) total() llm.Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}
