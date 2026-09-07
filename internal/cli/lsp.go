package cli

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// openLSP builds the session's LSP toolset: auto-detected language
// servers rooted at the working directory. nil — a clean no-op — when the
// integration is disabled or no known server binary is on PATH.
func openLSP(cfg config.Config) *lsp.Toolset {
	if cfg.LSP.Disabled {
		return nil
	}
	specs := lsp.DetectServers()
	if len(specs) == 0 {
		return nil
	}
	root, err := os.Getwd()
	if err != nil {
		return nil
	}
	return lsp.NewToolset(lsp.NewManager(root, specs, lsp.Options{
		RequestTimeout:     time.Duration(cfg.LSP.RequestTimeoutSeconds) * time.Second,
		DiagnosticsTimeout: time.Duration(cfg.LSP.DiagnosticsTimeoutSeconds) * time.Second,
	}))
}

// lspMutationHook appends fresh language-server diagnostics for the touched
// file to an applied write/edit result, bounded and errors-first, so the
// model self-corrects in the same round. nil toolset means no hook.
//
// A mutation never reaches the session executor, so this is the second place
// a late answer catches up: an edit that follows the one whose check ran long
// carries that check's verdict in front of its own result. This file's own
// question is re-asked before the held ones are collected, so a re-edit
// replaces the answer outstanding for it rather than printing a stale block
// above a fresh one about the same lines.
// See docs/capabilities/coding-agent.md#diagnostics-that-arrive-late-still-arrive.
func lspMutationHook(ts *lsp.Toolset) chat.MutationHook {
	if ts == nil {
		return nil
	}
	return func(name string, args json.RawMessage, result string) string {
		path := lspTouchedPath(name, args, result)
		if path == "" {
			return result
		}
		fresh := ts.Manager.DiagnosticsAfterChange(path)
		if held := ts.Manager.TakeHeldDiagnostics(); held != "" {
			result = held + "\n\n" + result
		}
		if fresh != "" {
			result += "\n\n" + fresh
		}
		return result
	}
}

// childMutationHook is lspMutationHook for a sub-agent: the same fresh
// verdict on the file the child just wrote, and nothing at all from the
// session's queue of late answers.
//
// The queue is shared, because the language server is — one per session, not
// one per child, since a `gopls` per writer would be a language server per
// spawn over a copy of the tree the session's own is already indexing. A
// child draining that queue would put a verdict about the session's file, or
// a sibling's, in front of its own result; a child leaving its own question
// in it would do the same to the person's next edit, naming a path in a
// worktree they are not standing in. So a child neither reads the queue nor
// leaves anything in it, and the promise the queue makes — exactly once, in
// front of the next result its reader sees — goes on meaning what it said.
//
// What a child gives up is the late answer to its own edit, which it can ask
// for outright: the diagnostics tool is registered on every child, and an
// edit whose check had not finished says so on the result and names the tool
// to call.
// See docs/capabilities/subagents.md#a-child-searches-with-what-the-session-searches-with.
func childMutationHook(ts *lsp.Toolset) chat.MutationHook {
	if ts == nil {
		return nil
	}
	return func(name string, args json.RawMessage, result string) string {
		path := lspTouchedPath(name, args, result)
		if path == "" {
			return result
		}
		fresh := ts.Manager.DiagnosticsAfterChange(path)
		// Closed whatever the verdict was, because a wait that ran out leaves
		// the question open by design and this reader is never the one who
		// will collect it — including on the round the child is told the file
		// has not been checked yet, which is exactly the round a question was
		// left behind.
		ts.Manager.DropHeld(path)
		if fresh != "" {
			return result + "\n\n" + fresh
		}
		return result
	}
}

// lspTouchedPath is the file a mutation hook should ask the language server
// about, and "" for every call it must leave alone.
//
// Which calls those are is the tools package's own reading and not a second
// copy of it: a hook that named write_file and edit_file itself would go on
// agreeing with the executor until a third mutating tool was registered, and
// then quietly stop asking about the files that one wrote, with nothing
// failing to say so. A write that came back an error changed nothing, and
// diagnostics fetched for it would describe the file as it already was.
func lspTouchedPath(name string, args json.RawMessage, result string) string {
	if strings.HasPrefix(result, "error:") {
		return ""
	}
	return tools.WrittenPath(name, string(args))
}
