package cli

import (
	"encoding/json"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

// repeatShapes is how shhh's tools read to the repeat detector: which one is a
// question, which ones search a place, and which ones write. Every constructor
// of a detector passes it. A detector built without it still counts the exact
// repeat, but silently stops telling a question from any other call and a
// sweep from a run of unrelated ones, which nothing would report.
func repeatShapes() agent.Shapes {
	return agent.Shapes{
		Question:    repeatQuestion,
		SearchScope: digest.SearchScope,
		Wrote:       repeatWrote,
	}
}

// repeatQuestion identifies a question by its text alone, so a re-ask with the
// options reworded is caught as well.
func repeatQuestion(tool string, args json.RawMessage) (string, bool) {
	if tool != ask.ToolName {
		return "", false
	}
	return ask.QuestionText(args), true
}

// repeatWrote reports whether a call changed anything, which is what ends a
// sweep: a run that has written something is acting on what it found,
// whatever it searched for before that.
//
// The file writes are read the way every surface that keeps no changeset
// reads them, so a third one of those ends a sweep the day it is registered
// rather than the day somebody remembers this line. The other two are named
// because they are registered elsewhere and no shared reading covers them: a
// command, because shhh cannot know whether a command wrote anything and
// assumes it did, and the writing half of git, whose four verbs all change
// the repository. Missing either one would let a run that searched a dozen
// times, committed, and searched a dozen more be told it had been going in
// one circle of twenty-four.
//
// A question is on neither list and must stay off both: nothing on the
// machine changed, so a question neither ends a sweep of searches nor starts
// one, and a run that stopped to ask in the middle of an investigation is
// still on the same investigation.
// See docs/interface/principles.md#weight-tracks-risk.
func repeatWrote(tool, rawArgs string) bool {
	return tool == tools.ExecCommandName ||
		tool == structural.GitWriteToolName ||
		len(tools.WrittenPaths(tool, rawArgs)) > 0
}
