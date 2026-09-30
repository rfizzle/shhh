package persona

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/secret"
)

func TestNormaliseChatDropsWriting(t *testing.T) {
	d := Draft{Name: "The Skeptic!", Description: "  checks   claims ", Permissions: []string{"write", "Web", "execute", "web"}, Prompt: " be sure ", Reasoning: "Medium"}
	if err := d.Normalise(KindChat); err != nil {
		t.Fatal(err)
	}
	if d.Name != "the-skeptic" || d.Description != "checks claims" || d.Prompt != "be sure" || d.Reasoning != "medium" {
		t.Fatalf("normalised = %+v", d)
	}
	if strings.Join(d.Permissions, ",") != "web" {
		t.Fatalf("chat persona kept writing tiers: %v", d.Permissions)
	}
	if d.Tier() != "read + web" {
		t.Fatalf("tier = %q", d.Tier())
	}
}

// A chat draft that names a writing tool is tidied the way its writing
// tiers are: the tool comes off and the draft is still a card, with what
// was dropped named for it, rather than a refusal the person cannot fix.
func TestNormaliseChatDropsWritingTools(t *testing.T) {
	o, ok := parse(`{"profile":{"name":"skeptic","description":"checks claims","permissions":["web","write"],
		"tools":["read_file","write_file","web_fetch","execute_command"],"prompt":"Doubt."}}`, KindChat)
	if !ok || o.Failed || o.Draft == nil {
		t.Fatalf("chat draft with a writing tool = %+v ok=%v", o, ok)
	}
	d := *o.Draft
	if strings.Join(d.Tools, ",") != "read_file,web_fetch" || d.Writes() {
		t.Fatalf("tools = %v permissions = %v", d.Tools, d.Permissions)
	}
	if strings.Join(d.Dropped, ",") != "write_file,execute_command" {
		t.Fatalf("dropped = %v", d.Dropped)
	}
	// The same list in a coding session is the author's to keep.
	code := Draft{Name: "fixer", Description: "fixes", Permissions: []string{"write"}, Tools: []string{"write_file"}, Prompt: "Fix."}
	if err := code.Normalise(KindCode); err != nil || len(code.Dropped) != 0 || strings.Join(code.Tools, ",") != "write_file" {
		t.Fatalf("code draft tools = %v dropped = %v err = %v", code.Tools, code.Dropped, err)
	}
}

func TestNormaliseCodeKeepsTiersInOrder(t *testing.T) {
	d := Draft{Name: "test-writer", Description: "adds tests", Permissions: []string{"execute", "write"}, Prompt: "write tests"}
	if err := d.Normalise(KindCode); err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Permissions, ",") != "write,execute" || !d.Writes() {
		t.Fatalf("permissions = %v", d.Permissions)
	}
	bad := Draft{Name: "x", Description: "d", Prompt: "p", Reasoning: "lots"}
	if err := bad.Normalise(KindCode); err == nil {
		t.Fatal("bad reasoning accepted")
	}
	empty := Draft{Name: "x", Description: "d"}
	if err := empty.Normalise(KindCode); err == nil {
		t.Fatal("empty prompt accepted")
	}
}

func TestWriteRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	d := Draft{
		Name: "skeptic", Description: `checks "claims" against sources`, Model: "claude-haiku-4-5-20251001",
		Reasoning: "low", Permissions: []string{"web"}, MaxTokens: 300000, Why: "cheap model: wide reads",
		Prompt: "You are the skeptic.\nFind the primary source. Say \"\"\"sure\"\"\" only when it is.",
	}
	path, err := Write(dir, d, KindChat, false)
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "skeptic" || def.Description != d.Description || def.Model != d.Model || def.Reasoning != "low" || def.MaxTokens != 300000 {
		t.Fatalf("loaded = %+v", def)
	}
	if !def.Has(config.PermissionWeb) || def.Writes() {
		t.Fatalf("permissions = %v", def.Permissions)
	}
	if !strings.Contains(def.Prompt, "primary source") {
		t.Fatalf("prompt = %q", def.Prompt)
	}
	if _, err := Write(dir, d, KindChat, false); err == nil {
		t.Fatal("overwrote an existing profile")
	}
	if _, err := Write(dir, d, KindChat, true); err != nil {
		t.Fatal(err)
	}
}

