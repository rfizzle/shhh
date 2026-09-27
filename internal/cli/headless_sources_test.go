package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/testhttp"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/web"
)

// fetchTurn is an agent that fetches one address and then answers.
func fetchTurn(t *testing.T, url string) *agent.Agent {
	t.Helper()
	rounds := [][]provider.StreamEvent{
		{{ToolCalls: []provider.ToolCall{{ID: "f1", Name: web.FetchToolName, Arguments: `{"url":"` + url + `"}`}}}},
		{{Token: "read it"}, {Done: true}},
	}
	var next int
	return agent.New(nil, func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		if next >= len(rounds) {
			t.Fatalf("unexpected stream request #%d", next+1)
		}
		ch := make(chan provider.StreamEvent, len(rounds[next]))
		for _, ev := range rounds[next] {
			ch <- ev
		}
		close(ch)
		next++
		return ch, func() {}, nil
	})
}

// ledgeredWeb is a web toolset over an in-memory fixture site, holding a
// ledger the way openSourceLedger hands it one.
func ledgeredWeb(t *testing.T) (*web.Toolset, *web.Ledger, string) {
	t.Helper()
	var fixtures testhttp.Registry
	srv := fixtures.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><head><title>Style guide</title></head><body><p>Use tabs.</p></body></html>")
	}))
	t.Cleanup(srv.Close)
	tools := web.NewToolset(web.NewFetcherWithClient(web.Policy{AllowPrivate: true}, fixtures.Client()), nil)
	ledger := web.NewLedger(nil)
	tools.UseLedger(ledger)
	return tools, ledger, srv.URL + "/style"
}

