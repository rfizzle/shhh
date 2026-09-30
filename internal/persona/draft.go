package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
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
	// Section names the one section of Current the note is about, for a
	// revision of that section alone; the rest of the draft is sent as
	// fixed context and only that section is taken from the answer. Empty
	// with Current set is a note on the whole draft: every prose section is
	// revised, and the tiers, tools and fields go as fixed context.
	Section string
	// Keep names the prose sections a whole-draft revision must hand back
	// as they are — the ones the person wrote themselves — so they go as
	// fixed context with the fields.
	Keep []string
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
	Model string
	// ModelAt, where set, is asked for the model at each drafting turn
	// instead of Model, and its answer is kept for that turn: a session that
	// moves the drafter onto another model takes it on the next turn, and a
	// turn already in flight finishes where it started
	// (docs/capabilities/configuration.md#a-session-can-hold-a-value-no-file-does).
	ModelAt   func() string
	Timeout   time.Duration
	MaxTokens int
}

// model is the name a turn asks with.
func (c Config) model() string {
	if c.ModelAt != nil {
		return strings.TrimSpace(c.ModelAt())
	}
	return strings.TrimSpace(c.Model)
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
	return d != nil && d.provider != nil && d.cfg.model() != ""
}

// DraftToolName is the tool the drafter answers through.
const DraftToolName = "draft_profile"

// draftSchema is the tool's arguments. The tool allowlist's vocabulary is
// the loader's own (config.KnownAgentTools) rather than a list written out
// here, so a tool added to a tier cannot go missing from what a draft may
// name.
var draftSchema = json.RawMessage(fmt.Sprintf(draftSchemaTemplate, jsonList(config.KnownAgentTools())))

