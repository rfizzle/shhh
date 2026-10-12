package cli

// Which calls the diagnostics hook asks the language server about. The
// manager it would ask needs a server on the other end, so what is pinned
// here is the reading that decides — the half that can be wrong with nothing
// failing, because a call the hook passes over is a call nobody hears about.

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/rfizzle/shhh/internal/tools"
)

func TestLSPTouchedPath(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		args   string
		result string
		want   []string
	}{
		{
			name: "a write names the file it wrote", tool: tools.WriteFileName,
			args:   `{"path":"internal/agent/loop.go","content":"package agent"}`,
			result: "wrote internal/agent/loop.go (1 line)", want: []string{"internal/agent/loop.go"},
		},
		{
			name: "an edit names the file it changed", tool: tools.EditFileName,
			args:   `{"path":"main.go","old_text":"a","new_text":"b"}`,
			result: "edited main.go", want: []string{"main.go"},
		},
		{
			name: "an edit across files names each of them", tool: tools.EditFileName,
			args:   `{"paths":["a.go","b.go"],"old_text":"a","new_text":"b"}`,
			result: "Edited 2 files", want: []string{"a.go", "b.go"},
		},
		{
			name: "a read changed nothing", tool: "read_file",
			args: `{"path":"main.go"}`, result: "package main", want: nil,
		},
		{
			name: "a command is not a file", tool: tools.ExecCommandName,
			args: `{"command":"go build ./..."}`, result: "ok", want: nil,
		},
		{
			name: "a write that failed left the file as it was", tool: tools.WriteFileName,
			args: `{"path":"main.go","content":"x"}`, result: "error: permission denied", want: nil,
		},
		{
			name: "arguments that do not parse name nothing", tool: tools.WriteFileName,
			args: `{"path":`, result: "wrote main.go", want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lspTouchedPaths(tc.tool, json.RawMessage(tc.args), tc.result)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("lspTouchedPaths(%s) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// Every mutating tool reaches the hook, so the day a third one is registered
// the files it writes are checked without this file being touched.
func TestLSPTouchedPathCoversEveryMutatingTool(t *testing.T) {
	for _, d := range tools.Mutating() {
		name := d.Tool.Name
		got := lspTouchedPaths(name, json.RawMessage(`{"path":"x.go"}`), "done")
		if !slices.Equal(got, []string{"x.go"}) {
			t.Errorf("%s writes a file the hook never asks about: got %q", name, got)
		}
	}
}
