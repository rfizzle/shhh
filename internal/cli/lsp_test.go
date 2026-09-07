package cli

// Which calls the diagnostics hook asks the language server about. The
// manager it would ask needs a server on the other end, so what is pinned
// here is the reading that decides — the half that can be wrong with nothing
// failing, because a call the hook passes over is a call nobody hears about.

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

func TestLSPTouchedPath(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		args   string
		result string
		want   string
	}{
		{
			name: "a write names the file it wrote", tool: tools.WriteFileName,
			args:   `{"path":"internal/agent/loop.go","content":"package agent"}`,
			result: "wrote internal/agent/loop.go (1 line)", want: "internal/agent/loop.go",
		},
		{
			name: "an edit names the file it changed", tool: tools.EditFileName,
			args:   `{"path":"main.go","old_text":"a","new_text":"b"}`,
			result: "edited main.go", want: "main.go",
		},
		{
			name: "a read changed nothing", tool: "read_file",
			args: `{"path":"main.go"}`, result: "package main", want: "",
		},
		{
			name: "a command is not a file", tool: tools.ExecCommandName,
			args: `{"command":"go build ./..."}`, result: "ok", want: "",
		},
		{
			name: "a write that failed left the file as it was", tool: tools.WriteFileName,
			args: `{"path":"main.go","content":"x"}`, result: "error: permission denied", want: "",
		},
		{
			name: "arguments that do not parse name nothing", tool: tools.WriteFileName,
			args: `{"path":`, result: "wrote main.go", want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lspTouchedPath(tc.tool, json.RawMessage(tc.args), tc.result)
			if got != tc.want {
				t.Fatalf("lspTouchedPath(%s) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// Every mutating tool reaches the hook, so the day a third one is registered
// the files it writes are checked without this file being touched.
func TestLSPTouchedPathCoversEveryMutatingTool(t *testing.T) {
	for _, d := range tools.Mutating() {
		name := d.Tool.Name
		got := lspTouchedPath(name, json.RawMessage(`{"path":"x.go"}`), "done")
		if got != "x.go" {
			t.Errorf("%s writes a file the hook never asks about: got %q", name, got)
		}
	}
}
