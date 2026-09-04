package cli

// Project trust: the one answer that decides whether the skills, agent
// profiles, quality suites, hooks and MCP servers a checkout declares are
// loaded at all. It is read once per process, before the toolset is built,
// and every surface that would load one of those things asks here rather
// than deciding for itself.
// See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// projectTrust is the checkout's standing for this process, read once and
// then held.
//
// Held because four loaders ask — the skills catalog, the agent profiles,
// the quality gate and the MCP connect — before the screens that report what
// they left out ask again, and the read walks the checkout's declared files
// to fingerprint them. Holding it also means they cannot disagree: a session
// that loaded skills under one answer and withheld suites under another
// would be reporting a state that never existed.
//
// It is a variable so a test can state the answer rather than writing a
// checkout and a store to imply one; nothing but a test ever assigns it.
var projectTrust = heldProjectTrust

// trustHeld is the answer this process has already read, and nothing until
// something has asked.
var trustHeld struct {
	mu   sync.Mutex
	read *project.Trust
}

// heldProjectTrust answers from what was already read, reading once.
func heldProjectTrust() project.Trust {
	trustHeld.mu.Lock()
	defer trustHeld.mu.Unlock()
	if trustHeld.read == nil {
		t := readProjectTrust()
		trustHeld.read = &t
	}
	return *trustHeld.read
}

// forgetProjectTrust drops what was held, so the next ask reads the row
// again. The writer calls it because two screens promise a re-run reports
// the machine as it is now: `shhh doctor` re-runs every check when an offer
// is taken rather than printing a line at the foot of a stale one, and
// `shhh mcp` dials again. A row that reads "untrusted" directly under the
// answer the reader just gave on it is worse than no offer at all.
//
// A session already under way is unaffected, and that is what the hold is
// for: its skills, profiles, suites and servers were resolved before the
// first turn, so trusting mid-session still takes effect in the next one,
// which is what every surface says it does.
func forgetProjectTrust() {
	trustHeld.mu.Lock()
	trustHeld.read = nil
	trustHeld.mu.Unlock()
}

// readProjectTrust asks the store what was answered for this checkout.
func readProjectTrust() project.Trust {
	cwd, err := os.Getwd()
	if err != nil {
		return project.Trust{}
	}
	root := project.Root(cwd)
	db, err := openStore()
	if err != nil {
		// No store is no record of an answer, which reads as no answer.
		return project.ReadTrust(root, nil)
	}
	defer db.Close()
	return project.ReadTrust(root, db)
}

// setProjectTrust records or withdraws the checkout's answer and returns
// what to tell the person. It is the only writer of that row: the doctor's
// offer, `shhh mcp`'s row and /trust all land here, so the sentence they
// print and the state they leave cannot drift apart.
func setProjectTrust(db *storage.DB, t project.Trust, trust bool) (string, error) {
	if db == nil {
		return "", errors.New("the local store is unavailable, so trust cannot be recorded")
	}
	if t.Root == "" {
		return "", errors.New("no project root here, so there is nothing to trust")
	}
	if !trust {
		had, err := db.DistrustProject(t.Root)
		if err != nil {
			return "", err
		}
		forgetProjectTrust()
		if !had {
			return "This checkout was not trusted.", nil
		}
		return "This checkout is no longer trusted: " + declares(t) + " will not load.", nil
	}
	if err := db.TrustProject(t.Root, t.Fingerprint); err != nil {
		return "", err
	}
	forgetProjectTrust()
	return "This checkout is trusted at its current state: " + declares(t) +
		" load. An edit to any of " + strings.Join(project.ResourceNames(), ", ") + " asks again.", nil
}

// declares names what the checkout puts into a session, or says that it puts
// nothing there yet — a repository that declares none of this is still worth
// answering for, because writing one of those files later is the edit that
// asks again.
func declares(t project.Trust) string {
	if names := kindNames(t.Present); len(names) > 0 {
		return "its " + joinAnd(names)
	}
	return "what it declares"
}

