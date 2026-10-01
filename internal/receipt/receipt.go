// Package receipt says what one call did, in the closed vocabulary every
// front-end draws it with: the kind of act, its verb, what it was about and
// where, how it came out, and how much it found.
//
// It exists so that a front-end holds no table of tools. A tool returns a
// string and knows nothing of how it is drawn; the screen used to know every
// tool by name instead, in four tables and a dozen comparisons, so a new tool
// joined by editing the screen and a redrawn transcript had to carry every
// one of those tables across. Read here once, from the call and its result,
// the facts a row states are one reading every front-end shares, and drawing
// them differently is a change to the renderer and to nothing else.
// See docs/architecture.md#one-agent-several-front-ends.
//
// It is not the digest, though it reads it. The digest is the content-free
// account the summariser is handed — a name, a target and an outcome word,
// never a byte of output — because what it feeds steers the agent
// (docs/capabilities/coding-agent.md#the-verdict-is-a-steering-signal-so-the-digest-is-a-boundary).
// A receipt is read by front-ends, which show the person what came back, so
// it may read what the digest may not: how many matches a search found, the
// line a commit answered with, the suite a gate turned out to be. Nothing
// here is ever sent to a model. Nor is it the session record's vocabulary,
// which is codes for export, or the evidence store, which keeps output.
package receipt

import (
	"fmt"

	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/receipt/describe"
	"github.com/rfizzle/shhh/internal/tools"
)

// Kind is what sort of act a call was: the closed set a front-end chooses a
// glyph and the mutation rail by. It is declared where the tools that are
// those acts can import it.
type Kind = describe.Kind

// The kinds, each described where it is declared.
const (
	KindRead    = describe.KindRead
	KindSearch  = describe.KindSearch
	KindLookup  = describe.KindLookup
	KindRun     = describe.KindRun
	KindWrite   = describe.KindWrite
	KindSpawn   = describe.KindSpawn
	KindRemote  = describe.KindRemote
	KindReport  = describe.KindReport
	KindSummary = describe.KindSummary
)

// Call is one call as a receipt is read from it.
type Call struct {
	// Name is the tool the model called. It is empty for a command the
	// runner ran, which Exec marks.
	Name string
	// Args is the call's arguments as the model wrote them, and for a
	// command the command line.
	Args string
	// Result is what came back, or nothing yet.
	Result string
	// Exec is how a command ended. It is nil on every tool call.
	Exec *tools.ExecResult
	// Served reports whether Name is a tool of an MCP server this session
	// registered, and ReadOnly whether the person marked that server
	// read-only. They are inputs because the answer is the session's
	// configuration, not anything the call or its name carries.
	Served, ReadOnly bool
	// Hunks is the change an edit applied, from the diff the caller already
	// holds for it, and Path the file it was applied to where the caller
	// holds the change rather than the arguments. A write's hunk head is read
	// from these and never from the result, which is the tool's sentence
	// about the change rather than the change.
	Hunks []diff.Hunk
	Path  string
	// Attachments is what the result carried that is not text — a read of
	// an image file hands the picture back beside its sentence. A front-end
	// needs the bytes to open it, and the result string never holds them.
	Attachments []provider.Attachment
}

// Receipt is what one call did, as a row states it.
type Receipt struct {
	Kind Kind
	// Verb is the act in the closed verb vocabulary
	// (docs/interface/principles.md#closed-vocabularies). A tool with no
	// word of its own is its own name, which is the signal that it declared
	// none or that the vocabulary has a hole where it is.
	Verb string
	// Subject is what the act was about: a path, a pattern and its place, a
	// command line, a server and its tool.
	Subject string
	// Scope is the tail of Subject that says where the act was put rather
	// than what it was about — a search's directory behind its pattern —
	// and is empty wherever the subject is its own whole.
	Scope string
	// Outcome is how the call came out where the result alone says so:
	// "error" for a tool that answered with one, a git write's own receipt
	// line, a report's link. It is empty where how the call came out is the
	// session's to say — still running, refused, cancelled — and on a
	// command, whose ending is Ended.
	Outcome string
	// Account qualifies the outcome: for a command that never started, the
	// prerequisite it was missing.
	Account string
	// Count is how much the call found, in Noun — the form that agrees with
	// it — and Short reports that the tool stopped before it had found it
	// all. Noun is empty on a call that found nothing worth counting.
	Count int
	Noun  string
	Short bool
	// Ended is how a command ended, resolved from the runner's answer. It is
	// the zero value on every tool call.
	Ended tools.ExecResult
	// Hunk is the head of the change an edit applied, and nil on a call that
	// carried none.
	Hunk *HunkHead
	// Picture is the image the call's result carried, which a front-end
	// opens the way it opens one the reader attached, and nil on a call that
	// returned none.
	Picture *provider.Attachment

	tool    string
	arg     string
	args    string
	path    string
	result  string
	gitVerb string
	command bool
	failed  bool
}

// Rail reports whether the act carries the mutation rail.
func (r Receipt) Rail() bool { return r.Kind.Rail() }

// Counts is the count as a row's counts field reads: `12 matches`, and
// `50+ matches` for a tool that stopped short, because the one thing worth
// saying about a truncated answer is that there is more of it. It is blank
// rather than `0` for a call that found nothing, for the reason a duration
// is blank below its threshold: a column of zeroes is noise.
func (r Receipt) Counts() string {
	switch {
	case r.Noun == "":
		return ""
	case r.Short:
		return fmt.Sprintf("%d+ %s", r.Count, r.Noun)
	}
	return fmt.Sprintf("%d %s", r.Count, r.Noun)
}

// Title is the call named the way the screen that shows its whole output
// names it: the verb and the plain argument, or for a command the line that
// ran. A gate's title is the suite asked for, not what the verdict made of
// it, since the screen is the output the verdict is part of.
func (r Receipt) Title() string {
	if r.command {
		return "$ " + r.Subject
	}
	if r.arg == "" {
		return r.Verb
	}
	return r.Verb + " " + r.arg
}

// Path is the file or directory the call named: the path the caller handed
// in with a change, or else the path argument the model wrote. It is read on
// asking rather than when the receipt is built, because every row is
// rebuilt on every paint and only a step's rollup wants it.
func (r Receipt) Path() string {
	if r.path != "" {
		return r.path
	}
	return digest.Path(r.args)
}

// IsGitWrite reports whether the call was the writing half of git.
func (r Receipt) IsGitWrite() bool { return IsGitWrite(r.tool) }

// IsCommit reports whether the call was a git write that committed. A call
// whose arguments could not be read is not one, even though its verb falls
// back to `commit`: the fallback is a word for a row, not a fact about the
// call.
func (r Receipt) IsCommit() bool { return r.IsGitWrite() && r.gitVerb == commitVerb }

// SwitchesBranch reports whether the call was a git write that switched
// branches, which is the one call that moves the checkout under the session.
func (r Receipt) SwitchesBranch() bool { return r.IsGitWrite() && r.gitVerb == switchVerb }

// IsReport reports whether the call published a report page.
func (r Receipt) IsReport() bool { return r.Kind == KindReport }

// Failed reports whether the tool answered with an error.
func (r Receipt) Failed() bool { return r.failed }

// OutcomeError is the outcome of a tool that answered with an error.
const OutcomeError = "error"
