package eval

// `shhh chat`: a conversation that promises to change nothing.
//
// A chat case is shaped like a workspace case and measured the same way, by
// starting the binary under measurement in a copy of the fixture. It differs
// in two places. Its verdict is not a command run afterwards but the facts the
// answer has to state, matched against the close line's final text; and the
// promise chat makes is observed rather than assumed. Chat's toolset has no
// mutating tool, so a model cannot call one; what it can do is ask for a tool
// it was never given, and that comes back as a result of class unknown, which
// the built-in no-mutation assertion counts (assert.go).
// See docs/capabilities/evals.md#the-other-modes-are-measured-too.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/rfizzle/shhh/internal/observe/record"
)

// KindChat is a question put to `shhh chat --print` in a copy of a fixture,
// decided by the facts its answer states and the built-in no-mutation.
const KindChat Kind = "chat"

// workspaceShaped reports whether this kind is measured by a session in a copy
// of a fixture: the empty kind, a workspace case, and a chat case.
func (k Kind) workspaceShaped() bool {
	return k == KindWorkspace || k == "" || k == KindChat
}

// sessionArgv is the command line of the session an attempt starts, past the
// binary. A chat session is not given --yes: a conversation's reads run
// unasked, and a flag that silenced a prompt chat never raises would only
// make the case read as one that approved something.
//
// A case with a persona is served, and its prompt is the first turn it puts
// rather than an argument (serve.go).
func (c Case) sessionArgv(forwarded []string) []string {
	var args []string
	if c.persona != nil {
		return append([]string{"serve", "--stdio"}, forwarded...)
	}
	if c.Kind == KindChat {
		args = []string{"chat", "--print", "--output", "jsonl"}
	} else {
		args = []string{"code", "--print", "--output", "jsonl", "--yes"}
	}
	args = append(args, forwarded...)
	return append(args, c.Prompt)
}

// withBuiltIn puts the assertion a kind always makes ahead of the ones the
// case declared, unless the case already declared it.
func withBuiltIn(declared []Assertion, word string) []Assertion {
	for _, a := range declared {
		if a.Word == word {
			return declared
		}
	}
	return append([]Assertion{{Word: word}}, declared...)
}

// finalText is the close line's final answer as text. A run that was asked
// for a checked answer states it as JSON in the text's place, and that is
// matched as the text it is written as.
func (t trace) finalText() (string, bool) {
	for i := len(t.events) - 1; i >= 0; i-- {
		if ev := t.events[i]; ev.Kind == record.EventClose {
			return ev.finalText()
		}
	}
	return "", false
}

// finalText is one close line's final answer as text.
func (ev event) finalText() (string, bool) {
	var s string
	if json.Unmarshal(ev.Final, &s) == nil {
		return s, true
	}
	return strings.TrimSpace(string(ev.Final)), len(ev.Final) > 0
}

// factsMissing is how many of the facts the answer does not state.
func factsMissing(facts []*regexp.Regexp, answer string) int {
	n := 0
	for _, re := range facts {
		if !re.MatchString(answer) {
			n++
		}
	}
	return n
}

// gradeChat is a chat attempt's check: every fact stated. The output is a
// count, never the answer or the fact, so what a report prints is the same
// kind of thing a finding is.
func gradeChat(facts []*regexp.Regexp, t trace) (passed bool, output string) {
	answer, _ := t.finalText()
	if n := factsMissing(facts, answer); n > 0 {
		return false, fmt.Sprintf("%d of %d facts missing from the answer", n, len(facts))
	}
	return true, ""
}
