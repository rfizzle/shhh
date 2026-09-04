package chat

// What the checkout was not allowed to put into this session. A repository
// names skills, agent profiles, quality suites, hooks and servers, and none
// of them load until the person has answered for the checkout, so a session
// in a fresh clone is quietly smaller than the same session in a trusted one
// — quietly being the failure this states out loud.
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
	// Changed says the answer was given once and the checkout has been
	// edited since, which is a different thing to tell the reader than never
	// having been asked.
	Changed bool
	// Manage backs the /trust slash command.
	Manage func(args []string) string
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

// word is the state as one word: the answer was never given, or it was and
// the checkout moved on.
func (t Trust) word() string {
	if t.Changed {
		return "changed"
	}
	return "withheld"
}

// trustStatus is the withheld list in words for `/status`, and nothing at all
// when the session lost nothing. It is on the same screen as the tool
// sources because it is the same question — what is not here — asked of
// the checkout rather than of the servers.
func (m Model) trustStatus() string {
	t := m.trust()
	if !t.withholding() {
		return ""
	}
	lead := "This checkout is not trusted"
	if t.Changed {
		lead = "This checkout changed since you trusted it"
	}
	return "Withheld\n" + lead + ", so its " + joinAnd(t.Withheld) +
		" are not in this session.\n/trust loads them from the next session on."
}

// trustCommand is `/trust`: the answer, and `/trust off` to withdraw it.
func (m Model) trustCommand(args []string) string {
	manage := m.trust().Manage
	if manage == nil {
		return "Trust is not answered from this session; `shhh doctor trust` records it."
	}
	return manage(args)
}