func jsonList(items []string) string {
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

const draftSchemaTemplate = `{
	"type": "object",
	"properties": {
		"profile": {
			"type": "object",
			"description": "The drafted profile. Omit when you are asking questions instead.",
			"properties": {
				"name": {"type": "string", "description": "Role name: lowercase letters, digits, dashes; at most 24 characters; not one that already exists"},
				"description": {"type": "string", "description": "One line, under 120 characters, written for the model that will choose this role among others: what it is for and when to pick it"},
				"model": {"type": "string", "description": "A model from the list, or omit to inherit the session's"},
				"reasoning": {"type": "string", "enum": ["off", "low", "medium", "high", "inherit"], "description": "How deeply the role reasons; inherit takes the session's level"},
				"permissions": {"type": "array", "items": {"type": "string", "enum": ["web", "write", "execute"]}, "description": "Tiers granted beyond read: web, write or execute"},
				"tools": {"type": "array", "items": {"type": "string", "enum": %s}, "description": "Optional: narrow the toolset to these names, within the tiers you granted. Omit unless narrowing is the point of the role"},
				"sections": {
					"type": "object",
					"description": "The standing instructions, second person, 80-300 words across the five sections. Answer every section by name; where the brief gives you nothing to put in one and you cannot reasonably assume it, answer it with an empty string rather than leaving it out or folding it into another, so the person sees it is empty",
					"properties": {
						"purpose": {"type": "string", "description": "Purpose: the job, and what done looks like"},
						"scope": {"type": "string", "description": "Scope: the paths and areas it works in, and what it must leave alone"},
						"restrictions": {"type": "string", "description": "Restrictions: what it never does"},
						"method": {"type": "string", "description": "Method: how it works and how it verifies"},
						"report": {"type": "string", "description": "Report: what it hands back, and in what shape"}
					},
					"required": ["purpose", "scope", "restrictions", "method", "report"]
				},
				"max_tokens": {"type": "integer", "description": "Token budget for one task; omit for the default"},
				"why": {"type": "string", "description": "One sentence on the choices that were not obvious"}
			},
			"required": ["name", "description", "permissions", "sections"]
		},
		"questions": {
			"type": "array",
			"items": {"type": "string"},
			"maxItems": 3,
			"description": "Only when the brief is too thin to draft from: up to three short questions whose answers would change the draft. Never ask what you could reasonably assume."
		}
	}
}`

// DraftTool is the tool a drafting turn answers through.
func DraftTool() provider.Tool {
	return provider.Tool{
		Name:        DraftToolName,
		Description: "Return the drafted profile, or the questions you need answered first.",
		Parameters:  draftSchema,
	}
}

// Draft runs one drafting turn.
func (d *Drafter) Draft(ctx context.Context, req Request) Outcome {
	start := time.Now()
	out := Outcome{Failed: true}
	finish := func(o Outcome) Outcome {
		o.Elapsed = time.Since(start)
		return o
	}
	model := ""
	if d != nil {
		model = d.cfg.model()
	}
	if !d.Enabled() || model == "" {
		out.Err = "no model is configured to draft a profile"
		return finish(out)
	}
	attemptCtx, cancel := context.WithTimeout(ctx, d.cfg.timeout())
	defer cancel()
	events, err := d.provider.StreamCompletion(attemptCtx, []provider.Message{
		{Role: provider.RoleSystem, Content: systemPrompt(req.Kind)},
		{Role: provider.RoleUser, Content: userPrompt(req)},
	}, provider.CompletionOpts{
		Model:      model,
		MaxTokens:  d.cfg.maxTokens(),
		Tools:      []provider.Tool{DraftTool()},
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
	common := `You draft agent profiles for shhh, a terminal assistant. A profile is a small file: a role name, a one-line description the orchestrating model uses to choose the role, the permission tiers it gets beyond reading, optionally a model and reasoning level, and a prompt that is the agent's standing instructions, written in five sections: Purpose, Scope, Restrictions, Method and Report. The agent spawned from it works one delegated task at a time, cannot see the conversation it was spawned from, and ends with a report that is the whole of what comes back.

Answer with the draft_profile tool. Draft from what you were given, making reasonable assumptions and naming them in "why"; ask questions only when an answer would genuinely change the draft, and then at most three, short. A person who gave you a full specification should get a draft, not questions. A person who gave you four words should get a draft too, if the four words are enough — a single sharp question is better than a vague draft, and a good draft is better than any question.

The prompt is the part that matters. Write it in the second person, to the agent, one section at a time: Purpose is the job and what done looks like, Scope is where it works and what it leaves alone, Restrictions is what it never does, Method is how it works and verifies, and Report is what it hands back and in what shape. Be specific about what it does first, what it must never do, what its report looks like, and how it should sound. Each section is its own text: do not repeat another section's content or write its heading. A section you have nothing for is an empty string, which the person will see and can fill; never pad one to hide the gap. Do not restate what every agent is told (that it works one task, cannot see the conversation, ends with a report). Do not pad.`
	if kind == KindChat {
		return common + `

This profile is for shhh chat: a conversation where nothing acts on the machine. The agent is a colleague with a persona: a standpoint, a way of reasoning, a voice. It only reads — files and, if granted, the web — so the permissions may include "web" and nothing else; never grant write or execute. Give it a standpoint the orchestrator can route to by description ("checks claims against primary sources", "argues the opposite case", "explains in a domain's own terms"). Tell it how to answer: what it leads with, how it cites, how it signals confidence, what it refuses to pretend to know. Mention the session's shared notebook: it should read the notebook before searching and write down what it settled. Tone matters here; a persona that sounds like every other assistant is not a persona.`
	}
	return common + `

This profile is for shhh code: a coding agent that edits, runs and verifies work in a repository. The agent is an engineer with one job. Grant "write" if it changes files, "execute" only if it must run arbitrary commands, "web" only if its job needs the outside world; a writing or executing agent works in its own copy of the repository and hands back a patch a human reviews, so say in the prompt what its patch should and should not contain. Running the project's own suite is not a reason to grant "execute": that is the "quality_gate" tool, which the read tier already grants, so a reviewer-shaped role that verifies through the project's checks and changes nothing gets no tiers at all — and if you narrow "tools", name "quality_gate" among them. A profile granting "write" or "execute" may not have the gate, because it works in a copy of the checkout the gate would not see; never name it beside them. Tell it how it verifies: which checks it runs, what "done" means, what it does when a check fails. Make it disciplined about scope — one job, the files that job touches, nothing opportunistic. Its report is read by an orchestrator deciding whether to take the patch: what changed, how it was verified, what to look at closely.`
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
		// The sections are the prompt the drafter answered; sending the
		// prompt assembled from them as well would be the same text twice.
		shown := *req.Current
		if shown.Sections != nil {
			shown.Prompt = ""
		}
		cur, _ := json.MarshalIndent(shown, "", "  ")
		fmt.Fprintf(&b, "\nCURRENT DRAFT:\n%s\n", cur)
		if req.Section != "" {
			// One section is being revised. The rest is context the answer
			// must not move, and the caller takes that section alone from
			// it, so a rewrite of anything else would be work thrown away.
			fmt.Fprintf(&b, "\nRevise the %s section only, to match what the person said about it. Every other section and field is fixed: answer them exactly as they are in the current draft. Only %s is taken from your answer.\n", req.Section, req.Section)
			fmt.Fprintf(&b, "\nWhat the person said about %s:\n%s\n", req.Section, strings.TrimSpace(req.Feedback))
			return b.String()
		}
		// A note on the whole draft. Only the sections are taken from the
		// answer, so the fields are named as fixed rather than left for a
		// rewrite nobody reads, and so are the sections the person wrote.
		b.WriteString("\nRevise every section to match what the person said about the whole draft, keeping what they did not mention. The name, description, permissions, tools, model, reasoning and budget are fixed: answer them exactly as they are in the current draft.")
		if len(req.Keep) > 0 {
			fmt.Fprintf(&b, " The person wrote %s themselves, so %s fixed too: answer %s exactly as %s in the current draft.",
				sectionNames(req.Keep), isAre(req.Keep), itThem(req.Keep), isAre(req.Keep))
		}
		b.WriteString(" Only the sections are taken from your answer.\n")
		fmt.Fprintf(&b, "\nWhat the person said about the whole draft:\n%s\n", strings.TrimSpace(req.Feedback))
	}
	return b.String()
}

// sectionNames is a list of sections as a sentence says it: "the Scope
// section", "the Scope and Method sections".
func sectionNames(names []string) string {
	switch len(names) {
	case 1:
		return "the " + names[0] + " section"
	case 2:
		return "the " + names[0] + " and " + names[1] + " sections"
	}
	return "the " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " sections"
}

// isAre and itThem agree a sentence with how many sections it names.
func isAre(names []string) string {
	if len(names) == 1 {
		return "it is"
	}
	return "they are"
}

func itThem(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "them"
}
