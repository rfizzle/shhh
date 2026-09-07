package eval

// A case with a site instead of a workspace: a research question put to a
// session that can read nothing but the pages this harness is serving, and a
// write-up graded against what the run actually fetched.
//
// Neither of the other two shapes can see a research run. A workspace check
// asks what the checkout looks like afterwards, and a run that reads six
// pages and writes a paragraph touches no file; a table asks one bounded call
// for one word, and the failure worth catching here happens across thirty
// fetches. So this shape scores the write-up against the run's own ledger:
// every URL it cites has to be one the fetcher returned, every sentence it
// quotes has to be on the page it attributes it to, and every fact the case
// requires has to be there. Three rates, never added together — a write-up
// can carry every fact and cite a page it never opened, and that is the
// failure a single pass would hide inside a success.
//
// The run is driven from here rather than by starting the binary, for the
// reason a table case is: the site is on loopback, which the fetch guard
// refuses, and the only honest way to lift that for one server is in the
// process that knows which server it is. A config key or an environment
// variable a child could read would be a hole in the guard that ships.
// See docs/capabilities/evals.md#a-research-case-is-graded-against-what-it-read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/web"
)

// KindResearch is a question put to a session with the web tools and a site
// served for the length of the attempt.
const KindResearch Kind = "research"

// SiteDir is the fixture site inside a research case directory, the way
// WorkspaceDir is the fixture checkout inside a workspace one.
const SiteDir = "site"

// SitePlaceholder is what a case writes where it means the site's base URL.
// The port is whatever the OS hands the listener, so a case cannot name it;
// without a way to say "start here" every case would depend on the search
// fixture finding the right page first, which measures the search fixture.
const SitePlaceholder = "{site}"

// searchPath is where the search fixture answers on the same listener. It is
// a path no case's page can collide with because a page is a file and this
// is not one of them.
const searchPath = "/-search"

// RunsBinary reports whether this kind is measured by starting a session
// process. Only a workspace case is: the other shapes are requests and loops
// this harness makes itself, and a suite of them must not refuse to start
// because the harness could not name its own executable.
//
// The empty kind answers yes for the reason the loader turns it into a
// workspace case: it is what every case written before the other shapes
// existed still means, and a caller that built a Case by hand rather than
// from a file means it too.
func (k Kind) RunsBinary() bool { return k == KindWorkspace || k == "" }

// Rate is one of the three things a write-up is graded on: how many of the
// things there were to check came out right, and how many there were.
type Rate struct{ Got, Of int }

// OK reports whether nothing was missed. A rate with nothing to check is
// whole: a write-up that quotes no sentence has no misattributed quotation
// in it, and the fact rate is what says whether it answered the question.
func (r Rate) OK() bool { return r.Got == r.Of }

func (r Rate) String() string { return fmt.Sprintf("%d of %d", r.Got, r.Of) }

// ResearchScore is one attempt's write-up, graded.
type ResearchScore struct {
	// Cited is the URLs the write-up names that the ledger says were read.
	// Quoted is the quotations that appear in the page they are attributed
	// to. Facts is the case's required facts the write-up contains.
	Cited  Rate
	Quoted Rate
	Facts  Rate
	// Misses is one line per thing that came out wrong, in the order the
	// three checks run, because a rate says how much was missed and never
	// which part.
	Misses []string
}

// OK is the attempt's hard verdict: all three whole.
func (s ResearchScore) OK() bool { return s.Cited.OK() && s.Quoted.OK() && s.Facts.OK() }

// Add folds another attempt's grading into this one.
func (s *ResearchScore) Add(other ResearchScore) {
	s.Cited.Got += other.Cited.Got
	s.Cited.Of += other.Cited.Of
	s.Quoted.Got += other.Quoted.Got
	s.Quoted.Of += other.Quoted.Of
	s.Facts.Got += other.Facts.Got
	s.Facts.Of += other.Facts.Of
	s.Misses = append(s.Misses, other.Misses...)
}

