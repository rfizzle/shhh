package subagent

// Failure handoffs are the durable, bounded record a replacement receives.
// They are made from the child's public activity rather than its conversation
// or raw tool output: a replacement needs verified facts and stable handles,
// not another model's private reasoning or instructions a tool happened to
// print.

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/meter"
)

const (
	maxHandoffProgress = 8
	maxHandoffTools    = 16
	maxHandoffText     = 1200
)

var (
	handoffEvidenceID = regexp.MustCompile(`\bev-[0-9a-f]{16}\b`)
	errInvalidHandoff = errors.New("invalid failure handoff")
)

// Handoff is the persisted record of a failed child attempt. Patch is retained
// for the parent to review but is deliberately never placed in a replacement
// prompt; the replacement receives PatchEvidence as an opaque handle instead.
type Handoff struct {
	Handle            string         `json:"-"`
	Child             string         `json:"child"`
	Role              Role           `json:"role"`
	Task              string         `json:"task"`
	Paths             []string       `json:"paths,omitempty"`
	Model             string         `json:"model,omitempty"`
	Budget            int64          `json:"budget"`
	RecommendedBudget int64          `json:"recommended_budget"`
	Tokens            HandoffTokens  `json:"tokens"`
	Spend             meter.Totals   `json:"spend"`
	Failure           HandoffFailure `json:"failure"`
	LastRound         int            `json:"last_round"`
	ReadPaths         []string       `json:"read_paths,omitempty"`
	WrittenPaths      []string       `json:"written_paths,omitempty"`
	Tools             []HandoffTool  `json:"tools,omitempty"`
	Progress          []string       `json:"progress,omitempty"`
	Evidence          []string       `json:"evidence,omitempty"`
	PatchEvidence     string         `json:"patch_evidence,omitempty"`
	Patch             string         `json:"patch,omitempty"`
}

// HandoffTokens keeps the phase accounting independent of the observability
// row, so a reopened session can show the budget that was actually consumed.
type HandoffTokens struct {
	Inherited int64 `json:"inherited"`
	Setup     int64 `json:"setup"`
	Tools     int64 `json:"tools"`
	Analysis  int64 `json:"analysis"`
	Handoff   int64 `json:"handoff"`
}

// HandoffFailure is the child's closed failure vocabulary plus the human
// detail shown on its lane.
type HandoffFailure struct {
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

// HandoffTool records only an action and its outcome. Tool results are never
// replayed here; retained evidence is named by an opaque ID in Evidence.
type HandoffTool struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
}

// MarshalHandoff is the storage boundary for a handoff. Keeping serialization
// here lets the persistence layer remain content-agnostic.
func MarshalHandoff(h Handoff) ([]byte, error) { return json.Marshal(h) }

// UnmarshalHandoff decodes a persisted handoff and rejects a record that could
// not safely identify the original task and role for a replacement.
func UnmarshalHandoff(data []byte) (Handoff, error) {
	var h Handoff
	if err := json.Unmarshal(data, &h); err != nil {
		return Handoff{}, err
	}
	if strings.TrimSpace(h.Task) == "" || h.Role == "" || h.Failure.Category == "" {
		return Handoff{}, errInvalidHandoff
	}
	return h, nil
}

func (c *child) makeHandoff(reason, detail string, lastRound int) Handoff {
	c.mu.Lock()
	transcript := append([]TranscriptEntry(nil), c.transcript...)
	progress := append([]string(nil), c.progress...)
	written := mapsKeys(c.wrote)
	slices.Sort(written)
	tokens := c.handoffTokensLocked()
	h := Handoff{
		Child: c.name, Role: c.role, Task: c.task, Paths: append([]string(nil), c.paths...), Model: c.model,
		Budget: c.maxTokens, RecommendedBudget: recommendedBudget(c.maxTokens, c.budgetHit),
		Tokens: tokens, Spend: c.priorSpend.Plus(c.spend.Total()),
		Failure: HandoffFailure{Category: reason, Detail: detail}, LastRound: lastRound,
		WrittenPaths: written,
		Progress:     progress,
	}
	c.mu.Unlock()

	read := map[string]bool{}
	evidence := map[string]bool{}
	for _, entry := range transcript {
		switch entry.Kind {
		case EntryTool:
			if path := handoffReadPath(entry.Tool, entry.Args); path != "" {
				read[path] = true
			}
			if !entry.Pending && len(h.Tools) < maxHandoffTools {
				h.Tools = append(h.Tools, HandoffTool{Name: entry.Tool, Outcome: string(digest.Outcome(entry.Result))})
			}
			for _, id := range handoffEvidenceID.FindAllString(entry.Result, -1) {
				evidence[id] = true
			}
		}
	}
	h.ReadPaths = mapsKeys(read)
	slices.Sort(h.ReadPaths)
	h.Evidence = mapsKeys(evidence)
	slices.Sort(h.Evidence)
	return h
}

