package cli

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// heldServers is a dial each server's connect waits on until the test lets it
// go: a server sent on its channel is the server answering, a closed channel
// the dial refused. No process, no listener.
type heldServers map[string]chan *mcp.Server

func holdServers(names ...string) heldServers {
	h := heldServers{}
	for _, n := range names {
		h[n] = make(chan *mcp.Server, 1)
	}
	return h
}

func (h heldServers) dial(ctx context.Context, def mcp.Definition, _ func(string) bool) (*mcp.Server, error) {
	select {
	case s, ok := <-h[def.Name]:
		if !ok {
			return nil, errors.New("connection refused")
		}
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answer lets a server's connect end with the named tools, and a resource
// where one is given.
func (h heldServers) answer(name string, resource string, tools ...string) {
	s := &mcp.Server{}
	for _, t := range tools {
		s.Tools = append(s.Tools, mcp.Tool{Name: name + mcp.Separator + t, Remote: t, Description: "Does " + t + "."})
	}
	if resource != "" {
		s.Resources = []mcp.Resource{{URI: resource, Name: "guide"}}
	}
	h[name] <- s
}

func stdioServer(name string, readOnly bool) mcp.Definition {
	return mcp.Definition{Name: name, Scope: mcp.ScopeUser, Transport: mcp.TransportStdio, Command: name + "-mcp", ReadOnly: readOnly}
}

// settledReport waits for one report to leave starting, reading the toolset
// the way the rail does.
func settledReport(t *testing.T, ts *mcp.Toolset, i int) mcp.Report {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := ts.Reports()[i]; r.Status != mcp.StatusStarting {
			return r
		}
		runtime.Gosched()
	}
	t.Fatalf("report %d never left starting", i)
	return mcp.Report{}
}

// The interactive session opens without waiting on any server: the attach
// returns while every dial is held, each admitted server reads starting, and
// nothing about them has been said to the model. Each still runs out its own
// bound, and one that does is failed with the reason it has always carried.
func TestAttachMCP_ReturnsBeforeAServerAnswers(t *testing.T) {
	h := holdServers("docs", "tracker")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("docs", true), stdioServer("tracker", false)}}
	cat.Servers[1].Timeout = 30 * time.Millisecond
	s := &chatSession{toolDefs: []provider.Tool{{Name: "read_file"}}, promptExtra: "standing"}
	closeAll := s.useMCP(connectMCP(t.Context(), cat, mcp.Options{Dial: h.dial}, false), cat, false)
	t.Cleanup(closeAll)

	reps := s.mcpTools.Reports()
	if reps[0].Status != mcp.StatusStarting {
		t.Fatalf("docs = %s, want starting while its dial is held", reps[0].Status)
	}
	if len(s.toolDefs) != 1 || s.promptExtra != "standing" || !s.mcpJoins {
		t.Fatalf("the attach said something to the model: %v / %q", s.toolDefs, s.promptExtra)
	}
	rows := mcpToolSources(s.mcpTools)
	if rows[0].State != components.ToolSourceStarting || rows[0].Since.IsZero() {
		t.Fatalf("docs row = %+v", rows[0])
	}

	if r := settledReport(t, s.mcpTools, 1); r.Status != mcp.StatusFailed || !r.TimedOut ||
		r.Error != "server tracker: no answer within 30ms" {
		t.Fatalf("tracker = %+v", r)
	}
	if row := mcpToolSources(s.mcpTools)[1]; row.State != components.ToolSourceFailed || row.Note != "timeout" {
		t.Fatalf("tracker row = %+v", row)
	}
	if got := s.mcpTools.Reports()[0].Status; got != mcp.StatusStarting {
		t.Fatalf("docs = %s, its dial is still held", got)
	}
}

// A surface with nobody to watch a rail still waits: every connect ends
// before the attach returns, and the servers' tools and block are in the
// prompt from the first round.
func TestHeadlessMCP_StillConnectsBeforeTheFirstRound(t *testing.T) {
	h := holdServers("docs")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("docs", true)}}
	s := &chatSession{toolDefs: []provider.Tool{{Name: "read_file"}}}
	dialing := make(chan struct{})
	opts := mcp.Options{Dial: func(ctx context.Context, def mcp.Definition, mask func(string) bool) (*mcp.Server, error) {
		close(dialing)
		return h.dial(ctx, def, mask)
	}}
	attached := make(chan func())
	go func() { attached <- s.useMCP(connectMCP(t.Context(), cat, opts, true), cat, true) }()

	<-dialing
	select {
	case <-attached:
		t.Fatal("the attach returned before its server answered")
	default:
	}
	h.answer("docs", "", "search")
	t.Cleanup(<-attached)

	if s.mcpJoins {
		t.Fatal("a waiting surface left its servers to join later")
	}
	var names []string
	for _, d := range s.toolDefs {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"read_file", "docs__search"}) {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(s.promptExtra, "# MCP servers") {
		t.Fatalf("no block in the prompt: %q", s.promptExtra)
	}
	if got := s.requestTools(s.toolDefs); len(got) != 2 {
		t.Fatalf("a waiting surface's request carries the servers twice: %v", got)
	}
}

