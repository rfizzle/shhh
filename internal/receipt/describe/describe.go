// Package describe is what a tool declares, beside its definition, about how
// a call to it reads on a receipt: the kind of act, its verb, how to read
// what the call was about and how much it found.
//
// It is a leaf so that the packages defining tools can import it: the
// receipt package reads the tool packages for what they declare, and a tool
// package importing the receipt package to declare it would close the cycle.
// A tool is declared once, where it is defined, so a new tool reaches every
// front-end without a table being edited
// (docs/architecture.md#one-agent-several-front-ends).
package describe

import "fmt"

// Kind is what sort of act a call was: the closed set a front-end chooses a
// glyph and the mutation rail by. Three kinds of read are told apart — a read
// of something, a search through something, a lookup of something — because
// a step is accounted for by them separately; a front-end that draws all
// three alike draws them with one glyph, and the verb says which it was.
type Kind int

const (
	// KindRead read something: a file, a listing, a stored result, git's
	// history, a skill's text.
	KindRead Kind = iota
	// KindSearch looked through many things for some: a pattern, a glob, a
	// query over structured files.
	KindSearch
	// KindLookup asked something that answers: a language server, the web,
	// the person.
	KindLookup
	// KindRun ran a program — a command, a supervised process, the project's
	// own checks, a git write — whose effects shhh cannot see.
	KindRun
	// KindWrite changed a file, or persisted something: an edit, a write, a
	// patch, a memory.
	KindWrite
	// KindSpawn is a sub-agent: started, read, steered or retried.
	KindSpawn
	// KindRemote is a call to a server the person did not mark read-only.
	// shhh cannot see the far side of it, so it is assumed to have acted
	// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).
	KindRemote
	// KindReport published a report page into shhh's own store.
	KindReport
	// KindSummary is the one kind no tool call has: the summariser's reading
	// of the session, which a front-end accounts for beside the calls.
	KindSummary
)

var kindWords = [...]string{
	KindRead:    "read",
	KindSearch:  "search",
	KindLookup:  "lookup",
	KindRun:     "run",
	KindWrite:   "write",
	KindSpawn:   "spawn",
	KindRemote:  "remote",
	KindReport:  "report",
	KindSummary: "summary",
}

func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindWords) {
		return kindWords[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Rail reports whether the act carries the mutation rail: it changed the
// workspace, or shhh cannot know that it did not and so assumes it did. A
// report's store is shhh's own state rather than the workspace, so it does
// not (docs/interface/principles.md#weight-tracks-risk).
func (k Kind) Rail() bool {
	return k == KindRun || k == KindWrite || k == KindRemote
}

// Call is one call as a describer's readers see it. It is a struct rather
// than the readers' arguments so that what a call carries can grow without
// every reader's signature changing.
type Call struct {
	// Name is the tool the model called.
	Name string
	// Args is the call's arguments as the model wrote them.
	Args string
	// Arg is the call's plain argument as the digest reads it: the subject a
	// row states when the describer has no reading of its own.
	Arg string
	// Result is what came back, or nothing yet.
	Result string
}

// Describer is what one tool declares about how its calls read.
type Describer struct {
	Kind Kind
	// Verb is the act in the closed verb vocabulary
	// (docs/interface/principles.md#closed-vocabularies). Empty is a tool
	// with no word of its own yet, which reads as its own name: the signal
	// that the vocabulary has a hole where this tool is.
	Verb string
	// Subject reads what the call was about. Nil reads Call.Arg.
	Subject func(Call) string
	// Count reads how much the call found, with the noun that agrees with
	// it, and whether the tool stopped before it had found it all. Nil
	// counts the result's lines. It is never asked of an empty result or of
	// a reader's "nothing here" sentence, which count as nothing.
	Count func(Call) (n int, noun string, short bool)
	// Does is what the tool does, in a clause, for a screen that offers the
	// tool rather than draws a call to it: a profile's tool picker. Empty
	// says nothing.
	Does string
}

// Unworded is the describer of a tool that has no word in the closed verb
// vocabulary: it reads as its own name, as a read, counted in lines, which is
// what a tool that declared nothing reads as. Declaring it says the reading
// was chosen rather than forgotten; choosing a word for the tool is a change
// to what the screen draws.
var Unworded = Describer{}

// Uncounted is the count of a call whose answer is not a quantity of
// anything — a decision, a receipt line — so that measuring its lines would
// measure the sentence rather than anything that happened.
func Uncounted(Call) (int, string, bool) { return 0, "", false }

// Measured is a count with the noun that agrees with it: the plural for a
// count the tool stopped short of, since `50+` is always more than one.
func Measured(n int, more bool, singular, plural string) (int, string, bool) {
	if n == 1 && !more {
		return n, singular, more
	}
	return n, plural, more
}
