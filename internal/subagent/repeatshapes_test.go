package subagent

import (
	"encoding/json"

	"github.com/rfizzle/agentcore/repeat"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

// testRepeatShapes is shhh's tools as the repeat detector reads them, the
// same reading internal/cli builds for a real session.
func testRepeatShapes() repeat.Shapes {
	return repeat.Shapes{
		Question: func(tool string, args json.RawMessage) (string, bool) {
			if tool != ask.ToolName {
				return "", false
			}
			return ask.QuestionText(args), true
		},
		SearchScope: digest.SearchScope,
		Wrote: func(tool, rawArgs string) bool {
			return tool == tools.ExecCommandName ||
				tool == structural.GitWriteToolName ||
				len(tools.WrittenPaths(tool, rawArgs)) > 0
		},
	}
}