func TestParseDraftAndQuestions(t *testing.T) {
	o, ok := parse(`{"profile":{"name":"Docs Keeper","description":"updates stale docs","permissions":["write"],"prompt":"Fix docs."}}`, KindCode)
	if !ok || o.Failed || o.Draft == nil || o.Draft.Name != "docs-keeper" {
		t.Fatalf("draft = %+v ok=%v", o, ok)
	}
	o, ok = parse("Sure, here you go: {\"questions\":[\"Which language?\",\"  \",\"Run tests too?\"]} done", KindCode)
	if !ok || len(o.Questions) != 2 {
		t.Fatalf("questions = %+v ok=%v", o, ok)
	}
	o, ok = parse(`{"profile":{"name":"","description":"x","permissions":[],"prompt":"p"}}`, KindChat)
	if !ok || !o.Failed {
		t.Fatalf("unloadable draft not reported: %+v", o)
	}
	if _, ok := parse("not json", KindChat); ok {
		t.Fatal("garbage parsed")
	}
}

// A reviewer runs the project's own checks and changes nothing, so the tier
// it needs is the one every profile has: the gate is a read
// (docs/capabilities/subagents.md#a-profile-that-changes-nothing-can-still-run-the-checks).
// The draft that says so must survive normalising, rendering and the loader,
// and the same draft with a tier that puts the agent in a copy of the
// checkout must be refused before a file exists.
func TestReviewerDraftRunsTheGateOnRead(t *testing.T) {
	const answer = `{"profile":{"name":"security-reviewer","description":"reads a diff for security problems and reports by severity",
		"permissions":["read"],"tools":["read_file","search","quality_gate","quality_gate"],
		"prompt":"Read the declared diff first. Verify with the default suite.","why":"the gate needs no execute"}}`
	o, ok := parse(answer, KindCode)
	if !ok || o.Failed || o.Draft == nil {
		t.Fatalf("reviewer draft = %+v ok=%v", o, ok)
	}
	d := *o.Draft
	if len(d.Permissions) != 0 || d.Writes() || d.Tier() != "read" {
		t.Fatalf("reviewer is not read-only: permissions=%v tier=%q", d.Permissions, d.Tier())
	}
	if strings.Join(d.Tools, ",") != "read_file,search,quality_gate" {
		t.Fatalf("tools = %v", d.Tools)
	}
	if body := Render(d, KindCode); !strings.Contains(body, "permissions = [] # read only") ||
		!strings.Contains(body, `tools = ["read_file", "search", "quality_gate"]`) {
		t.Fatalf("rendered = %q", body)
	}
	dir := filepath.Join(t.TempDir(), "agents")
	path, err := Write(dir, d, KindCode, false)
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Writes() || !def.Allows(config.QualityGateTool) || !def.Has(config.PermissionRead) {
		t.Fatalf("loaded profile has no gate on read: %+v", def)
	}

	// The same allowlist beside a tier that works in a copy of the checkout
	// is the loader's refusal, heard while the draft is still a card.
	const executing = `{"profile":{"name":"security-reviewer","description":"reads a diff",
		"permissions":["execute"],"tools":["quality_gate"],"prompt":"Read the diff."}}`
	bad, ok := parse(executing, KindCode)
	if !ok || !bad.Failed || !strings.Contains(bad.Err, "changes nothing") {
		t.Fatalf("gate beside execute not refused: %+v ok=%v", bad, ok)
	}
	writing := d
	writing.Permissions = []string{"write"}
	if _, err := Write(dir, writing, KindCode, true); err == nil {
		t.Fatal("gate beside write was written")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v err = %v", entries, err)
	}
}

