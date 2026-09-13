package plan

// The durable artifact a plan-mode turn ends on. Like a failed child's
// handoff it is made from what the session can stand behind — the instruction
// that was given, the steps the person approved, the paths those steps name,
// and opaque handles for evidence the research kept — and never from raw tool
// output: a record made of what a command printed is the transcript again,
// and the transcript is the thing a fresh session is leaving behind.
// See docs/capabilities/coding-agent.md#an-approved-plan-is-an-artifact.

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// maxRecordText bounds the prose a record carries for a plan that never
// adopted the step shape. It is the whole plan in that case, so it is
// generous — but it is still bounded, because a model that answered with a
// file's contents would otherwise put them in the seed of every session the
// record opens.
const maxRecordText = 4000

var errInvalidRecord = errors.New("invalid plan record")

// Record is one approved plan, as much of it as outlives the conversation.
type Record struct {
	// Handle is the store's opaque id, assigned on save. It is not part of
	// the serialized record for the same reason a handoff's is not: the
	// content is what was written down, and the handle is where.
	Handle string `json:"-"`
	// Task is the instruction the planning turn was serving, in the person's
	// own words. A record whose plan is carried into a fresh session has to
	// say what was asked for, or the new session has steps and no reason.
	Task string `json:"task"`
	// Title is the plan's own heading where the model gave one.
	Title string `json:"title,omitempty"`
	// Scope is the paths the steps name, in first-mention order — the plan's
	// declared reach, which is the half of it a reader checks first.
	Scope []string `json:"scope,omitempty"`
	// Steps are the plan as the card showed it.
	Steps []RecordStep `json:"steps,omitempty"`
	// Text is the plan as prose, kept only for a plan that never adopted the
	// step shape. With steps it would be the same plan twice.
	Text string `json:"text,omitempty"`
	// Evidence are the handles of retained output the research left behind.
	// Handles and not content: a fresh session reads one only if it turns out
	// to need it, which is the difference between a seed and a transcript.
	Evidence []string `json:"evidence,omitempty"`
}

// RecordStep is one step, flattened to the words a later session can act on.
// The action is its name and not its number, because the number is this
// package's business and a record outlives the order these constants are
// declared in.
type RecordStep struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Action string   `json:"action,omitempty"`
	Paths  []string `json:"paths,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// NewRecord builds the record for an approved plan: the plan itself, the
// instruction it was serving, and the evidence handles the turn collected.
func NewRecord(task string, p Plan, evidence []string) Record {
	r := Record{
		Task:     strings.TrimSpace(task),
		Title:    strings.TrimSpace(p.Title),
		Scope:    p.pathsWhere(func(Action) bool { return true }),
		Evidence: evidence,
	}
	for _, s := range p.Steps {
		r.Steps = append(r.Steps, RecordStep{
			Number: s.Number, Title: s.Title, Action: s.Action.String(),
			Paths: s.Paths, Note: s.Note,
		})
	}
	if !p.Structured() {
		r.Text = boundText(p.Text)
	}
	return r
}

// boundText trims a plan's prose to what a seed may carry.
func boundText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > maxRecordText {
		return text[:maxRecordText] + "…"
	}
	return text
}

// Empty reports a record with no plan in it, which is what an approval of a
// response the parser found nothing in produces. Nothing is written down for
// one: a stored record with no steps and no prose is a handle that promises a
// plan and hands back a task line.
func (r Record) Empty() bool {
	return len(r.Steps) == 0 && strings.TrimSpace(r.Text) == ""
}

// MarshalRecord is the storage boundary, so the persistence layer stays
// content-agnostic.
func MarshalRecord(r Record) ([]byte, error) { return json.Marshal(r) }

// UnmarshalRecord decodes a stored record and refuses one that could not seed
// a session — no task to say what was asked for, or no plan to carry.
func UnmarshalRecord(data []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(r.Task) == "" || r.Empty() {
		return Record{}, errInvalidRecord
	}
	return r, nil
}

// Prologue is the record as the one message a fresh session opens holding:
// what was asked for, the plan that was approved, and the handles of anything
// the research kept. It is written as a statement of what has already been
// decided rather than as a request for a plan, because the session it opens
// is the one carrying the plan out.
func (r Record) Prologue() string {
	var b strings.Builder
	b.WriteString("A plan for this task was approved in an earlier session, which is no longer in context. " +
		"Carry it out. Do not plan it again, and do not repeat the research behind it — read what you need as you reach it.\n\n")
	b.WriteString("The task, in the user's words:\n" + r.Task + "\n")
	if r.Title != "" {
		b.WriteString("\nPlan: " + r.Title + "\n")
	}
	if len(r.Steps) > 0 {
		b.WriteString("\nThe approved steps:\n")
		for _, s := range r.Steps {
			b.WriteString(strconv.Itoa(s.Number) + ". " + s.Title + "\n")
			if len(s.Paths) > 0 {
				b.WriteString("   files: " + strings.Join(s.Paths, ", ") + "\n")
			}
			if s.Action != "" {
				b.WriteString("   action: " + s.Action + "\n")
			}
			if s.Note != "" {
				b.WriteString("   note: " + s.Note + "\n")
			}
		}
	} else if r.Text != "" {
		b.WriteString("\nThe approved plan:\n" + r.Text + "\n")
	}
	if len(r.Evidence) > 0 {
		b.WriteString("\nRetained evidence: " + strings.Join(r.Evidence, ", ") +
			". Read a handle only if a step turns out to need it.\n")
	}
	return b.String()
}
