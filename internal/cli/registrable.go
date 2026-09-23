package cli

import (
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/web"
)

// registrableDefinitions is every optional tool definition a surface can put
// in front of a model, beyond the base toolset BuildAgent describes itself.
// A registration joins this list in the same change that adds it, because
// the list is what the toolbox and the argument descriptions are held to: a
// tool registered anywhere else reaches the model as whatever its schema
// says, and nothing fails when that is a bare schema.
//
// It opens nothing. Each definition is built the way its surface builds it,
// from inputs that need no language server, binary, key, store or listener:
// the language-server and web toolsets over no manager and no fetcher, the
// structural set as if every binary were found, a one-shot report publisher
// with no store. Two definitions are shaped by what the session loaded, and
// those take their input from the caller: the skill tool's name enum comes
// from the catalog, and the spawn tool's role enum from the profile set.
//
// MCP server tools are not here and cannot be: their names and descriptions
// are the server's. The one MCP definition shhh writes itself, the resource
// read, is.
func registrableDefinitions(skills *skill.Catalog, profiles subagent.Profiles) []provider.Tool {
	var defs []provider.Tool
	defs = append(defs, lsp.NewToolset(nil).Definitions()...)
	defs = append(defs, structural.Registrable()...)
	defs = append(defs, web.NewToolset(nil, &web.Searcher{}).Definitions()...)
	defs = append(defs,
		mcp.ResourceDefinition(),
		process.Definition(),
		quality.ToolDefinition(),
		reports.NewPublisher(nil, "", "", reports.OneShot, false).ToolDefinition(),
	)
	defs = append(defs, notebook.Definitions()...)
	defs = append(defs, subagent.Definitions(profiles)...)
	defs = append(defs,
		evidence.ToolDefinition(),
		memory.ToolDefinition(),
		skill.ToolDefinition(skills),
		ask.ToolDefinition(),
		todo.ExtractTool(todo.BuiltinCode()),
		persona.DraftTool(),
	)
	return defs
}
