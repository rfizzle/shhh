package chat

// The run's review stage: the reading child it hands the work to, the diff
// that child is given, and what its verdict does to the run. It is its own
// file because rendering the session's own change as a diff is a job of its
// own that no other stage needs.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// startTodoReview hands the work to a reading child. The child cannot run
// commands, so the change is read here and put in its task, bounded. With no
// supervisor, or a spawn the supervisor refuses, the orchestrator reads in
// its own turn and the step label says so.
func (m Model) startTodoReview() (tea.Model, tea.Cmd) {
	st, it := m.todoRunner.state, m.todoRunner.item
	// A pipeline that changes the tree and changed nothing has produced
	// nothing to read, which is a run that went wrong rather than one with
	// a clean review. A pipeline whose steps only read has no change to
	// point at by design, and the reader is given the item and the answers.
	if st.Pipeline.Writes() && len(m.todoRunPaths()) == 0 {
		return m.todoRunStep(st.Block("the run changed no files under the repository, so there is nothing to review"))
	}
	if m.subagents == nil {
		return m.todoRunStep(st.SelfReview(it))
	}
	args, _ := json.Marshal(map[string]any{
		"role": m.todoReviewRole(st),
		"name": st.Reviewer,
		"task": st.ReviewTask(it, run.BoundDiff(m.todoRunDiff(), run.ReviewDiffLines, run.ReviewFileFloor)),
	})
	if _, err := m.subagents.Spawn(args); err != nil {
		model, _ := m.systemNotice("No reviewer agent could be spawned — " + err.Error())
		return model.(Model).todoRunStep(st.SelfReview(it))
	}
	_ = st.Save(m.todos.Root)
	return m.systemNotice(fmt.Sprintf("▸ todo run %s · review by %s", st.Slug, st.Reviewer))
}

// todoReviewRole is the agent profile the reading child takes: the persona
// the step names where this session has one by that name, and the reviewer
// role otherwise.
//
// A persona is an agent profile and so is a role, so naming one where the
// other would go is the whole of it. The fallback is what makes a profile
// portable: the same pipeline worked in a coding session, which has no
// personas, is read by the role that reads changes there.
// See docs/capabilities/chat.md#colleagues-not-workers.
func (m Model) todoReviewRole(st *run.State) string {
	ps, ok := st.Pipeline.At(st.Stage)
	if !ok || ps.Persona == "" || m.subagents == nil {
		return string(subagent.RoleReviewer)
	}
	if _, has := m.subagents.Profiles()[subagent.Role(ps.Persona)]; !has {
		return string(subagent.RoleReviewer)
	}
	return ps.Persona
}

// todoRunDiff is the run's change as the changeset recorded it — before
// and after for every path, which is what shows a file the run created,
// where git's own diff of the tree would show nothing for it.
//
// It answers with one entry per file rather than with one string because
// what the reader is handed is bounded per file (run.BoundDiff): a budget
// spent in order hands over the first files whole and never mentions the
// rest, and a diff that stops looks exactly like a change that ended.
func (m Model) todoRunDiff() []string {
	root := m.todos.Root
	var files []string
	seen := map[string]bool{}
	// Paths from an earlier session have no record here; git's diff of
	// the tree stands in, with an untracked file shown whole.
	recorded := map[string]bool{}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRunner.state.Turn {
			continue
		}
		for _, r := range t.Records {
			if rel := runRelPath(root, r.Path); rel != "" && r.Changed() {
				recorded[rel] = true
			}
		}
	}
	for _, rel := range m.todoRunner.state.Paths {
		if recorded[rel] || seen[rel] {
			continue
		}
		seen[rel] = true
		// Only a diff counts; git's complaint about a path or a tree is
		// not one, and must not reach the reviewer as if it were.
		if out, code := git(root, "diff", "--", rel); code == 0 && strings.HasPrefix(out, "diff --git") {
			files = append(files, out)
			continue
		}
		if out, _ := git(root, "diff", "--no-index", os.DevNull, rel); strings.HasPrefix(out, "diff --git") {
			files = append(files, out)
		}
	}
	for _, t := range m.changes.Turns() {
		if int(t.N) < m.todoRunner.state.Turn {
			continue
		}
		for _, r := range t.Records {
			rel := runRelPath(root, r.Path)
			if rel == "" || seen[rel] || !r.Changed() {
				continue
			}
			seen[rel] = true
			files = append(files, recordDiff(rel, r))
		}
	}
	return files
}

// recordDiff renders one record as a unified diff.
func recordDiff(rel string, r changeset.Record) string {
	var b strings.Builder
	old, now := "a/"+rel, "b/"+rel
	if !r.BeforeExists {
		old = "/dev/null"
	}
	if !r.AfterExists {
		now = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", old, now)
	for _, h := range diff.Compute(r.Before, r.After) {
		b.WriteString(h.Header() + "\n")
		for _, l := range h.Lines {
			prefix := " "
			switch l.Kind {
			case diff.Add:
				prefix = "+"
			case diff.Del:
				prefix = "-"
			}
			b.WriteString(prefix + l.Text + "\n")
		}
	}
	return b.String()
}

// runRelPath is a record's path relative to the root, or "" when it is
// outside the root or a backlog file.
func runRelPath(root, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, filepath.Join(todo.StateDir, todo.Subdir)) {
		return ""
	}
	return rel
}

// todoReviewDone is the reviewer child finishing: its own final message is
// the review's answer. A child that did not finish — killed, failed, out of
// rounds — has no answer, and the reading falls back to this session's own
// turn rather than being graded on whatever the placeholder for a failed
// child happens to say.
func (m Model) todoReviewDone(status subagent.Status) (tea.Model, tea.Cmd, bool) {
	st := m.todoRunner.state
	if st == nil || st.Over() || st.Reviewer == "" || status.Name != st.Reviewer {
		return m, nil, false
	}
	report, state, ok := m.subagents.FinalReport(st.Reviewer)
	if !ok || state != subagent.StateDone {
		// A reader that did not finish is a reader the run did not get,
		// which is what SelfReview is for. Blocking on it stops a finished,
		// verified piece of work over the one stage that was always allowed
		// to be missing — a session with no supervisor has never had a
		// reader either — and the step label is what says which reading
		// this was.
		model, _ := m.systemNotice(fmt.Sprintf("The reviewer %s did not finish (%s) — reading the change in this session instead.",
			st.Reviewer, status.Detail))
		next, cmd := model.(Model).todoRunStep(st.SelfReview(m.todoRunner.item))
		return next, cmd, true
	}
	next, cmd := m.todoRunStep(st.ReviewResult(m.todoRunner.item, report))
	return next, cmd, true
}