// A finished connect is taken at the boundary: the request's tool list, read
// from the toolset on each request, gains the server's tools there and not
// before; the system prompt says the servers' block and the toolbox again over
// the new set; a new session's prompt says them from its start; and the
// transcript's line is the death note's shape.
func TestJoin_RebuildsTheToolListTheServerBlockAndTheToolbox(t *testing.T) {
	h := holdServers("docs", "tracker")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("docs", true), stdioServer("tracker", false)}}
	s := &chatSession{toolDefs: []provider.Tool{{Name: "read_file"}, {Name: "web_fetch"}}}
	s.toolboxSaid = prompt.Toolbox(s.toolDefs, false)
	t.Cleanup(s.useMCP(connectMCP(t.Context(), cat, mcp.Options{Dial: h.dial}, false), cat, false))
	join := newMCPJoin(*s)
	system := "base prompt\n\n" + s.toolboxSaid

	if got := s.requestTools(s.toolDefs); len(got) != 2 {
		t.Fatalf("a request before any server answered carries %v", got)
	}
	if _, moved := join.take(system); moved {
		t.Fatal("a boundary with nothing settled moved something")
	}

	h.answer("docs", "docs://guide", "search")
	settledReport(t, s.mcpTools, 0)
	if got := s.requestTools(s.toolDefs); len(got) != 2 {
		t.Fatalf("an answer reached the request before the boundary: %v", got)
	}

	j, moved := join.take(system)
	if !moved {
		t.Fatal("the boundary took nothing")
	}
	var names []string
	for _, d := range s.requestTools(s.toolDefs) {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"read_file", "web_fetch", "docs__search", mcp.ResourceToolName}) {
		t.Fatalf("the request after the boundary carries %v", names)
	}
	if !slices.Equal(j.Notes, []string{"mcp: docs: up — 1 tool, from this turn"}) {
		t.Fatalf("notes = %q", j.Notes)
	}
	block := strings.Index(j.System, "# MCP servers")
	toolbox := strings.Index(j.System, "# Toolbox")
	if block < 0 || toolbox < block || !strings.HasPrefix(j.System, "base prompt\n\n") {
		t.Fatalf("system prompt after the join:\n%s", j.System)
	}
	if !strings.Contains(j.System[toolbox:], "- "+mcp.ResourceToolName+" — ") || strings.Count(j.System, "# Toolbox") != 1 {
		t.Fatalf("the toolbox was not said again over the new set:\n%s", j.System)
	}
	var served []string
	for _, d := range j.ServerTools {
		served = append(served, d.Name)
	}
	if !slices.Equal(served, []string{"docs__search", mcp.ResourceToolName}) {
		t.Fatalf("server tools = %v", served)
	}
	if len(j.Sources) != 2 || j.Sources[0].State != components.ToolSourceUp || j.Sources[1].State != components.ToolSourceStarting {
		t.Fatalf("rows = %+v", j.Sources)
	}
	// A new session's prompt, built from the standing extra, says the same.
	if fresh := join.fresh(system); fresh != j.System {
		t.Fatalf("a new session's prompt:\n%s\nwant:\n%s", fresh, j.System)
	}

	// tracker joins later and is not read-only: its tool asks, and the card
	// it asks on comes with it.
	h.answer("tracker", "", "file")
	settledReport(t, s.mcpTools, 1)
	j, _ = join.take(j.System)
	if strings.Count(j.System, "# MCP servers") != 1 || !strings.Contains(j.System, "tracker") {
		t.Fatalf("the block was not said again in place:\n%s", j.System)
	}
	if _, ok := j.Gated["tracker__file"]; !ok {
		t.Fatalf("the joined tool that asks has no card: %v", j.Gated)
	}
	if p, err := j.Gated["tracker__file"](json.RawMessage(`{}`)); err != nil || !strings.Contains(p.Summary, "tracker file") {
		t.Fatalf("card = %+v, %v", p, err)
	}
}

// A server that fails or runs out its bound reaches the transcript the way a
// join does: one line at the boundary, and nothing said to the model.
func TestMCPFailure_IsALineAtTheBoundary(t *testing.T) {
	h := holdServers("tracker", "linear")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("tracker", false), stdioServer("linear", false)}}
	cat.Servers[0].Timeout = 20 * time.Millisecond
	s := &chatSession{toolDefs: []provider.Tool{{Name: "read_file"}}}
	t.Cleanup(s.useMCP(connectMCP(t.Context(), cat, mcp.Options{Dial: h.dial}, false), cat, false))
	join := newMCPJoin(*s)
	close(h["linear"])
	settledReport(t, s.mcpTools, 0)
	settledReport(t, s.mcpTools, 1)

	j, moved := join.take("base prompt")
	if !moved {
		t.Fatal("the boundary took nothing")
	}
	want := []string{
		"mcp: tracker: did not start (no answer within 0.0s — timeout_seconds) — its tools are not in this session",
		"mcp: linear: did not start (connection refused) — its tools are not in this session",
	}
	if !slices.Equal(j.Notes, want) {
		t.Fatalf("notes = %q", j.Notes)
	}
	if j.System != "" || j.ServerTools != nil {
		t.Fatalf("a failure said something to the model: %+v", j)
	}
	if _, moved := join.take("base prompt"); moved {
		t.Fatal("the failure was said twice")
	}
}

