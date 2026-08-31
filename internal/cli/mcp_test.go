package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func TestMCPDefinitionsInferTransport(t *testing.T) {
	cfg := config.Config{MCP: config.MCPConfig{Servers: map[string]config.MCPServer{
		"cmd":  {Command: "npx", Args: []string{"-y", "x"}, ReadOnly: true},
		"web":  {URL: "https://x/mcp", Headers: map[string]string{"Authorization": "Bearer ${T}"}},
		"old":  {URL: "https://x/sse", Type: "sse"},
		"none": {},
	}}}
	defs := mcpDefinitions(cfg)
	got := map[string]mcp.Transport{}
	for _, d := range defs {
		got[d.Name] = d.Transport
		if d.Scope != mcp.ScopeUser {
			t.Errorf("%s: scope %s", d.Name, d.Scope)
		}
	}
	want := map[string]mcp.Transport{"cmd": mcp.TransportStdio, "web": mcp.TransportHTTP, "old": mcp.TransportSSE, "none": ""}
	for name, tr := range want {
		if got[name] != tr {
			t.Errorf("%s: transport %q, want %q", name, got[name], tr)
		}
	}
	if defs[0].Name != "cmd" || !defs[0].ReadOnly {
		t.Errorf("definitions are not sorted by name or lost read_only: %+v", defs[0])
	}
}

func TestMCPFindingReadsEachStatus(t *testing.T) {
	def := mcp.Definition{Name: "gh", Scope: mcp.ScopeProject, Transport: mcp.TransportStdio, Command: "npx", Args: []string{"mcp-remote"}, Source: "/repo/.mcp.json"}
	cases := []struct {
		report  mcp.Report
		state   components.DoctorState
		outcome string
		fix     string
	}{
		{mcp.Report{Definition: def, Status: mcp.StatusUntrusted}, components.DoctorWarned, "untrusted", "shhh mcp trust gh"},
		{mcp.Report{Definition: def, Status: mcp.StatusChanged}, components.DoctorWarned, "changed", "shhh mcp trust gh"},
		{mcp.Report{Definition: def, Status: mcp.StatusFailed, Error: "server gh: connect: boom"}, components.DoctorFailed, "failed", "boom"},
		{mcp.Report{Definition: def, Status: mcp.StatusMissingEnv, Missing: []string{"TOKEN"}}, components.DoctorWarned, "unset: TOKEN", "export TOKEN=..."},
		{mcp.Report{Definition: def, Status: mcp.StatusDisabled}, components.DoctorSkipped, "disabled", "\"disabled\": false"},
		{mcp.Report{Definition: def, Status: mcp.StatusExcluded}, components.DoctorSkipped, "not read-only", "read-only is your word"},
	}
	for _, c := range cases {
		f := mcpFinding(c.report, "/repo", nil)
		if f.State != c.state || f.Outcome != c.outcome {
			t.Errorf("%s: state %d outcome %q, want %d %q", c.report.Status, f.State, f.Outcome, c.state, c.outcome)
		}
		if f.Consequence == "" {
			t.Errorf("%s: no consequence", c.report.Status)
		}
		if !strings.Contains(strings.Join(f.Fix, "\n"), c.fix) {
			t.Errorf("%s: fix %v lacks %q", c.report.Status, f.Fix, c.fix)
		}
		if f.Subject != "gh" || !strings.Contains(f.Detail, "npx mcp-remote") || !strings.Contains(f.Detail, "project") {
			t.Errorf("%s: subject %q detail %q", c.report.Status, f.Subject, f.Detail)
		}
		// Trust is offered only where it can be recorded: the store is nil
		// here, so no row offers a key.
		if f.Action != "" {
			t.Errorf("%s: offered %q without a store", c.report.Status, f.Action)
		}
	}
}

