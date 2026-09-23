package chat

// What the checkout was not allowed to put into this session, and what
// changed in one that was. A repository names skills, agent profiles, quality
// suites, hooks and servers, and none of them load until the person has
// answered for the checkout, so a session in a fresh clone is quietly smaller
// than the same session in a trusted one — quietly being the failure this
// states out loud. A trusted checkout loads what it holds as it is now, and
// the first session after a change says once what moved.
// See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.

import (
	"strings"

	"github.com/rfizzle/shhh/internal/project"
)

// Trust is the checkout's standing as the session read it at startup. The
// zero value is a session with nothing withheld, which is what a trusted
// checkout, an empty one and every non-chat host all look like.
//
// It arrives inside StartInfo rather than through a setter of its own
// because it is one more thing the CLI learned about this checkout while it
// was surveying it, read once and never again while the session runs:
// trusting takes effect in the next session, so a value is the honest shape
// and a callback would imply otherwise.
type Trust struct {
	// Withheld names what this checkout declares and the session did not
	// load, in the words the doctor uses for the same list.
	Withheld []string
	// Changed names the kinds that moved since a session here last read a
	// trusted checkout. Nothing is withheld for it: it is the notice, and
	// this is the one session that shows it.
	Changed []string
	// Manage backs the /trust slash command.
	Manage func(args []string) string
	// Granted is the answer itself: the checkout was trusted and the answer
	// has not been withdrawn. Withheld is the list a reader is shown and is
	// empty both for a trusted checkout and for one that declares nothing,
	// so it cannot answer this question — and a commit hook is a program the
	// checkout can point git at, which is a decision that needs the answer
	// and not the list.
	// See docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.
	Granted bool
}

// trust is what the checkout was not allowed to put into this session.
func (m Model) trust() Trust {
	if m.start == nil {
		return Trust{}
	}
	return m.start.Trust
}

// withholding reports whether anything at all was left out.
func (t Trust) withholding() bool { return len(t.Withheld) > 0 }

// withholds reports whether one kind of resource is in the withheld list.
func (t Trust) withholds(kind project.Kind) bool {
	for _, name := range t.Withheld {
		if name == string(kind) {
			return true
		}
	}
	return false
}

// joinAnd is a list inside a sentence rather than in a column.
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// trustStatus is the withheld list in words for `/status`, or the kinds a
// trusted checkout changed since it was last read, and nothing at all when
// there is neither. It is on the same screen as the tool sources because it
// is the same question — what is not here, or not as it was — asked of the
// checkout rather than of the servers.
func (m Model) trustStatus() string {
	t := m.trust()
	if len(t.Changed) > 0 {
		return "Changed\nThis checkout's " + joinAnd(t.Changed) +
			" changed since you trusted it, and are in this session as they are now.\n" +
			"/trust off withdraws the answer from the next session on."
	}
	if !t.withholding() {
		return ""
	}
	return "Withheld\nThis checkout is not trusted, so its " + joinAnd(t.Withheld) +
		" are not in this session.\n/trust loads them from the next session on."
}

// trustCommand is `/trust`: the answer, and `/trust off` to withdraw it.
func (m Model) trustCommand(args []string) string {
	manage := m.trust().Manage
	if manage == nil {
		return "Trust is not answered from this session; `shhh trust` records it."
	}
	return manage(args)
}
