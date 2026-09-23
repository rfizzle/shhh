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
	"github.com/spf13/cobra"
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
	// told marks that a session in this process has already said what
	// changed and re-stamped the row. `shhh serve` opens many sessions over
	// one held reading, and the notice is once per change, not once per
	// session that reads the same stale reading.
	told bool
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
	trustHeld.told = false
	trustHeld.mu.Unlock()
}

// changeTold reports whether a session in this process already said what
// changed (restampProjectTrust).
func changeTold() bool {
	trustHeld.mu.Lock()
	defer trustHeld.mu.Unlock()
	return trustHeld.told
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
	if err := db.TrustProject(t.Root, t.Fingerprint, t.DigestNames()); err != nil {
		return "", err
	}
	forgetProjectTrust()
	return "This checkout is trusted: " + declares(t) + " load, and go on loading as " +
		strings.Join(project.ResourceNames(), ", ") + " change. A change is said once, at the next session; " +
		"`shhh trust off` withdraws the answer.", nil
}

// restampProjectTrust moves a trusted checkout's record to the digests it
// stands at now, once this session has read what changed. It is what makes
// the notice a notice: the session that says a suite moved is the last one
// that says so. It writes nothing for a checkout nobody trusted, and leaves
// the held reading alone — this session's screens are still telling the
// reader what it found.
func restampProjectTrust() {
	t := projectTrust()
	if !t.Granted || !t.Outdated || t.Root == "" || changeTold() {
		return
	}
	trustHeld.mu.Lock()
	trustHeld.told = true
	trustHeld.mu.Unlock()
	db, err := openStore()
	if err != nil {
		return
	}
	defer db.Close()
	// A re-stamp that fails costs the next session the same notice again,
	// which is the safe way for it to fail.
	_, _ = db.RestampProject(t.Root, t.Fingerprint, t.DigestNames())
}

// newTrustCmd is `shhh trust [off]`: the answer for a terminal that is not a
// session, spelled the way `/trust [off]` spells it inside one. It is a verb
// of its own rather than a line under the doctor because trust is a decision
// about the repository, made once, and a decision is not a diagnostic.
func newTrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust [off]",
		Short: "Let this checkout's skills, agent profiles, quality suites and servers load",
		Long: "Record that the checkout you are standing in may put what it declares into a session: its skills, " +
			"agent profiles, quality suites, hooks, MCP servers, settings, wordings and backlog profile. They run as you. " +
			"The answer is about the checkout, so it holds while those files change; the next session after a change " +
			"says once what moved. `shhh trust off` withdraws it.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || (len(args) == 1 && args[0] == "off") {
				return nil
			}
			return errors.New("usage: shhh trust [off]")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("the local store is unavailable, so trust cannot be recorded: %w", err)
			}
			defer db.Close()
			note, err := setProjectTrust(db, projectTrust(), len(args) == 0)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), note)
			return nil
		},
	}
}

// declares names what the checkout puts into a session, or says that it puts
// nothing there yet — a repository that declares none of this is still worth
// answering for, because the answer covers what it writes later too.
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
	return chat.Trust{Withheld: t.WithheldNames(), Changed: t.ChangedNames(), Granted: t.Allows(), Manage: trustManager(db)}
}

// trustStartupNote is the line a session prints before it starts when the
// checkout was holding something back, or when a checkout the person trusted
// changed since a session last read it. It is one line on stderr for the
// same reason a server that did not connect is: a session quietly missing
// the skills and the gate the repository ships is a session whose behaviour
// nobody can account for, and a suite whose command line a pull rewrote is
// the one change worth telling even though it loads. Nothing when there was
// nothing to say.
func trustStartupNote() string {
	t := projectTrust()
	if names := t.WithheldNames(); len(names) > 0 {
		return "trust: this checkout is not trusted, so its " + joinAnd(names) +
			" are not in this session — `shhh trust` loads them"
	}
	if names := t.ChangedNames(); len(names) > 0 && !changeTold() {
		return "trust: this checkout's " + joinAnd(names) +
			" changed since you trusted it, and load as they are now — `shhh trust off` withdraws the answer"
	}
	return ""
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
		f := doctorFinding{Subject: "trusted", Detail: detail, Outcome: "ok"}
		// The standing a session will report, read and never written: the
		// re-stamp is the session's, so the doctor can be run as often as
		// anyone likes without swallowing the notice.
		if changed := t.ChangedNames(); len(changed) > 0 {
			f.Outcome = "changed"
			f.Consequence = "its " + joinAnd(changed) + " changed since a session here last read them, and load as they are now"
			f.Fix = []string{"shhh trust off   # if that change is not one you trust"}
			f.FixLabel = "show the line"
		}
		return f
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
	f.Fix = []string{"shhh trust   # or [a] on this row, or /trust in a session", "one answer covers the whole checkout, not one file"}
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
