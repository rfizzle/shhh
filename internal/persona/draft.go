package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// Request is one drafting turn. The first carries the brief; a later one
// carries the draft so far and what the person said about it, or the
// answers to the questions the drafter asked.
type Request struct {
	Kind Kind
	// Brief is what the person said they want, in their words.
	Brief string
	// Exchange is the questions asked and the answers given, in order,
	// when the brief was too thin to draft from.
	Exchange []QA
	// Current is the draft being revised, with Feedback the person's note
	// on it; both empty for a first draft.
	Current  *Draft
	Feedback string
	// Existing is the role names the session already has.
	Existing []string
	// Models the drafter may name; empty leaves the model inherited.
	Models []string
}

// QA is one question the drafter asked and the answer it got.
type QA struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Outcome is what a drafting turn produced: a draft, or the questions the
// drafter needs answered first, or a failure in words.
type Outcome struct {
	Draft     *Draft
	Questions []string
	Usage     provider.Usage
	Elapsed   time.Duration
	Failed    bool
	Err       string
}

// Config bounds the drafter's request.
type Config struct {
	Model     string
	Timeout   time.Duration
	MaxTokens int
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 90 * time.Second
	}
	return c.Timeout
}

func (c Config) maxTokens() int {
	if c.MaxTokens <= 0 {
		return 4000
	}
	return c.MaxTokens
}

// Drafter turns briefs into drafts on a provider.
type Drafter struct {
	provider provider.Provider
	cfg      Config
}

// NewDrafter builds a drafter; a nil provider or empty model disables it.
func NewDrafter(p provider.Provider, cfg Config) *Drafter {
	return &Drafter{provider: p, cfg: cfg}
}

// Enabled reports a drafter with somewhere to send the brief.
func (d *Drafter) Enabled() bool {
	return d != nil && d.provider != nil && strings.TrimSpace(d.cfg.Model) != ""
}

// DraftToolName is the tool the drafter answers through.
const DraftToolName = "draft_profile"

var draftSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"profile": {
			"type": "object",
			"description": "The drafted profile. Omit when you are asking questions instead.",
			"properties": {
				"name": {"type": "string", "description": "Role name: lowercase letters, digits, dashes; at most 24 characters; not one that already exists"},
				"description": {"type": "string", "description": "One line, under 120 characters, written for the model that will choose this role among others: what it is for and when to pick it"},
				"model": {"type": "string", "description": "A model from the list, or omit to inherit the session's"},
				"reasoning": {"type": "string", "enum": ["off", "low", "medium", "high", "inherit"]},
				"permissions": {"type": "array", "items": {"type": "string", "enum": ["web", "write", "execute"]}, "description": "Tiers beyond read"},
				"prompt": {"type": "string", "description": "The standing instructions, second person, 80-300 words"},
				"max_tokens": {"type": "integer", "description": "Token budget for one task; omit for the default"},
				"why": {"type": "string", "description": "One sentence on the choices that were not obvious"}
			},
			"required": ["name", "description", "permissions", "prompt"]
		},
		"questions": {
			"type": "array",
			"items": {"type": "string"},
			"maxItems": 3,
			"description": "Only when the brief is too thin to draft from: up to three short questions whose answers would change the draft. Never ask what you could reasonably assume."
		}
	}
}`)

// Draft runs one drafting turn.
func (d *Drafter) Draft(ctx context.Context, req Request) Outcome {
	start := time.Now()
	out := Outcome{Failed: true}
	finish := func(o Outcome) Outcome {
		o.Elapsed = time.Since(start)
		return o
	}
	if !d.Enabled() {
		out.Err = "no model is configured to draft a profile"
		return finish(out)
	}
	attemptCtx, cancel := context.WithTimeout(ctx, d.cfg.timeout())
	defer cancel()
	events, err := d.provider.StreamCompletion(attemptCtx, []provider.Message{
		{Role: provider.RoleSystem, Content: systemPrompt(req.Kind)},
		{Role: provider.RoleUser, Content: userPrompt(req)},
	}, provider.CompletionOpts{
		Model:      d.cfg.Model,
		MaxTokens:  d.cfg.maxTokens(),
		Tools:      []provider.Tool{{Name: DraftToolName, Description: "Return the drafted profile, or the questions you need answered first.", Parameters: draftSchema}},
		ToolChoice: "auto",
	})
	if err != nil {
		out.Err = err.Error()
		return finish(out)
	}
	var text strings.Builder
	var calls []provider.ToolCall
	for done := false; !done; {
		select {
		case <-attemptCtx.Done():
			out.Err = attemptCtx.Err().Error()
			return finish(out)
		case ev, ok := <-events:
			if !ok {
				done = true
				break
			}
			if ev.Err != nil {
				out.Err = ev.Err.Error()
				return finish(out)
			}
			text.WriteString(ev.Token)
			calls = append(calls, ev.ToolCalls...)
			if ev.Usage != nil {
				out.Usage = *ev.Usage
			}
			if ev.Done {
				done = true
			}
		}
	}
	for _, tc := range calls {
		if tc.Name == DraftToolName {
			if o, ok := parse(tc.Arguments, req.Kind); ok {
				o.Usage, o.Elapsed = out.Usage, time.Since(start)
				return o
			}
		}
	}
	if o, ok := parse(text.String(), req.Kind); ok {
		o.Usage, o.Elapsed = out.Usage, time.Since(start)
		return o
	}
	out.Err = "the model answered with neither a draft nor a question"
	return finish(out)
}

// parse reads the tool's arguments — or a reply carrying the same JSON —
// into an outcome. A draft that will not normalise is a failure in words,
// not a card the person cannot save.
func parse(text string, kind Kind) (Outcome, bool) {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if j := strings.LastIndex(text, "}"); j >= 0 && j < len(text)-1 {
		text = text[:j+1]
	}
	var body struct {
		Profile   *Draft   `json:"profile"`
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		return Outcome{}, false
	}
	if body.Profile != nil {
		if err := body.Profile.Normalise(kind); err != nil {
			return Outcome{Failed: true, Err: "the draft would not load: " + err.Error()}, true
		}
		return Outcome{Draft: body.Profile}, true
	}
	var qs []string
	for _, q := range body.Questions {
		if q = strings.TrimSpace(q); q != "" {
			qs = append(qs, q)
		}
		if len(qs) == 3 {
			break
		}
	}
	if len(qs) > 0 {
		return Outcome{Questions: qs}, true
	}
	return Outcome{}, false
}

// systemPrompt is where the two sessions part. Both draft the same file;
// what a good one looks like is not the same thing in a conversation and
// in a coding session, and a single prompt hedging between them would
// draft a persona that hedges too.
func systemPrompt(kind Kind) string {
	common := `You draft agent profiles for shhh, a terminal assistant. A profile is a small file: a role name, a one-line description the orchestrating model uses to choose the role, the permission tiers it gets beyond reading, optionally a model and reasoning level, and a prompt that is the agent's standing instructions. The agent spawned from it works one delegated task at a time, cannot see the conversation it was spawned from, and ends with a report that is the whole of what comes back.