// A run behind --print keeps a ledger and states it: the page the run fetched
// is a row of the transcript's sources, in the shape the sources screen draws
// it from, and a run that read nothing leaves the field out rather than
// writing an empty one — the field is an addition to a shape scripts already
// read (docs/capabilities/headless.md#a-run-says-what-it-read).
func TestHeadlessRun_TheTranscriptListsWhatItRead(t *testing.T) {
	webTools, ledger, url := ledgeredWeb(t)
	a := fetchTurn(t, url)
	var lines bytes.Buffer
	obs := headlessObserver{rounds: a.Rounds, stream: newJSONLStream(&lines), sources: newSourceFeed(ledger)}
	h := &agent.Headless{
		Agent: a,
		Gate:  unattendedGate(webTools, nil, nil, nil),
		Resolve: headlessApprover(context.Background(), printOpts{yes: true}, nil, nil, fakeRun(&[]string{}), "", nil, obs.decision,
			webTools, nil, nil, nil, nil, nil, unattended{at: obs.pos}),
		OnToolResult: obs.toolResult,
	}
	final, err := h.Run("read the style guide")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var out bytes.Buffer
	if err := writeJSONTranscript(&out, jsonRun{messages: a.Messages(), final: final, sources: ledger.List()}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sources []jsonSource `json:"sources"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("the transcript should list the one page read, got %+v:\n%s", got.Sources, out.String())
	}
	s := got.Sources[0]
	if s.Kind != web.KindFetch || s.URL != url || s.Title != "Style guide" || s.Agent != web.Orchestrator ||
		s.Status != http.StatusOK || s.Bytes == 0 {
		t.Fatalf("the row is not the page as the ledger recorded it: %+v", s)
	}

	// And the stream says the same row, once, before the run closes.
	var rows int
	for _, line := range strings.Split(strings.TrimSpace(lines.String()), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("not an event: %q", line)
		}
		if ev.Kind != observe.EventSource {
			continue
		}
		rows++
		if ev.Source == nil || ev.Source.URL != url || ev.Source.Kind != web.KindFetch {
			t.Fatalf("a source line does not carry the row: %q", line)
		}
	}
	obs.sourcesRead()
	if rows != 1 || strings.Count(lines.String(), `"kind":"source"`) != 1 {
		t.Fatalf("the stream should carry the row exactly once, carried %d:\n%s", rows, lines.String())
	}

	var none bytes.Buffer
	if err := writeJSONTranscript(&none, jsonRun{final: "nothing read"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), `"sources"`) {
		t.Fatalf("a run that read nothing should leave the field out:\n%s", none.String())
	}
}

// A run's ledger goes with its conversation: once the save has settled the
// slot, the rows are filed under it in the store, so a session that resumes
// the slot has them on /sources (docs/capabilities/chat.md#what-was-read).
func TestHeadlessRun_TheLedgerFollowsTheSlot(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	webTools, _, url := ledgeredWeb(t)
	ledger := web.NewLedger(db)
	webTools.UseLedger(ledger)
	a := fetchTurn(t, url)
	h := &agent.Headless{
		Agent: a,
		Gate:  unattendedGate(webTools, nil, nil, nil),
		Resolve: headlessApprover(context.Background(), printOpts{yes: true}, nil, nil, fakeRun(&[]string{}), "", nil, nil,
			webTools, nil, nil, nil, nil, nil, unattended{}),
	}
	if _, err := h.Run("read the style guide"); err != nil {
		t.Fatalf("run: %v", err)
	}
	slot, err := db.ClaimChatSlot("unattended")
	if err != nil {
		t.Fatal(err)
	}
	saved := &headlessChat{db: db, slot: slot, kind: "code"}
	saved.save(a.Messages())
	saved.keepSources(ledger)

	rows, err := db.LoadSources(saved.slot)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != web.KindFetch || rows[0].FinalURL != url || rows[0].Title != "Style guide" {
		t.Fatalf("the slot should hold the page the run read, got %+v", rows)
	}
	resumed := web.NewLedger(db)
	if err := resumed.Bind(saved.slot); err != nil {
		t.Fatal(err)
	}
	if got := resumed.List(); len(got) != 1 || got[0].FinalURL != url {
		t.Fatalf("a session resuming the slot should list the run's reading, got %+v", got)
	}
}

// A page a server's tool handed back travels as the ledger words it — kind
// "mcp", the way the sources screen draws it "via mcp" — and never as a fetch.
func TestJSONSources_AServersPageTravelsAsItsOwnKind(t *testing.T) {
	rows := jsonSources([]web.Source{{Kind: web.KindServer, FinalURL: "https://docs.example/a", Agent: web.Orchestrator}})
	if len(rows) != 1 || rows[0].Kind != web.KindServer || rows[0].Status != 0 {
		t.Fatalf("a server's page should keep its kind and claim no status: %+v", rows)
	}
}

// A served session puts each ledger row on its client's stream as a line of
// its own, ahead of the close — the line --output jsonl prints, since the
// two are one writer.
func TestAServedSessionsClientIsToldWhatItRead(t *testing.T) {
	webTools, ledger, url := ledgeredWeb(t)
	a := fetchTurn(t, url)
	lines := &syncLines{}
	l := &serveLoop{
		agent:  a,
		events: newJSONLStream(lines),
		own:    &writtenByCalls{},
		saved:  &headlessChat{},
	}
	l.obs = headlessObserver{rounds: a.Rounds, turn: l.turnNow, stream: l.events, sources: newSourceFeed(ledger)}
	l.headless = &agent.Headless{
		Agent: a,
		Gate:  unattendedGate(webTools, nil, nil, nil),
		Resolve: headlessApprover(context.Background(), printOpts{yes: true}, nil, nil, fakeRun(&[]string{}), "",
			nil, nil, webTools, nil, nil, nil, nil, nil, unattended{at: l.obs.pos}),
		OnToolResult: l.obs.toolResult,
	}
	if _, err := l.Run(1, "read the style guide"); err != nil {
		t.Fatalf("the turn: %v", err)
	}
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(lines.String()), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("not an event: %q", line)
		}
		kinds = append(kinds, ev.Kind)
		if ev.Kind == observe.EventSource && (ev.Source == nil || ev.Source.URL != url || ev.Turn != 1) {
			t.Fatalf("the source line lost its row or its turn: %q", line)
		}
	}
	src, closed := -1, -1
	for i, k := range kinds {
		switch k {
		case observe.EventSource:
			src = i
		case observe.EventClose:
			closed = i
		}
	}
	if src < 0 || closed < 0 || src > closed {
		t.Fatalf("the client should be told the row before the turn closes: %v", kinds)
	}
}

// The runner hands the write-up's Sources block what each stage's own
// process read: a stage's sources join the checkpoint's State.Sources, the
// first read of a page kept, and a server's page and an error page left out
// of the read set (docs/capabilities/todo.md#a-write-up-says-what-it-read).
func TestTodoRunHeadless_AWriteUpListsWhatTheStagesRead(t *testing.T) {
	dir := t.TempDir()
	aReadingProfile(t, dir)
	words, pipeline, err := run.LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	withBacklogProfile(t, words, pipeline)
	root := aBacklogOf(t, t.TempDir(), "kind: reading\n", "a-one")
	d, out := headlessDriver(t, root, nil)
	d.turn = func(_ context.Context, _ time.Time, _ string, step run.Step) (todoTurn, error) {
		if step.Stage == "check" {
			// The reader read the same page again; it is one source.
			return todoTurn{text: "verdict: clean", code: exitDone, sources: []web.Source{
				{Kind: web.KindFetch, FinalURL: "https://example.com/style/", Status: 200, Title: "Style guide (again)"},
			}}, nil
		}
		return todoTurn{
			text: "The sources say tabs.",
			code: exitDone,
			sources: []web.Source{
				{Kind: web.KindSearch, Query: "tabs or spaces", Results: 3},
				{Kind: web.KindFetch, FinalURL: "https://example.com/style", Status: 200, Title: "Style guide"},
				{Kind: web.KindFetch, FinalURL: "https://example.com/gone", Status: 404},
				{Kind: web.KindServer, FinalURL: "https://docs.example/served"},
			},
		}, nil
	}

	st := d.work(context.Background(), mustReading(t, root, "a-one"), nil)
	if st.Stage != run.StageDone {
		t.Fatalf("the reading stopped at %s (%s):\n%s", st.Stage, st.Blocked, out.String())
	}
	want := run.Source{URL: "https://example.com/style", Title: "Style guide", Read: true}
	if len(st.Sources) != 1 || st.Sources[0] != want {
		t.Fatalf("State.Sources = %+v, want the one page read, %+v", st.Sources, want)
	}

	it, ok := todo.Load(words, root).Find("a-one")
	if !ok || !it.Archived {
		t.Fatalf("the reading should be archived: %+v", it)
	}
	body, err := os.ReadFile(it.Path)
	if err != nil {
		t.Fatal(err)
	}
	report := string(body)
	for _, line := range []string{"## Sources", "- https://example.com/style — Style guide"} {
		if !strings.Contains(report, line) {
			t.Errorf("the write-up should carry %q:\n%s", line, report)
		}
	}
	for _, absent := range []string{"docs.example/served", "example.com/gone"} {
		if strings.Contains(report, absent) {
			t.Errorf("the write-up lists %q as read, which no fetch of shhh's answered for:\n%s", absent, report)
		}
	}
}

// A report that cites a page no stage read lists it under the pages that
// were read, once; a run that read nothing is given no block to say so in,
// as a session with an empty ledger is given none.
func TestCiteSources_ListsWhatTheReportCitesAndNobodyRead(t *testing.T) {
	st := &run.State{
		Report:  "Tabs, per https://example.com/style/ and https://example.com/never-opened (twice: https://example.com/never-opened).",
		Sources: []run.Source{{URL: "https://example.com/style", Title: "Style guide", Read: true}},
	}
	citeSources(st)
	want := []run.Source{
		{URL: "https://example.com/style", Title: "Style guide", Read: true},
		{URL: "https://example.com/never-opened"},
	}
	if len(st.Sources) != len(want) || st.Sources[0] != want[0] || st.Sources[1] != want[1] {
		t.Fatalf("State.Sources = %+v, want %+v", st.Sources, want)
	}
	if block := run.SourcesSection(st.Sources); !strings.Contains(block, "Cited, not read:\n\n- https://example.com/never-opened") {
		t.Fatalf("the block should list the unread citation:\n%s", block)
	}

	unread := &run.State{Report: "See https://example.com/never-opened."}
	citeSources(unread)
	if len(unread.Sources) != 0 {
		t.Fatalf("a run that read nothing should be handed no sources, got %+v", unread.Sources)
	}

	// A run whose stages searched and read nothing says so over what it
	// cites, as a session whose ledger holds only searches does; with
	// nothing cited, both give no block.
	searched := &run.State{Report: "See https://example.com/never-opened."}
	readSources(searched, []web.Source{{Kind: web.KindSearch, Query: "tabs or spaces", Results: 3}})
	citeSources(searched)
	block := run.SourcesSection(searched.Sources)
	for _, line := range []string{"Nothing was read", "Cited, not read:\n\n- https://example.com/never-opened"} {
		if !strings.Contains(block, line) {
			t.Fatalf("a run that only searched should carry %q:\n%s", line, block)
		}
	}
	quiet := &run.State{Report: "Nothing cited."}
	readSources(quiet, []web.Source{{Kind: web.KindSearch, Query: "tabs or spaces"}})
	citeSources(quiet)
	if block := run.SourcesSection(quiet.Sources); block != "" {
		t.Fatalf("a run that searched and cites nothing should get no block, as a session does:\n%s", block)
	}
}
