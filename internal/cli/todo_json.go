package cli

// `shhh todo --json` as data. The report is presentation and its shape is
// free to change with the terminal; a script reads these structs, which name
// the item's own header fields plus the three things a reader gets from the
// screen and a script otherwise cannot: whether an item is ready, what it is
// waiting on, and whether a run has it in flight.
//
// The warnings are in it for the same reason they are on the screen. A file
// with an unreadable size line still loads, and a script that saw only the
// fields would go on treating it as ungraded without ever learning that the
// line is there and wrong.

import (
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// todoDoc is one listing verb's answer: where the backlog is, the items the
// verb lists, the sprint scoping them, and the files that would not load.
type todoDoc struct {
	Root string `json:"root"`
	Dir  string `json:"dir"`
	// Items is always present, empty array included: a script that indexes
	// into it should find nothing rather than a missing key.
	Items  []todoItemDoc  `json:"items"`
	Sprint *todoSprintDoc `json:"sprint,omitempty"`
	// Diagnostics are the files that could not be read at all. They are the
	// reason an item the reader expected is not in Items.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// todoItemDoc is one item. Status is the word in the file; State is what the
// listing's last column says, which for an open item is whether it can be
// started — the two differ, and a script keying on the wrong one would start
// an item whose dependencies are outstanding.
type todoItemDoc struct {
	Slug      string       `json:"slug"`
	Title     string       `json:"title"`
	Kind      string       `json:"kind,omitempty"`
	Priority  string       `json:"priority"`
	Size      string       `json:"size,omitempty"`
	Status    string       `json:"status"`
	State     string       `json:"state"`
	Ready     bool         `json:"ready"`
	Waiting   []string     `json:"waiting,omitempty"`
	DependsOn []string     `json:"depends_on,omitempty"`
	Created   string       `json:"created,omitempty"`
	Session   string       `json:"session,omitempty"`
	Held      *todoHeldDoc `json:"held,omitempty"`
	Archived  bool         `json:"archived,omitempty"`
	Path      string       `json:"path"`
	Warnings  []string     `json:"warnings,omitempty"`
}

// todoHeldDoc is the run that has the item, from its checkpoint. A script
// that means to change an item reads this first: the verbs refuse a held
// one, and the refusal is a failed command rather than an answer.
type todoHeldDoc struct {
	Session string `json:"session"`
	Stage   string `json:"stage,omitempty"`
	Sprint  bool   `json:"sprint,omitempty"`
}

type todoSprintDoc struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Goal   string `json:"goal,omitempty"`
	Path   string `json:"path"`
	// Done and Total are the sprint's progress, leaving out a slug the
	// backlog no longer holds — it is on Items with its own state.
	Done  int                  `json:"done"`
	Total int                  `json:"total"`
	Items []todoSprintEntryDoc `json:"items"`
	// Open reports the sprint still scoping the ready set. A closed one on
	// disk is a record and the whole backlog applies again.
	Open     bool     `json:"open"`
	Warnings []string `json:"warnings,omitempty"`
}

// todoSprintEntryDoc is one of the sprint's slugs placed against the backlog
// as it stands, including a slug the backlog dropped — which has no item and
// is exactly the fact worth reporting.
type todoSprintEntryDoc struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title,omitempty"`
	State   string   `json:"state"`
	Waiting []string `json:"waiting,omitempty"`
}

// todoJSON is the store as the screen shows it, narrowed to the items the
// verb was asked about. The sprint rides along whichever verb was asked,
// because an open one is what "ready" was scoped by and a set read without
// it is a set read against the wrong list.
func todoJSON(s *todo.Store, items []todo.Item) todoDoc {
	doc := todoDoc{Root: s.Root, Dir: s.Dir, Items: make([]todoItemDoc, 0, len(items))}
	ready := map[string]bool{}
	for _, it := range s.Ready() {
		ready[it.Slug] = true
	}
	for _, it := range items {
		doc.Items = append(doc.Items, todoItemJSON(s, it, ready[it.Slug]))
	}
	doc.Sprint = todoSprintJSON(s)
	doc.Diagnostics = s.Diagnostics
	return doc
}

func todoItemJSON(s *todo.Store, it todo.Item, ready bool) todoItemDoc {
	doc := todoItemDoc{
		Slug: it.Slug, Title: it.Title, Kind: string(it.Kind),
		Priority: string(it.Priority), Size: string(it.Size),
		Status: string(it.Status), State: todoState(s, it), Ready: ready,
		Waiting: s.Waiting(it), DependsOn: it.DependsOn,
		Created: it.Created, Session: it.Session,
		Archived: it.Archived, Path: it.Path, Warnings: it.Warnings,
	}
	if h, held := run.HeldBy(s.Root, it.Slug); held {
		doc.Held = &todoHeldDoc{Session: h.Session, Stage: string(h.Stage), Sprint: h.Sprint}
	}
	return doc
}

// todoSprintJSON is the sprint file placed against the backlog, or nothing
// where the checkout holds no sprint.
func todoSprintJSON(s *todo.Store) *todoSprintDoc {
	sp := s.Sprint
	if sp == nil {
		return nil
	}
	done, total := s.SprintProgress()
	doc := &todoSprintDoc{
		Name: sp.Name, Status: string(sp.Status), Goal: sp.Goal, Path: sp.Path,
		Done: done, Total: total, Open: sp.Open(), Warnings: sp.Warnings,
	}
	entries := s.SprintEntries()
	doc.Items = make([]todoSprintEntryDoc, 0, len(entries))
	for _, e := range entries {
		doc.Items = append(doc.Items, todoSprintEntryDoc{
			Slug: e.Slug, Title: e.Item.Title, State: string(e.State), Waiting: e.Waiting,
		})
	}
	return doc
}