func estimateReportTokens(report string) int64 { return agent.EstimateTokens(report) }

func (c *child) handoffTokensLocked() HandoffTokens {
	out := HandoffTokens{Inherited: c.inheritedTokens, Setup: c.setupTokens, Tools: c.toolResultTokens, Handoff: estimateReportTokens(c.report)}
	out.Analysis = max(c.fresh-out.Inherited-out.Setup-out.Tools-out.Handoff, 0)
	return out
}

func handoffText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > maxHandoffText {
		text = text[:maxHandoffText] + "…"
	}
	return text
}

func handoffReadPath(tool, args string) string {
	if tool != "read_file" && tool != "search" && tool != "lsp_document_symbol" && tool != "lsp_hover" {
		return ""
	}
	var raw struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &raw) != nil {
		return ""
	}
	return strings.TrimSpace(raw.Path)
}

func mapsKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func recommendedBudget(budget int64, budgetHit bool) int64 {
	out, _ := retryBudget(budget, budgetHit)
	return out
}

func (s *Supervisor) persistHandoff(c *child, reason, detail string, lastRound int) {
	h := c.makeHandoff(reason, detail, lastRound)
	// A memory-only supervisor has no durable handoff to attach a patch to.
	// Reading and staging a writer worktree during cancellation would delay its
	// slot release for data that cannot be recovered, so it tears down normally.
	if c.rec.Handoff != nil {
		if c.profile.Writes {
			worktree, _ := c.workspace()
			if patch, err := worktreePatch(worktree); err == nil && strings.TrimSpace(patch) != "" {
				h.Patch = patch
				if c.env.Archive != nil {
					h.PatchEvidence, _ = c.env.Archive("subagent_patch", patch)
				}
			}
		}
		if data, err := MarshalHandoff(h); err == nil {
			if id, saveErr := c.rec.Handoff(data); saveErr == nil {
				h.Handle = id
			}
		}
	}
	c.mu.Lock()
	c.handoff, c.handoffID = h, h.Handle
	c.retainWorktree = c.profile.Writes && strings.TrimSpace(h.Patch) != ""
	c.mu.Unlock()
}

func (s *Supervisor) supersedeHandoff(handle string) {
	s.mu.Lock()
	children := append([]*child(nil), s.children...)
	s.mu.Unlock()
	for _, c := range children {
		c.mu.Lock()
		if c.handoffID != handle || !c.retainWorktree {
			c.mu.Unlock()
			continue
		}
		worktree, repoTop := c.worktree, c.repoTop
		c.retainWorktree = false
		c.mu.Unlock()
		removeWorktree(repoTop, worktree)
		return
	}
}

func (c *child) keepsWorktree(worktree string) bool {
	if worktree == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retainWorktree && c.worktree == worktree
}

func resumePrologue(h Handoff, validEvidence func(string) bool) string {
	evidence := make([]string, 0, len(h.Evidence)+1)
	for _, id := range h.Evidence {
		if validEvidence == nil || validEvidence(id) {
			evidence = append(evidence, id)
		}
	}
	if h.PatchEvidence != "" && (validEvidence == nil || validEvidence(h.PatchEvidence)) {
		evidence = append(evidence, h.PatchEvidence)
	}
	var b strings.Builder
	b.WriteString("A previous child failed on this task. Its original task and declared scope remain binding. Do not repeat its repository survey; start with the next unresolved action from this verified handoff.\n\n")
	b.WriteString("Failure: " + h.Failure.Category)
	if h.Failure.Detail != "" {
		b.WriteString(" — " + h.Failure.Detail)
	}
	b.WriteString(".\n")
	if h.LastRound > 0 {
		b.WriteString("Last completed tool round: " + strconv.Itoa(h.LastRound) + ".\n")
	}
	if len(h.ReadPaths) > 0 {
		b.WriteString("Already read: " + strings.Join(h.ReadPaths, ", ") + ".\n")
	}
	if len(h.WrittenPaths) > 0 {
		b.WriteString("Changed in its isolated workspace: " + strings.Join(h.WrittenPaths, ", ") + ".\n")
	}
	if len(h.Progress) > 0 {
		b.WriteString("Public progress:\n")
		for _, p := range h.Progress {
			b.WriteString("- " + p + "\n")
		}
	}
	if len(evidence) > 0 {
		b.WriteString("Retained evidence: " + strings.Join(evidence, ", ") + ". Read a handle only if its bounded summary is needed.\n")
	}
	b.WriteString("\nThe task, unchanged:\n\n")
	return b.String()
}