// researchAttempt runs one research case once and grades what came back.
func researchAttempt(ctx context.Context, c Case, opts Options) Attempt {
	start := time.Now()
	a := Attempt{}
	fail := func(err error) Attempt {
		a.Err = err
		a.Elapsed = time.Since(start)
		return a
	}

	if opts.Provider == nil {
		return fail(fmt.Errorf("no provider to ask: a %s case is a loop this harness runs itself, not a binary it starts", c.Kind))
	}
	site, err := openSite(c.Site)
	if err != nil {
		return fail(err)
	}
	defer site.close()

	runCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// The toolset is the session's, with two things replaced: the fetch
	// policy reaches this one server and nothing else, and the search
	// endpoint is the fixture's. Everything the model sees of a page — the
	// header with the final URL, the inline cut, the notice — is what a
	// session's fetch produces, which is the half of the run being measured.
	ts := web.NewToolset(
		web.NewFetcher(web.FixturePolicy(site.authority)),
		&web.Searcher{APIKey: "fixture", Endpoint: site.base + searchPath},
	)
	// The ledger is opened here rather than borrowed from a session: it is a
	// plain object bound to the toolset, and the harness needs the rows to
	// grade with, so it keeps them for the length of the attempt and throws
	// them away with the site.
	ledger := web.NewLedger(nil)
	ts.UseLedger(ledger)

	ag := agent.New(
		[]provider.Message{{Role: provider.RoleSystem, Content: prompt.BuildResearcher(shell.Detect(), prompt.WebTools{Fetch: true, Search: true})}},
		func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			sctx, cancel := context.WithCancel(runCtx)
			ev, sErr := opts.Provider.StreamCompletion(sctx, msgs, provider.CompletionOpts{
				Model:      opts.Model,
				Tools:      ts.Definitions(),
				ToolChoice: choice,
			})
			if sErr != nil {
				cancel()
				return nil, nil, sErr
			}
			return ev, cancel, nil
		})
	ag.SetExecutor(ts.WrapExecutor(web.Orchestrator, func(name string, _ json.RawMessage) (string, error) {
		return "", fmt.Errorf("there is no %s tool in this session", name)
	}))

	// Nothing is gated. A fetch prompts in a session and is policy in an
	// unattended run, which is the same reading `--yes` gives a workspace
	// case: a suite that stopped for an approval would measure nothing.
	h := &agent.Headless{
		Agent:      ag,
		OnToolCall: func(provider.ToolCall) { a.Calls++ },
		OnUsage: func(u *provider.Usage) {
			a.TokensIn += u.PromptTokens
			a.TokensOut += u.CompletionTokens
		},
	}
	writeup, runErr := h.Run(strings.ReplaceAll(c.Prompt, SitePlaceholder, site.base))
	a.Rounds = ag.Rounds()
	if opts.Price != nil {
		a.Cost, a.Priced = opts.Price(opts.Model, a.TokensIn, a.TokensOut)
	}
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		// A run stopped part-way read some of the site and wrote nothing,
		// and grading that would report a ceiling as a model that cites
		// badly. It is the same reading a workspace attempt gives a session
		// that did not finish: a machine to fix, not a score.
		return fail(fmt.Errorf("the session did not finish inside %s", opts.Timeout))
	case runErr != nil:
		return fail(fmt.Errorf("the session failed: %w", runErr))
	}

	score := GradeResearch(c, writeup, ledger.List(), site.pages)
	a.Research = &score
	a.Passed = score.OK()
	a.Elapsed = time.Since(start)
	return a
}