// Every report becomes a source the rail can draw, and a failure carries why
// rather than the word "failed" the state itself already says.
func TestMCPToolSourcesReadEachStatus(t *testing.T) {
	def := mcp.Definition{Name: "gh", Scope: mcp.ScopeProject, Transport: mcp.TransportStdio, Command: "npx"}
	cases := []struct {
		report mcp.Report
		state  components.ToolSourceState
		note   string
	}{
		{mcp.Report{Definition: def, Status: mcp.StatusConnected, Server: &mcp.Server{Tools: []mcp.Tool{{Name: "a"}, {Name: "b"}}}}, components.ToolSourceUp, "2 tools"},
		{mcp.Report{Definition: def, Status: mcp.StatusFailed, Error: "server gh: connect: boom\n    at line 2"}, components.ToolSourceFailed, "server gh: connect: boom"},
		{mcp.Report{Definition: def, Status: mcp.StatusUntrusted}, components.ToolSourceBlocked, "untrusted"},
		{mcp.Report{Definition: def, Status: mcp.StatusChanged}, components.ToolSourceBlocked, "changed"},
		{mcp.Report{Definition: def, Status: mcp.StatusMissingEnv, Missing: []string{"TOKEN"}}, components.ToolSourceBlocked, "unset: TOKEN"},
		{mcp.Report{Definition: def, Status: mcp.StatusDisabled}, components.ToolSourceOff, ""},
		{mcp.Report{Definition: def, Status: mcp.StatusExcluded}, components.ToolSourceOff, "not read-only"},
	}
	for _, c := range cases {
		got := mcpToolSources(&mcp.Toolset{Reports: []mcp.Report{c.report}})
		if len(got) != 1 || got[0].Name != "gh" {
			t.Fatalf("%s: sources = %+v", c.report.Status, got)
		}
		if got[0].State != c.state || got[0].Note != c.note {
			t.Errorf("%s: state %d note %q, want %d %q", c.report.Status, got[0].State, got[0].Note, c.state, c.note)
		}
	}
	if got := mcpToolSources(nil); got != nil {
		t.Errorf("a session with no servers has no sources: %+v", got)
	}
}

func TestMCPListingSaysWhatEachServerBecame(t *testing.T) {
	if got := mcpListing(nil, nil, ""); !strings.Contains(got, "No MCP servers defined") {
		t.Errorf("empty listing = %q", got)
	}
	ts := &mcp.Toolset{Reports: []mcp.Report{
		{Definition: mcp.Definition{Name: "keyed", Scope: mcp.ScopeUser, Transport: mcp.TransportHTTP, URL: "https://x/mcp"}, Status: mcp.StatusMissingEnv, Missing: []string{"X_TOKEN"}},
		{Definition: mcp.Definition{Name: "proj", Scope: mcp.ScopeProject, Transport: mcp.TransportStdio, Command: "npx"}, Status: mcp.StatusUntrusted},
	}}
	got := mcpListing(ts, &mcp.Catalog{Diagnostics: []string{"/repo/.mcp.json: server Bad Name: bad"}}, "/repo")
	for _, want := range []string{"⚠  keyed", "unset: X_TOKEN", "export X_TOKEN=...", "⚠  proj", "shhh mcp trust proj", "Bad Name", "0 servers connected"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing lacks %q:\n%s", want, got)
		}
	}
}

func TestMCPAddRefusesACredentialValue(t *testing.T) {
	for _, v := range []string{"Bearer ghp_abcdefghijklmnopqrstuvwxyz", "sk-ant-api03-xxxxxxxxxxxx", "Bearer eyJhbGciOiJIUzI1NiJ9.abcdef"} {
		if !looksLikeToken(v) {
			t.Errorf("%q not flagged", v)
		}
	}
	for _, v := range []string{"Bearer ${GH_TOKEN}", "https://api.example.test/mcp", "application/json", "Bearer x"} {
		if looksLikeToken(v) {
			t.Errorf("%q flagged", v)
		}
	}
	if _, err := pairs([]string{"novalue"}); err == nil {
		t.Error("a flag without = parsed")
	}
	got, err := pairs([]string{"A=1", "B=x=y"})
	if err != nil || got["A"] != "1" || got["B"] != "x=y" {
		t.Errorf("pairs = %v %v", got, err)
	}
}

func TestMCPStartupNotesNameOnlyWhatDidNotConnect(t *testing.T) {
	ts := &mcp.Toolset{Reports: []mcp.Report{
		{Definition: mcp.Definition{Name: "off"}, Status: mcp.StatusDisabled},
		{Definition: mcp.Definition{Name: "chatless"}, Status: mcp.StatusExcluded},
		{Definition: mcp.Definition{Name: "dead"}, Status: mcp.StatusFailed, Error: "x"},
	}}
	notes := mcpStartupNotes(ts, &mcp.Catalog{Diagnostics: []string{"d1"}})
	if len(notes) != 2 || !strings.Contains(notes[0], "d1") || !strings.Contains(notes[1], "dead: failed") {
		t.Errorf("notes = %v", notes)
	}
}