// A child reads the read-only servers that have joined when it is spawned. One
// spawned before a server joined is not handed it, and cannot reach it by a
// name it learned from its task.
func TestSpawn_ReadsTheServersUpNow(t *testing.T) {
	h := holdServers("docs", "wiki")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("docs", true), stdioServer("wiki", true)}}
	s := &chatSession{notebook: notebook.New(nil)}
	t.Cleanup(s.useMCP(connectMCP(t.Context(), cat, mcp.Options{Dial: h.dial}, false), cat, false))
	join := newMCPJoin(*s)
	h.answer("docs", "", "search")
	settledReport(t, s.mcpTools, 0)
	join.take("")

	base := func(name string, _ json.RawMessage) (string, error) { return "", errors.New("unknown tool: " + name) }
	spawn := func() ([]string, func(string, json.RawMessage) (string, error), string) {
		defs, exec, sys, _ := withSessionTools(*s, nil, "child-1", t.TempDir(), nil, base, fixedPrompt(""))
		var names []string
		for _, d := range defs {
			names = append(names, d.Name)
		}
		return names, exec, sys
	}
	early, earlyExec, earlyPrompt := spawn()
	if !slices.Contains(early, "docs__search") || slices.Contains(early, "wiki__page") || strings.Contains(earlyPrompt, "wiki") {
		t.Fatalf("the first child holds %v", early)
	}

	h.answer("wiki", "", "page")
	settledReport(t, s.mcpTools, 1)
	join.take("")

	late, _, latePrompt := spawn()
	if !slices.Contains(late, "wiki__page") || !strings.Contains(latePrompt, "wiki") {
		t.Fatalf("a child spawned after the join holds %v", late)
	}
	if _, err := earlyExec("wiki__page", json.RawMessage(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "joined the session after this agent started") {
		t.Fatalf("the first child reached a server it was never handed: %v", err)
	}
}

// The transcript's line for a bound that ran out carries the bound and the
// key that raises it, as /mcp's fix line does: the definition's own key where
// the definition set the bound, the session's where it did.
func TestMCPFailure_ATimeoutLineNamesTheBoundAndTheKey(t *testing.T) {
	own := mcp.Report{Definition: stdioServer("tracker", false), Status: mcp.StatusFailed, TimedOut: true,
		Bound: 20 * time.Second}
	session := own
	session.BoundBySession = true
	got := mcpJoinNotes([]mcp.Report{own, session})
	want := []string{
		"mcp: tracker: did not start (no answer within 20s — timeout_seconds) — its tools are not in this session",
		"mcp: tracker: did not start (no answer within 20s — mcp.startup_timeout_seconds) — its tools are not in this session",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("notes = %q", got)
	}
}

// A join rewrites the system prompt the conversation sends, so the session
// row's fingerprint is taken again over the prompt as sent: a turn after a
// join carries a hash that matches its request, and one before it still
// matches the launch prompt.
func TestJoin_TheRecordsPromptHashFollowsTheJoin(t *testing.T) {
	h := holdServers("docs")
	cat := &mcp.Catalog{Servers: []mcp.Definition{stdioServer("docs", true)}}
	s := &chatSession{toolDefs: []provider.Tool{{Name: "read_file"}}}
	s.toolboxSaid = prompt.Toolbox(s.toolDefs, false)
	t.Cleanup(s.useMCP(connectMCP(t.Context(), cat, mcp.Options{Dial: h.dial}, false), cat, false))
	system := "base prompt\n\n" + s.toolboxSaid

	db := fixtureStore(t)
	rec := startObserveRecorder(db, "chat", "anthropic", "test-model", nil)
	rec.stamp(system, 0, "/repo", storage.AgentSettings{})
	join := newMCPJoin(*s)
	join.sent = func(text string) { rec.stamp(text, 0, "/repo", storage.AgentSettings{}) }
	hashOf := func() string {
		t.Helper()
		row, ok, err := db.AgentSession(rec.sessionID())
		if err != nil || !ok {
			t.Fatalf("session: %v (found=%v)", err, ok)
		}
		return row.PromptHash
	}

	// A boundary with nothing settled leaves the hash alone.
	if _, moved := join.take(system); moved || hashOf() != fingerprint(system) {
		t.Fatalf("an empty boundary moved the hash: %q", hashOf())
	}
	h.answer("docs", "", "search")
	settledReport(t, s.mcpTools, 0)
	j, moved := join.take(system)
	if !moved || j.System == "" || j.System == system {
		t.Fatalf("the join did not rewrite the prompt: %+v", j)
	}
	if got := hashOf(); got != fingerprint(j.System) || got == fingerprint(system) {
		t.Fatalf("hash = %q; want the rewritten prompt's %q", got, fingerprint(j.System))
	}
}