// GradeResearch scores one write-up against the run's ledger and the pages
// the site served. It is separate from the run so the grading can be put to a
// fixed ledger and a fixed write-up, which is the only way to know a rate
// measures what it claims to.
func GradeResearch(c Case, writeup string, rows []web.Source, pages map[string]string) ResearchScore {
	var s ResearchScore
	miss := func(format string, args ...any) { s.Misses = append(s.Misses, fmt.Sprintf(format, args...)) }

	// Read is what the fetcher answered with — a 2xx page, once per URL —
	// and never what the model says it read.
	read := map[string]bool{}
	for _, p := range web.Pages(rows) {
		read[web.CanonicalURL(p.FinalURL)] = true
	}
	for _, u := range web.CitedURLs(writeup) {
		s.Cited.Of++
		if read[web.CanonicalURL(u)] {
			s.Cited.Got++
			continue
		}
		miss("cited, not read: %s", u)
	}

	normalized := make(map[string]string, len(pages))
	for u, text := range pages {
		normalized[web.CanonicalURL(u)] = normalizeQuoted(text)
	}
	for _, q := range quotations(writeup) {
		s.Quoted.Of++
		where, found := quotedOn(q, normalized, read)
		if found {
			s.Quoted.Got++
			continue
		}
		miss("quoted but not on %s: %q", where, clip(q.text, maxMissQuote))
	}

	for _, re := range c.Facts {
		s.Facts.Of++
		if re.MatchString(writeup) {
			s.Facts.Got++
			continue
		}
		miss("the write-up does not say: %s", re.String())
	}
	return s
}

// maxMissQuote is how much of a quotation a miss line carries. A report row's
// body is one terminal line; enough of a sentence to find it in the write-up
// is all it can spend.
const maxMissQuote = 60

// minQuoteChars is the shortest run of quoted text taken as a quotation. Below
// it a pair of quotation marks is a term of art, an identifier or a value —
// `"auto"`, `"127.0.0.1"` — and demanding those appear verbatim on a page
// would score the write-up's typography. A quoted sentence clears it easily.
const minQuoteChars = 24

// quotation is one quoted sentence and the pages the write-up attributes it
// to.
type quotation struct {
	text string
	// cited is the URLs the quotation is attributed to: the ones named in
	// its own paragraph, or failing that in the nearest paragraph above it.
	// Empty means the write-up named none, and the quotation is then checked
	// against every page the run read — which is weaker, and still catches a
	// sentence that is on no page at all.
	cited []string
}

// quotations is every quoted sentence in a write-up, with what it is
// attributed to.
//
// Two shapes count, because both are how a model quotes a source: text
// between quotation marks, and a markdown block quote. A block quote is
// usually its own paragraph, with the URL in the sentence that introduces it,
// which is why attribution falls back to the paragraph above rather than
// giving up at the paragraph boundary.
func quotations(writeup string) []quotation {
	var out []quotation
	var carried []string
	for _, para := range paragraphs(writeup) {
		cited := web.CitedURLs(para)
		if len(cited) > 0 {
			carried = cited
		}
		for _, text := range quotedSpans(para) {
			out = append(out, quotation{text: text, cited: carried})
		}
	}
	return out
}

// paragraphs splits on blank lines, which is the unit a citation covers in
// prose: the sentence naming the URL and the sentences around it.
func paragraphs(text string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// quotedSpans is the quoted text in one paragraph: spans between quotation
// marks — straight or typographic, since a model uses both — and runs of
// block-quoted lines, joined so a quotation broken across two lines is one
// quotation.
func quotedSpans(para string) []string {
	var out []string
	var block []string
	flushBlock := func() {
		if len(block) > 0 {
			out = append(out, keepQuote(strings.Join(block, " "))...)
			block = nil
		}
	}
	for _, line := range strings.Split(para, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ">") {
			block = append(block, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
			continue
		}
		flushBlock()
		out = append(out, keepQuote(betweenMarks(line)...)...)
	}
	flushBlock()
	return out
}

// betweenMarks is the text between each pair of quotation marks on one line.
// An unclosed mark ends the scan: an apostrophe-heavy sentence would
// otherwise pair a mark with one three sentences later and quote the prose in
// between.
func betweenMarks(line string) []string {
	pairs := map[rune]rune{'"': '"', '“': '”'}
	var out []string
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		closer, ok := pairs[runes[i]]
		if !ok {
			continue
		}
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == closer {
				out = append(out, string(runes[i+1:j]))
				i = j
				break
			}
		}
	}
	return out
}

