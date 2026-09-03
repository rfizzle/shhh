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
		if name != tools.WriteFileName && name != tools.EditFileName {
			return result
		}
		if strings.HasPrefix(result, "error:") {
			return result
		}
		var a struct {
			Path string `json:"path"`
		}
		fresh := ""
		if err := json.Unmarshal(args, &a); err == nil && a.Path != "" {
			fresh = ts.Manager.DiagnosticsAfterChange(a.Path)
		}
		if held := ts.Manager.TakeHeldDiagnostics(); held != "" {
			result = held + "\n\n" + result
		}
		if fresh != "" {
			result += "\n\n" + fresh
		}
		return result
	}
}