func TestPromptsLeanBySession(t *testing.T) {
	chat, code := systemPrompt(KindChat), systemPrompt(KindCode)
	if !strings.Contains(chat, "never grant write") || !strings.Contains(chat, "notebook") {
		t.Fatal("chat prompt does not read as a read-only persona")
	}
	if !strings.Contains(code, "patch") || !strings.Contains(code, "verifies") {
		t.Fatal("code prompt does not read as an engineering role")
	}
	// The gate is a read: a role that only runs the project's checks
	// is not told to grant execute for them.
	if !strings.Contains(code, config.QualityGateTool) || strings.Contains(code, `"execute" if it runs anything`) {
		t.Fatal("code prompt still buys a test run with execute")
	}
	u := userPrompt(Request{Kind: KindChat, Brief: "a skeptic", Existing: []string{"researcher"}, Current: &Draft{Name: "skeptic"}, Feedback: "gentler"})
	for _, want := range []string{"researcher", "a skeptic", "CURRENT DRAFT", "gentler"} {
		if !strings.Contains(u, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
	if len(Suggestions(KindChat)) == 0 || Suggestions(KindChat)[0] == Suggestions(KindCode)[0] {
		t.Fatal("suggestions do not differ by session")
	}
}

func TestExistingSorted(t *testing.T) {
	got := Existing(map[string]config.AgentDefinition{"zed": {}, "researcher": {}}, "writer", "researcher")
	if strings.Join(got, ",") != "researcher,writer,zed" {
		t.Fatalf("existing = %v", got)
	}
}

// The drafter answers the prompt by section name, and a section it left
// empty stays an empty section on the draft rather than vanishing: the
// written prompt is the filled ones under their headings, and the file reads
// back to the same five.
func TestDraftAnswersInSections(t *testing.T) {
	o, ok := parse(`{"profile":{"name":"docs-keeper","description":"updates stale docs","permissions":["write"],
		"sections":{"purpose":"Keep the docs true.","scope":"docs/ only.","restrictions":"",
		"method":"Read the change, then the docs it names.","report":"The files changed and why."}}}`, KindCode)
	if !ok || o.Failed || o.Draft == nil || o.Draft.Sections == nil {
		t.Fatalf("sectioned draft = %+v ok=%v", o, ok)
	}
	d := *o.Draft
	if empty := d.Sections.Empty(); strings.Join(empty, ",") != config.SectionRestrictions {
		t.Fatalf("empty sections = %v", empty)
	}
	const want = "## Purpose\n\nKeep the docs true.\n\n## Scope\n\ndocs/ only.\n\n## Method\n\nRead the change, then the docs it names.\n\n## Report\n\nThe files changed and why."
	if d.Prompt != want {
		t.Fatalf("prompt = %q", d.Prompt)
	}
	path, err := Write(filepath.Join(t.TempDir(), "agents"), d, KindCode, false)
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file's multi-line string ends on a line of its own; the child is
	// handed the prompt trimmed, so the text it reads is the draft's.
	if strings.TrimSpace(def.Prompt) != want {
		t.Fatalf("the file's prompt is not the draft's: %q", def.Prompt)
	}
	if got := sectionsOf(def.Sections()); *got != *d.Sections {
		t.Fatalf("read back = %+v, drafted %+v", *got, *d.Sections)
	}

	// Revised, the drafter sees the sections by name, the empty one
	// included, and not the prompt assembled from them a second time.
	u := userPrompt(Request{Kind: KindCode, Brief: "docs", Current: &d, Feedback: "add a restriction"})
	if !strings.Contains(u, `"restrictions": ""`) || strings.Contains(u, "## Purpose") {
		t.Fatalf("current draft as sent:\n%s", u)
	}
}

// An answer in the older shape — one prompt — is still a draft, and reads as
// the loader reads a prompt with no headings: one Purpose section, written
// back as the text it was.
func TestAPromptAnswerIsItsPurpose(t *testing.T) {
	o, ok := parse(`{"profile":{"name":"fixer","description":"fixes","permissions":["write"],"prompt":"Fix the bug."}}`, KindCode)
	if !ok || o.Failed || o.Draft == nil {
		t.Fatalf("draft = %+v ok=%v", o, ok)
	}
	d := *o.Draft
	if d.Prompt != "Fix the bug." || d.Sections == nil || d.Sections.Purpose != "Fix the bug." ||
		len(d.Sections.Empty()) != 4 {
		t.Fatalf("draft = %+v sections = %+v", d, d.Sections)
	}
	if err := d.Normalise(KindCode); err != nil || d.Prompt != "Fix the bug." {
		t.Fatalf("normalising again gave the prompt a heading: %q err=%v", d.Prompt, err)
	}
}

// The tool asks for the five by name and requires every one, which is what
// makes an empty section an answer rather than an omission.
func TestDraftSchemaAsksForTheSectionsByName(t *testing.T) {
	var schema struct {
		Properties struct {
			Profile struct {
				Required   []string `json:"required"`
				Properties struct {
					Sections struct {
						Required   []string                   `json:"required"`
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"sections"`
				} `json:"properties"`
			} `json:"profile"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DraftTool().Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	profile := schema.Properties.Profile
	if !slices.Contains(profile.Required, "sections") || slices.Contains(profile.Required, "prompt") {
		t.Fatalf("profile requires %v", profile.Required)
	}
	var want []string
	for _, name := range config.PromptSectionNames() {
		want = append(want, strings.ToLower(name))
	}
	if strings.Join(profile.Properties.Sections.Required, ",") != strings.Join(want, ",") {
		t.Fatalf("sections required = %v, want %v", profile.Properties.Sections.Required, want)
	}
	for _, name := range want {
		if _, ok := profile.Properties.Sections.Properties[name]; !ok {
			t.Errorf("schema has no %q section", name)
		}
	}
}

// A revision changes a section and the prompt is written from the five, so
// a later Normalise — which rebuilds the prompt from the sections — keeps
// it; clearing every section leaves a prompt the loader refuses rather than
// the text the sections had before.
func TestSetSectionIsThePromptsOneWriter(t *testing.T) {
	d := Draft{Name: "tester", Description: "adds tests", Prompt: "Add tests."}
	if !d.SetSection(config.SectionRestrictions, "  Never delete a test.  ") {
		t.Fatal("Restrictions is a section")
	}
	if d.SetSection("Tools", "x") {
		t.Fatal("Tools is not a prose section")
	}
	if d.Sections.Purpose != "Add tests." || d.Sections.Restrictions != "Never delete a test." {
		t.Fatalf("sections = %+v", d.Sections)
	}
	before := d.Prompt
	if err := d.Normalise(KindCode); err != nil {
		t.Fatal(err)
	}
	if d.Prompt != before || !strings.Contains(d.Prompt, "Never delete a test.") {
		t.Fatalf("Normalise moved the revised prompt:\n%s\nwas\n%s", d.Prompt, before)
	}
	for _, name := range config.PromptSectionNames() {
		d.SetSection(name, "")
	}
	if d.Prompt != "" {
		t.Fatalf("a draft with every section cleared kept a prompt: %q", d.Prompt)
	}
	if err := d.Normalise(KindCode); err == nil {
		t.Fatal("a draft with every section cleared should not load")
	}
}

// A revision of one section tells the drafter which section it is and that
// every other one is fixed context.
func TestASectionRevisionNamesItsSection(t *testing.T) {
	cur := &Draft{Name: "tester", Description: "adds tests",
		Sections: &Sections{Purpose: "Add tests.", Method: "Read, then write."}}
	prompt := userPrompt(Request{Kind: KindCode, Brief: "tests", Current: cur, Section: config.SectionMethod, Feedback: "run go vet"})
	for _, want := range []string{"Revise the Method section only", "Every other section and field is fixed",
		"Only Method is taken from your answer", "What the person said about Method:\nrun go vet", "Read, then write."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the revision request lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "revise the draft to match") {
		t.Fatalf("a section's revision should not ask for the whole draft:\n%s", prompt)
	}
}

// A note on the whole draft names no section: every section is revised, the
// fields are fixed, and the sections the person wrote are named as fixed too.
func TestAWholeDraftRevisionFixesTheFieldsAndTheSectionsKept(t *testing.T) {
	cur := &Draft{Name: "tester", Description: "adds tests",
		Sections: &Sections{Purpose: "Add tests.", Scope: "One package.", Method: "Read, then write."}}
	prompt := userPrompt(Request{Kind: KindCode, Brief: "tests", Current: cur, Feedback: "terser",
		Keep: []string{config.SectionScope, config.SectionMethod}})
	for _, want := range []string{"Revise every section to match what the person said about the whole draft",
		"The name, description, permissions, tools, model, reasoning and budget are fixed",
		"The person wrote the Scope and Method sections themselves, so they are fixed too",
		"Only the sections are taken from your answer", "What the person said about the whole draft:\nterser"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the whole-draft request lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "section only") {
		t.Fatalf("a whole-draft note should not name one section:\n%s", prompt)
	}
	one := userPrompt(Request{Kind: KindCode, Brief: "tests", Current: cur, Feedback: "terser", Keep: []string{config.SectionScope}})
	if !strings.Contains(one, "The person wrote the Scope section themselves, so it is fixed too: answer it exactly as it is") {
		t.Fatalf("one kept section should read as one:\n%s", one)
	}
	if none := userPrompt(Request{Kind: KindCode, Brief: "tests", Current: cur, Feedback: "terser"}); strings.Contains(none, "themselves") {
		t.Fatalf("no kept section should name none:\n%s", none)
	}
}

// The Commands section is two fields of the file: the drafter may answer
// them, the renderer writes them, and the loader reads them back as the
// deny list and the intent the child is run under.
func TestTheCommandsSectionIsWrittenAndReadBack(t *testing.T) {
	var schema struct {
		Properties struct {
			Profile struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"profile"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DraftTool().Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"intent", "deny"} {
		if _, ok := schema.Properties.Profile.Properties[field]; !ok {
			t.Fatalf("the draft schema does not offer %q", field)
		}
	}
	if _, ok := schema.Properties.Profile.Properties["allow"]; ok {
		t.Fatal("the draft schema offers an allow list")
	}
	dir := filepath.Join(t.TempDir(), "agents")
	d := Draft{Name: "tester", Description: "adds tests", Permissions: []string{"write", "execute"},
		Prompt: "Add tests.", MaxTokens: 300000,
		Intent: "  running the\npackage's tests ", Deny: []string{"git push", " git  push ", "", `rm "-rf"`}}
	path, err := Write(dir, d, KindCode, false)
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if def.Intent != "running the package's tests" || strings.Join(def.Deny, "|") != `git push|rm "-rf"` {
		t.Fatalf("loaded intent %q deny %q", def.Intent, def.Deny)
	}
}

// SetCommands reads the editor's lines: intent and deny, comments and blank
// lines skipped; anything else is refused with its line, and an allow line
// in the loader's own words, leaving the draft as it was.
func TestSetCommandsReadsTheEditorsLines(t *testing.T) {
	d := Draft{Intent: "before", Deny: []string{"git push"}}
	if err := d.SetCommands("# guide\n\nIntent: run the tests\ndeny: go install\ndeny: go install\n"); err != nil {
		t.Fatal(err)
	}
	if d.Intent != "run the tests" || strings.Join(d.Deny, ",") != "go install" {
		t.Fatalf("read %q %v", d.Intent, d.Deny)
	}
	if got := d.CommandsText(); got != "intent: run the tests\ndeny: go install" {
		t.Fatalf("CommandsText = %q", got)
	}
	for text, want := range map[string]string{
		"allow: go test": "a profile carries no allowlist",
		"run: go test":   `line 1: start it with "intent:" or "deny:"`,
		"intent: " + strings.Repeat("x", config.MaxIntentChars+1): "intent:",
	} {
		if err := d.SetCommands(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("SetCommands(%.20q) = %v, want %q", text, err, want)
		}
	}
	if d.Intent != "run the tests" || strings.Join(d.Deny, ",") != "go install" {
		t.Fatalf("a refused text changed the draft: %q %v", d.Intent, d.Deny)
	}
}

// A revision of the Commands section names its two fields, since they are
// not a heading in the prompt the drafter could find by name.
func TestACommandsRevisionNamesItsFields(t *testing.T) {
	cur := &Draft{Name: "tester", Description: "adds tests", Sections: &Sections{Purpose: "Add tests."}}
	prompt := userPrompt(Request{Kind: KindCode, Brief: "tests", Current: cur, Section: SectionCommands, Feedback: "never push"})
	for _, want := range []string{"Revise the Commands section (the intent and deny fields) only",
		"Only Commands is taken from your answer", "What the person said about Commands:\nnever push"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the Commands revision request lacks %q:\n%s", want, prompt)
		}
	}
}

// The prompt is a TOML basic string, where a backslash is an escape: a
// drafted \d+ written as it stands is read back as something else or not
// at all. Whatever the prompt holds, the file the loader reads must hand it
// back unchanged.
func TestRenderedPromptSurvivesTheLoader(t *testing.T) {
	prompt := "Match lines with `\\d+` and split on a literal \\n.\n" +
		`Quote him: """exactly""" and '''verbatim'''.` + "\n" +
		`A path ends C:\dir\ and a run of quotes """"".`
	d := Draft{Name: "matcher", Description: `matches \d+ in logs`, Intent: `finds \d+ runs`, Prompt: prompt}
	path := filepath.Join(t.TempDir(), "matcher.toml")
	if err := os.WriteFile(path, []byte(Render(d, KindCode)), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSuffix(def.Prompt, "\n"); got != prompt {
		t.Fatalf("prompt = %q, want %q", got, prompt)
	}
	if def.Description != d.Description || def.Intent != d.Intent {
		t.Fatalf("description = %q intent = %q", def.Description, def.Intent)
	}
}

// sentProvider keeps every message of the one request it is sent and
// answers with a question, so a test can read what left the machine.
type sentProvider struct{ msgs []provider.Message }

func (p *sentProvider) Name() string { return "sent" }

func (p *sentProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.msgs = append([]provider.Message(nil), msgs...)
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{Name: DraftToolName, Arguments: `{"questions":["which paths?"]}`}}}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

// What the person typed into the drafter — the brief, their answers, a note
// on a section, on the Commands section or on the whole draft — passes the
// session's scrub before the request leaves: a declared value comes back as
// its name and a token of a known shape as its kind, in every message sent.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func TestTheDrafterScrubsWhatThePersonTyped(t *testing.T) {
	const (
		declared = "hunter2-deploy-key-7f3a9c"
		shaped   = "sk-ant-api03-9Qw3RtY6uIoP0aSdFgHjKlZxCvBnM4eR7tY2uI9oP1aSdFgH"
	)
	v := secret.New()
	if err := v.Add("DEPLOY_KEY", declared); err != nil {
		t.Fatal(err)
	}
	pasted := "deploy with " + declared + " and call the API with " + shaped
	current := &Draft{Name: "deployer", Description: "deploys", Sections: &Sections{Purpose: "Deploy."}}
	for name, req := range map[string]Request{
		"brief":    {Kind: KindCode, Brief: pasted},
		"answers":  {Kind: KindCode, Brief: "a deployer", Exchange: []QA{{Question: "which key?", Answer: pasted}}},
		"section":  {Kind: KindCode, Brief: "a deployer", Current: current, Section: config.SectionMethod, Feedback: pasted},
		"commands": {Kind: KindCode, Brief: "a deployer", Current: current, Section: SectionCommands, Feedback: pasted},
		"whole":    {Kind: KindCode, Brief: "a deployer", Current: current, Feedback: pasted},
	} {
		p := &sentProvider{}
		NewDrafter(p, Config{Model: "m", Scrub: v.Scrub}).Draft(context.Background(), req)
		if len(p.msgs) == 0 {
			t.Fatalf("%s: nothing was sent", name)
		}
		var all strings.Builder
		for _, m := range p.msgs {
			if strings.Contains(m.Content, declared) || strings.Contains(m.Content, shaped) {
				t.Fatalf("%s: a secret reached the provider in the %s message: %q", name, m.Role, m.Content)
			}
			all.WriteString(m.Content)
		}
		if !strings.Contains(all.String(), secret.Placeholder("DEPLOY_KEY")) || !strings.Contains(all.String(), secret.Redacted("anthropic-key")) {
			t.Errorf("%s: the request was not scrubbed to the placeholders: %q", name, all.String())
		}
	}

	// Nil is a session with no secrets: the text goes as typed.
	p := &sentProvider{}
	NewDrafter(p, Config{Model: "m"}).Draft(context.Background(), Request{Kind: KindCode, Brief: pasted})
	if len(p.msgs) == 0 || !strings.Contains(p.msgs[len(p.msgs)-1].Content, declared) {
		t.Fatalf("with no scrub the brief should go as typed: %+v", p.msgs)
	}
}