Answer with the draft_profile tool. Draft from what you were given, making reasonable assumptions and naming them in "why"; ask questions only when an answer would genuinely change the draft, and then at most three, short. A person who gave you a full specification should get a draft, not questions. A person who gave you four words should get a draft too, if the four words are enough — a single sharp question is better than a vague draft, and a good draft is better than any question.

The prompt is the part that matters. Write it in the second person, to the agent. Be specific about what it does first, what it must never do, what its report looks like, and how it should sound. Do not restate what every agent is told (that it works one task, cannot see the conversation, ends with a report). Do not pad.`
	if kind == KindChat {
		return common + `

This profile is for shhh chat: a conversation where nothing acts on the machine. The agent is a colleague with a persona: a standpoint, a way of reasoning, a voice. It only reads — files and, if granted, the web — so the permissions may include "web" and nothing else; never grant write or execute. Give it a standpoint the orchestrator can route to by description ("checks claims against primary sources", "argues the opposite case", "explains in a domain's own terms"). Tell it how to answer: what it leads with, how it cites, how it signals confidence, what it refuses to pretend to know. Mention the session's shared notebook: it should read the notebook before searching and write down what it settled. Tone matters here; a persona that sounds like every other assistant is not a persona.`
	}
	return common + `

This profile is for shhh code: a coding agent that edits, runs and verifies work in a repository. The agent is an engineer with one job. Grant "write" if it changes files, "execute" if it runs anything, "web" only if its job needs the outside world; a writing or executing agent works in its own copy of the repository and hands back a patch a human reviews, so say in the prompt what its patch should and should not contain. Tell it how it verifies: which checks it runs, what "done" means, what it does when a check fails. Make it disciplined about scope — one job, the files that job touches, nothing opportunistic. Its report is read by an orchestrator deciding whether to take the patch: what changed, how it was verified, what to look at closely.`
}

// userPrompt is the turn's evidence: the brief, then whatever the
// conversation has added.
func userPrompt(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session: shhh %s.\n", req.Kind)
	if len(req.Existing) > 0 {
		fmt.Fprintf(&b, "Roles that already exist (do not reuse these names): %s.\n", strings.Join(req.Existing, ", "))
	}
	if len(req.Models) > 0 {
		fmt.Fprintf(&b, "Models the profile may name: %s. Omit the model to inherit the session's, which is the right default unless the job is clearly cheap and wide or clearly hard.\n", strings.Join(req.Models, ", "))
	}
	fmt.Fprintf(&b, "\nBRIEF (the person's own words):\n%s\n", strings.TrimSpace(req.Brief))
	if len(req.Exchange) > 0 {
		b.WriteString("\nYou asked, they answered:\n")
		for _, qa := range req.Exchange {
			fmt.Fprintf(&b, "Q: %s\nA: %s\n", qa.Question, qa.Answer)
		}
	}
	if req.Current != nil {
		cur, _ := json.MarshalIndent(req.Current, "", "  ")
		fmt.Fprintf(&b, "\nCURRENT DRAFT:\n%s\n", cur)
		fmt.Fprintf(&b, "\nWhat the person said about it — revise the draft to match, keeping everything they did not mention:\n%s\n", strings.TrimSpace(req.Feedback))
	}
	return b.String()
}
