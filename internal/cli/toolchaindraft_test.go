package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/secret"
)

// draftAnswers is a model that answers each drafting request with the next
// set of draft_toolchain arguments, and keeps what it was sent.
type draftAnswers struct {
	mu      sync.Mutex
	answers []string
	sent    [][]provider.Message
	offered [][]provider.Tool
}

func (p *draftAnswers) Name() string { return "fake" }

func (p *draftAnswers) StreamCompletion(_ context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	n := len(p.sent)
	p.sent = append(p.sent, msgs)
	p.offered = append(p.offered, opts.Tools)
	args := p.answers[min(n, len(p.answers)-1)]
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "c1", Name: toolchainDraftToolName, Arguments: args}}}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

const (
	pinnedAnswer   = `{"packages":[],"install":[{"line":"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0","provides":["golangci-lint"]}],"hosts":["proxy.golang.org","sum.golang.org"],"check":["golangci-lint"]}`
	unpinnedAnswer = `{"packages":[],"install":[{"line":"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest","provides":["golangci-lint"]}],"hosts":["proxy.golang.org"],"check":["golangci-lint"]}`
)

// draftCheckout is a Go checkout whose Makefile runs a linter.
func draftCheckout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range map[string]string{
		"go.mod":   "module example.com/x\n\ngo 1.24\n",
		"Makefile": "lint:\n\tgolangci-lint run ./...\n",
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func drafterOn(p provider.Provider, root string) toolchainDrafter {
	return toolchainDrafter{prov: p, model: func() string { return "small" }, root: root, dir: root}
}

// The request is offered the draft tool and nothing else, under the
// instruction that carries the grammar, with the checkout's files as data;
// the answer is rendered and read by the loader before it is handed back.
func TestTheDraftIsAskedWithTheGrammarAndReadByTheLoader(t *testing.T) {
	root := draftCheckout(t)
	p := &draftAnswers{answers: []string{pinnedAnswer}}
	got := drafterOn(p, root).draft(context.Background(), false)
	if got.Err != "" {
		t.Fatalf("the draft failed: %s", got.Err)
	}
	if len(p.offered) != 1 || len(p.offered[0]) != 1 || p.offered[0][0].Name != toolchainDraftToolName {
		t.Fatalf("the request offered %v, want the draft tool alone", p.offered)
	}
	system, user := p.sent[0][0].Content, p.sent[0][1].Content
	if !strings.Contains(system, strings.TrimSpace(project.ToolchainGrammar)) {
		t.Fatal("the instruction does not carry the grammar the documentation is generated from")
	}
	for _, want := range []string{"Language: go (1.24)", "--- Makefile ---", "golangci-lint run", "There is no " + project.ToolchainFile} {
		if !strings.Contains(user, want) {
			t.Errorf("the evidence never says %q:\n%s", want, user)
		}
	}
	if _, err := project.ParseToolchain(got.Content); err != nil {
		t.Fatalf("the draft handed back does not load: %v", err)
	}
	if names := got.Provides["go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0"]; len(names) != 1 || names[0] != "golangci-lint" {
		t.Fatalf("what the line provides was lost: %v", got.Provides)
	}
}

// A line the loader refuses goes back to the model once, with the loader's
// own sentence; a second refusal is the drafting's failure, and nothing that
// would not load is handed back.
func TestARefusedDraftIsFedBackOnceWithTheLoadersSentence(t *testing.T) {
	root := draftCheckout(t)
	p := &draftAnswers{answers: []string{unpinnedAnswer, pinnedAnswer}}
	got := drafterOn(p, root).draft(context.Background(), false)
	if got.Err != "" || len(p.sent) != 2 {
		t.Fatalf("a draft refused once and fixed should land (err %q, %d requests)", got.Err, len(p.sent))
	}
	retry := p.sent[1][len(p.sent[1])-1].Content
	_, loaderSays := project.ParseToolchain(project.Toolchain{Install: []string{"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"}, Hosts: []string{"proxy.golang.org"}, Check: []string{"golangci-lint"}}.Render())
	if loaderSays == nil || !strings.Contains(retry, loaderSays.Error()) {
		t.Fatalf("the retry does not carry the loader's sentence %q:\n%s", loaderSays, retry)
	}

	p = &draftAnswers{answers: []string{unpinnedAnswer}}
	got = drafterOn(p, root).draft(context.Background(), false)
	if got.Err == "" || got.Content != nil || len(p.sent) != 2 {
		t.Fatalf("two refusals should fail the draft after one retry (err %q, %d requests)", got.Err, len(p.sent))
	}
}

// A review reads the file as it stands and proposes only changes; answering
// the same four lists is nothing to change.
func TestAReviewThatChangesNothingSaysSo(t *testing.T) {
	root := draftCheckout(t)
	if err := os.MkdirAll(filepath.Join(root, project.StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "install = [\"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0\"]\nhosts = [\"proxy.golang.org\", \"sum.golang.org\"]\ncheck = [\"golangci-lint\"]\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(project.ToolchainFile)), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &draftAnswers{answers: []string{pinnedAnswer}}
	got := drafterOn(p, root).draft(context.Background(), true)
	if !got.Unchanged {
		t.Fatalf("a review answering the same declaration should be nothing to change: %+v", got)
	}
	if !strings.Contains(p.sent[0][1].Content, "(the declaration as it stands)") || !strings.Contains(p.sent[0][0].Content, "propose only changes") {
		t.Fatal("the review was not shown the declaration, or not told to propose only changes")
	}
}

// The write re-reads the bytes through the loader and replaces the file the
// way every file shhh writes is replaced; it refuses to write over a link.
func TestTheDraftIsWrittenOnlyAsADeclarationThatLoads(t *testing.T) {
	root := t.TempDir()
	write := writeToolchainDraft(root)
	if _, err := write([]byte("install = [\"pip install ruff\"]\n")); err == nil {
		t.Fatal("an unpinned declaration was written")
	}
	good := project.Toolchain{Check: []string{"gosec"}}.Render()
	path, err := write(good)
	if err != nil || path != project.ToolchainFile {
		t.Fatalf("write = %q, %v", path, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(project.ToolchainFile))); string(data) != string(good) {
		t.Fatalf("the file holds %q", data)
	}
}

// A run with nobody to answer the card refuses the command in a sentence,
// and leaves every other prompt alone.
func TestAToolchainDraftIsRefusedWhereNobodyCanAnswerTheCard(t *testing.T) {
	for _, p := range []string{"/toolchain", "  /toolchain  ", "/toolchain review"} {
		if err := toolchainCommandRefusal(p); err == nil || !strings.Contains(err.Error(), "nobody to answer") {
			t.Errorf("%q was not refused: %v", p, err)
		}
	}
	for _, p := range []string{"fix the toolchain", "/toolchains", "draft /toolchain"} {
		if err := toolchainCommandRefusal(p); err != nil {
			t.Errorf("%q was refused: %v", p, err)
		}
	}
}

// The draft tool is offered only in its own request, and like every tool a
// surface can put in front of a model it is on the registrable list, which is
// what holds its argument descriptions and its toolbox line.
func TestTheDraftToolIsHeldToTheRegistrableRules(t *testing.T) {
	for _, d := range registrable(t) {
		if d.Name == toolchainDraftToolName {
			return
		}
	}
	t.Fatal("the draft tool is missing from the registrable list, so its descriptions are held to nothing")
}

// A declaration too big to read is not mistaken for no declaration: the
// drafting refuses rather than handing the card a new file to write over it.
func TestAnUnreadableDeclarationIsNotDraftedOver(t *testing.T) {
	root := draftCheckout(t)
	if err := os.MkdirAll(filepath.Join(root, project.StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("# padding\n", 8<<10)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(project.ToolchainFile)), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &draftAnswers{answers: []string{pinnedAnswer}}
	got := drafterOn(p, root).draft(context.Background(), false)
	if got.Err == "" || got.Content != nil || len(p.sent) != 0 {
		t.Fatalf("an oversized declaration was drafted over (err %q, %d requests)", got.Err, len(p.sent))
	}
}

// The evidence is the checkout's own files, so it passes the session's scrub
// before it leaves: a declared value comes back as its name, and a token of
// a known shape nobody declared comes back as its kind.
func TestTheDraftEvidenceIsScrubbedBeforeItLeaves(t *testing.T) {
	const (
		declared = "hunter2-deploy-key-7f3a9c"
		shaped   = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	)
	root := draftCheckout(t)
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "jobs:\n  lint:\n    steps:\n      - run: golangci-lint run ./...\n        env:\n          DEPLOY_KEY: " + declared + "\n          GH_TOKEN: " + shaped + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	v := secret.New()
	if err := v.Add("DEPLOY_KEY", declared); err != nil {
		t.Fatal(err)
	}
	p := &draftAnswers{answers: []string{pinnedAnswer}}
	d := drafterOn(p, root)
	d.scrub = v.Scrub
	if got := d.draft(context.Background(), false); got.Err != "" {
		t.Fatalf("the draft failed: %s", got.Err)
	}
	for _, msgs := range p.sent {
		for _, m := range msgs {
			if strings.Contains(m.Content, declared) || strings.Contains(m.Content, shaped) {
				t.Fatalf("a secret reached the provider:\n%s", m.Content)
			}
		}
	}
	user := p.sent[0][1].Content
	for _, want := range []string{"--- .github/workflows/ci.yml ---", secret.Placeholder("DEPLOY_KEY"), secret.Redacted("github-token")} {
		if !strings.Contains(user, want) {
			t.Errorf("the evidence never says %q:\n%s", want, user)
		}
	}
}
