package agent

// The explanation of a command, for a reader deciding whether to run it
// (docs/interface/surfaces.md#the-approval-card). A cheap model reads the
// command in front of the person and says what it does, and the answer
// settles nothing: it is read, and the decision is still waiting behind it.
//
// It is the titler's shape and shares its rules: one request, no retries, a
// failed reading changes nothing and says so. What it does not share is where
// the words come from: the instruction is handed in, the way the classifier's
// and the summariser's are, and what the session hands it is the one-shot's
// own — so a command explained at a card and the same command explained under
// `shhh cmd` are explained by one rule rather than by two that drift.

import (
	"context"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// DefaultExplainTimeout bounds one reading. It is shorter than the
	// classifier's because nothing is blocked on it and somebody is watching
	// it: a verdict the session is waiting on may take its time, and a card
	// that has said "asking" for half a minute has stopped being an offer.
	DefaultExplainTimeout = 20 * time.Second
	// DefaultExplainMaxTokens caps the whole response, the reasoning
	// included: every dialect spends the thought and the answer from one
	// ceiling, and a ceiling under the smallest budget a dialect asks for at
	// low returns an unfinished thought and no paragraph
	// (classifier.go).
	DefaultExplainMaxTokens = 8192
	// maxExplainCommand bounds the command the reading is taken over. A
	// command longer than this is a script, and the head of it is what an
	// explanation is read from anyway.
	maxExplainCommand = 4_000
)

// ExplainConfig bounds the explainer. Model is the model that answers; empty
// disables it, the way an unconfigured titler is disabled.
type ExplainConfig struct {
	Model string
	// Prompt is the whole instruction. It is required rather than defaulted,
	// because the words that settle what an explanation of a command says and
	// how long it is are the one-shot's and live above this package: a
	// built-in fallback here would be a second answer to a question already
	// answered, and it is the first thing that would drift. The command
	// travels in the user turn either way (Explain).
	Prompt    string
	Timeout   time.Duration
	MaxTokens int
}

func (c ExplainConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultExplainTimeout
}

func (c ExplainConfig) maxTokens() int {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return DefaultExplainMaxTokens
}

// Explainer says what a command does, through the session's provider.
type Explainer struct {
	provider provider.Provider
	cfg      ExplainConfig
}

func NewExplainer(p provider.Provider, cfg ExplainConfig) *Explainer {
	return &Explainer{provider: p, cfg: cfg}
}

// Enabled reports whether a reading will actually be taken. The surface asks
// before it offers the key: an offer that cannot be answered is worse than no
// offer (docs/interface/surfaces.md#the-approval-card).
func (e *Explainer) Enabled() bool {
	return e != nil && e.provider != nil &&
		strings.TrimSpace(e.cfg.Model) != "" && strings.TrimSpace(e.cfg.Prompt) != ""
}

// Model is the model that answers, for a readout. The card names it, because
// an explanation is a claim and a reader deciding what to do with it is owed
// who made it.
func (e *Explainer) Model() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.cfg.Model)
}

// ExplainRequest is the command a paragraph is asked about.
type ExplainRequest struct {
	Command string
}

// ExplainVerdict is one reading. Failed marks a reading that did not happen —
// the request failed, the deadline passed, or the model answered with
// nothing — and Err is what to say about it. There is no verdict that is
// neither a paragraph nor a failure: a blank screen is not an answer to a
// question somebody pressed a key to ask.
type ExplainVerdict struct {
	Text    string
	Model   string
	Usage   provider.Usage
	Failed  bool
	Err     string
	Elapsed time.Duration
}

// Explain takes one reading of what the command does.
func (e *Explainer) Explain(ctx context.Context, req ExplainRequest) ExplainVerdict {
	start := time.Now()
	v := ExplainVerdict{Failed: true, Model: e.Model()}
	finish := func(v ExplainVerdict) ExplainVerdict {
		v.Elapsed = time.Since(start)
		return v
	}
	if !e.Enabled() {
		v.Err = "nothing is configured to explain commands"
		return finish(v)
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		v.Err = "there is no command to explain"
		return finish(v)
	}
	command = truncateTail(command, maxExplainCommand)

	attemptCtx, cancel := context.WithTimeout(ctx, e.cfg.timeout())
	defer cancel()
	// The instruction and the command travel in separate messages, and the
	// command is labelled as what it is. The wording is the caller's and is
	// not rewritten here; what is added is the framing every bounded reader
	// in this package uses (classifier.go), and it is added because this
	// command was written by a model and is about to be read by a person
	// deciding whether to run it — an explanation that could be talked into
	// vouching for the thing it describes is worse than none.
	events, err := e.provider.StreamCompletion(attemptCtx, []provider.Message{
		{Role: provider.RoleSystem, Content: e.cfg.Prompt},
		{Role: provider.RoleUser, Content: "UNTRUSTED COMMAND:\n" + command},
	}, provider.CompletionOpts{
		Model:     e.cfg.Model,
		MaxTokens: e.cfg.maxTokens(),
		// A shallow thought is the right amount for reading a command line,
		// and asking for it is the only way to bound one on a model that
		// thinks whether or not it was asked.
		Effort: provider.EffortLow,
	})
	if err != nil {
		v.Err = "the explanation could not be read: " + err.Error()
		return finish(v)
	}

	var text strings.Builder
	for done := false; !done; {
		select {
		case <-attemptCtx.Done():
			// Guards against providers that ignore cancellation.
			v.Err = "the explanation could not be read: " + attemptCtx.Err().Error()
			return finish(v)
		case ev, ok := <-events:
			if !ok {
				done = true
				break
			}
			if ev.Err != nil {
				v.Err = "the explanation could not be read: " + ev.Err.Error()
				return finish(v)
			}
			text.WriteString(ev.Token)
			if ev.Usage != nil {
				v.Usage = *ev.Usage
			}
			if ev.Done {
				done = true
			}
		}
	}

	answer := strings.TrimSpace(text.String())
	if answer == "" {
		v.Err = "the model returned no explanation"
		return finish(v)
	}
	v.Text = answer
	v.Failed = false
	return finish(v)
}
