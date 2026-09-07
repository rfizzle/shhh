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
