package cli

import (
	"encoding/json"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/web"
)

// unattendedGate is which of a run's calls are put to a decision rather than
// dispatched outright, for every surface that has no card to draw: the
// scripted run, and a session a client drives over the protocol.
//
// It is one function and not one per surface. This is the line between the
// tier that runs on its own and the tier that has to be answered for
// (docs/architecture.md#tiers-not-permissions), and a second copy of it
// agrees on the day it is written: the next tool that has to be answered for
// is added to whichever copy the author was looking at, and the other surface
// runs it unasked with nothing going red.
//
// Each of the three registrations is a gate of its own because each has its
// own answer. A fetch is an external action; a server call is gated unless
// the server was declared read-only; and the process tool gates on its
// arguments rather than its name, since only a start needs an answer and
// status, read, input and stop do not.
func unattendedGate(webTools *web.Toolset, procSup *process.Supervisor, mcpTools *mcp.Toolset) agent.ApprovalGate {
	return func(tc provider.ToolCall) bool {
		if webTools != nil && tc.Name == web.FetchToolName {
			return true
		}
		if procSup != nil && tc.Name == process.ToolName {
			return process.NeedsApproval(json.RawMessage(tc.Arguments))
		}
		if mcpTools != nil && mcpTools.Has(tc.Name) {
			return !mcpTools.ReadOnly(tc.Name)
		}
		return headlessGate(tc.Name)
	}
}