// keepQuote drops what is quoted but is not a quotation: too short to be a
// sentence, or a single word in marks.
//
// The length is measured on the form the comparison will use, not on the
// characters as written. A run of punctuation long enough to pass here and
// short enough to vanish under trimQuoted would be looked for as an empty
// string, which every page contains — a quotation that could not be wrong.
func keepQuote(texts ...string) []string {
	var out []string
	for _, t := range texts {
		t = strings.TrimSpace(t)
		compared := trimQuoted(normalizeQuoted(t))
		if len([]rune(compared)) < minQuoteChars || !strings.Contains(compared, " ") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// quotedOn reports whether the quotation is on a page it was attributed to,
// and names where it was looked for. The pages are already normalized.
func quotedOn(q quotation, pages map[string]string, read map[string]bool) (where string, found bool) {
	want := trimQuoted(normalizeQuoted(q.text))
	targets := q.cited
	where = strings.Join(targets, ", ")
	if len(targets) == 0 {
		// Nothing was cited near it, so the weaker question is asked: is
		// this sentence on any page the run actually read?
		where = "any page the run read"
		for u := range read {
			targets = append(targets, u)
		}
	}
	for _, u := range targets {
		if text, ok := pages[web.CanonicalURL(u)]; ok && strings.Contains(text, want) {
			return where, true
		}
	}
	return where, false
}

// normalizeQuoted is the form a quotation and a page are compared in:
// case-folded, with every run of whitespace one space and the typographic
// spellings of the marks and dashes folded onto the ASCII ones. A page's line
// wrapping and a model's quotation marks are not what is being measured.
func normalizeQuoted(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		switch r {
		case '‘', '’':
			r = '\''
		case '“', '”':
			r = '"'
		case '–', '—':
			r = '-'
		case ' ':
			r = ' '
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// quotedEdges are the characters a quotation picks up from the sentence that
// carries it, and never from the page.
//
// The failure this exists for: a page reads "…when a query names no order,
// and it has done so in every 1.x release", the write-up quotes the clause
// and closes it with a full stop, and a literal comparison scores an
// accurate quotation as an invented one. Terminal punctuation and an ellipsis
// are the writer's; the words are the page's, and the words are what is being
// checked. It is the same reading the ledger takes of a URL in prose.
const quotedEdges = ".,;:!?…-'\" "

// trimQuoted takes those characters off both ends of a normalized quotation.
func trimQuoted(text string) string {
	return strings.Trim(text, quotedEdges)
}

func clip(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// site is a case's pages, served over loopback for the length of one attempt
// and thrown away with it.
type site struct {
	ln     net.Listener
	server *http.Server
	// base is "http://127.0.0.1:41234" and authority "127.0.0.1:41234"; the
	// first is what a case's prompt is given and the second what the fetch
	// policy is opened for.
	base      string
	authority string
	// pages is the text of every page, by URL: what a quotation is checked
	// against. It is the served bytes read through the same extractor the
	// fetch tool uses, so what is compared is what the model was shown.
	pages map[string]string
	// files is what the handler answers with, by request path.
	files map[string]sitePage
	// index is the same pages in one order, which is what the search fixture
	// ranks.
	index []sitePage
}

// sitePage is one file on the fixture site.
type sitePage struct {
	URL         string
	Title       string
	Text        string
	Body        []byte
	ContentType string
}

// openSite reads a case's site directory and starts serving it on loopback.
//
// Every file is read up front rather than served off the disk: the handler
// then answers from memory, the text a quotation is checked against is the
// text that went over the wire, and there is no path outside the case
// directory for a request to walk to.
func openSite(dir string) (*site, error) {
	if dir == "" {
		return nil, fmt.Errorf("the case has no site to serve")
	}
	s := &site{pages: map[string]string{}, files: map[string]sitePage{}}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		s.files["/"+filepath.ToSlash(rel)] = sitePage{
			Body:        body,
			ContentType: contentType(path),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the site in %s: %w", dir, err)
	}
	if len(s.files) == 0 {
		return nil, fmt.Errorf("%s: the site has no pages in it", dir)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot serve the site: %w", err)
	}
	s.ln = ln
	s.authority = ln.Addr().String()
	s.base = "http://" + s.authority

	// A page is extracted here rather than as it was read, because a page is
	// only extractable once it has an address: that is what a relative link
	// resolves against, and the text a quotation is graded against has to be
	// the text the model was shown.
	for path, p := range s.files {
		p.URL = s.base + path
		if strings.HasPrefix(p.ContentType, "text/html") {
			ex := web.ExtractHTML(p.Body, p.URL)
			p.Title, p.Text = ex.Title, ex.Text
		} else {
			p.Text = string(p.Body)
		}
		if p.Title == "" {
			p.Title = strings.TrimPrefix(path, "/")
		}
		s.files[path] = p
		s.pages[p.URL] = p.Text
		s.index = append(s.index, p)
	}
	sort.Slice(s.index, func(i, j int) bool { return s.index[i].URL < s.index[j].URL })

	mux := http.NewServeMux()
	mux.HandleFunc(searchPath, s.search)
	mux.HandleFunc("/", s.page)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.server.Serve(ln) }()
	return s, nil
}

func (s *site) close() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// page answers a fetch. A request for the root with no file behind it gets
// index.html, the way an ordinary site does.
func (s *site) page(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	p, ok := s.files[path]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	_, _ = w.Write(p.Body)
}

// maxFixtureResults is how many hits the search fixture answers with, which
// is the search tool's own ceiling.
const maxFixtureResults = 10

// search answers the search tool in the shape its provider does.
//
// Every page is a hit, ranked by how many of the query's words are on it.
// Ranking rather than filtering is deliberate: a fixture search that came
// back empty for a phrasing nobody anticipated would fail the case for the
// harness's vocabulary rather than for anything the session did.
func (s *site) search(w http.ResponseWriter, r *http.Request) {
	words := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
	type hit struct {
		page  sitePage
		score int
	}
	hits := make([]hit, 0, len(s.index))
	for _, p := range s.index {
		haystack := strings.ToLower(p.Title + " " + p.Text)
		score := 0
		for _, word := range words {
			if strings.Contains(haystack, word) {
				score++
			}
		}
		hits = append(hits, hit{page: p, score: score})
	}
	// A stable order under equal scores, so two runs of the same case see
	// the same results in the same places.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	type result struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	var body struct {
		Web struct {
			Results []result `json:"results"`
		} `json:"web"`
	}
	for i, h := range hits {
		if i == maxFixtureResults {
			break
		}
		body.Web.Results = append(body.Web.Results, result{
			Title:       h.page.Title,
			URL:         h.page.URL,
			Description: clip(strings.Join(strings.Fields(h.page.Text), " "), 200),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// contentType is what a fixture file is served as. The extension decides,
// because a fixture is a file somebody wrote and its name is the only claim
// about it there is.
func contentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "text/plain; charset=utf-8"
}

// compileFacts turns a case's required facts into the expressions they are
// matched with, naming the one that would not compile.
//
// A fact is an expression and not a phrase because a fact has phrasings: the
// answer "could not confirm" arrives as "I was unable to verify" just as
// often, and a case written in one wording would measure the model's diction.
func compileFacts(path string, facts []string) ([]*regexp.Regexp, error) {
	var out []*regexp.Regexp
	for _, raw := range facts {
		re, err := regexp.Compile(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: fact %q: %w", path, raw, err)
		}
		out = append(out, re)
	}
	return out, nil
}