// joinAnd is a list inside a sentence. The doctor's rows join with a middot
// because they are a listing; a sentence that reads "its skills, quality
// suites load" reads as a sentence with a word missing.
func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// kindNames is a list of resource kinds as a sentence says them.
func kindNames(kinds []project.Kind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// trustManager backs /trust in a session: what is being withheld, and the
// answer. Like trusting a server, it takes effect in the next session —
// the prompt naming the skills and the toolset holding the gate were both
// built when this one started.
func trustManager(db *storage.DB) func(args []string) string {
	return func(args []string) string {
		t := projectTrust()
		switch {
		case len(args) == 0:
			note, err := setProjectTrust(db, t, true)
			if err != nil {
				return err.Error()
			}
			return note + " It takes effect in the next session."
		case len(args) == 1 && args[0] == "off":
			note, err := setProjectTrust(db, t, false)
			if err != nil {
				return err.Error()
			}
			return note + " It takes effect in the next session."
		}
		return "Usage: /trust [off]"
	}
}

// chatTrust is the session's standing as the chat TUI takes it: the withheld
// list for the start screen and /status, and the answer behind /trust.
func chatTrust(db *storage.DB) chat.Trust {
	t := projectTrust()
	return chat.Trust{Withheld: t.WithheldNames(), Changed: t.Changed, Manage: trustManager(db)}
}

// trustStartupNote is the line a session prints before it starts when the
// checkout was holding something back. It is one line on stderr for the same
// reason a server that did not connect is: a session quietly missing the
// skills and the gate the repository ships is a session whose behaviour
// nobody can account for. Nothing when there was nothing to withhold.
func trustStartupNote() string {
	t := projectTrust()
	names := t.WithheldNames()
	if len(names) == 0 {
		return ""
	}
	lead := "this checkout is not trusted"
	if t.Changed {
		lead = "this checkout changed since you trusted it"
	}
	return "trust: " + lead + ", so its " + joinAnd(names) +
		" are not in this session — `shhh doctor trust` loads them"
}

// probeTrust is the doctor's reading of the checkout. The store is opened
// only to learn whether the answer could be recorded at all; the offer on
// the row opens its own, because the row is acted on long after the probe
// returned and a handle held open across a whole doctor run is a lock this
// screen has no reason to take.
func probeTrust(context.Context, config.Config) doctorFinding {
	db, err := openStore()
	if db != nil {
		_ = db.Close()
	}
	return doctorTrust(projectTrust(), err == nil)
}

// doctorTrust is that reading. A trusted checkout and one that declares
// nothing are both fine and say so; a checkout waiting on the person is `⊘`
// with what it is holding back, because withholding is a diagnostic and
// never a fault — the session started, with less in it.
func doctorTrust(t project.Trust, offer bool) doctorFinding {
	names := t.WithheldNames()
	switch {
	case t.Root == "":
		return doctorFinding{
			Subject: "no project here", Detail: "nothing to trust",
			Outcome: "empty", State: components.DoctorSkipped,
		}
	case t.Allows():
		detail := "nothing declared yet"
		if present := kindNames(t.Present); len(present) > 0 {
			detail = strings.Join(present, " · ")
		}
		return doctorFinding{Subject: "trusted", Detail: detail, Outcome: "ok"}
	case len(names) == 0:
		return doctorFinding{
			Subject: "declares nothing that runs", Detail: strings.Join(project.ResourceNames(), " · "),
			Outcome: "empty", State: components.DoctorSkipped,
		}
	}
	f := doctorFinding{
		Subject: countOf(len(names), "kind", "kinds") + " withheld",
		Detail:  strings.Join(names, " · "),
		Outcome: "untrusted", State: components.DoctorSkipped,
		Consequence: "this checkout's " + joinAnd(names) + " are not in a session here until you trust it",
	}
	if t.Changed {
		f.Outcome = "changed"
		f.Consequence = "it changed since you trusted it, so " + joinAnd(names) + " are not loaded"
	}
	f.Fix = []string{"shhh doctor trust   # or [a] on this row, or /trust in a session", "one answer covers the whole checkout, not one file"}
	f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "line", "lines"))
	if !offer {
		return f
	}
	f.Action = "trust this checkout"
	f.ActionPrompt = "Trust " + shortPath(t.Root) + "? Its " + joinAnd(names) +
		" load in sessions here, and run as you."
	f.Apply = func() ([]string, error) {
		db, err := openStore()
		if err != nil {
			return nil, fmt.Errorf("the local store is unavailable, so trust cannot be recorded: %w", err)
		}
		defer db.Close()
		note, err := setProjectTrust(db, t, true)
		if err != nil {
			return nil, err
		}
		return []string{note}, nil
	}
	return f
}
