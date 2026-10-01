package persona

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/secret"
)

// answerProvider answers every request with the tool arguments it holds and
// keeps the messages it was sent.
type answerProvider struct {
	args string
	msgs []provider.Message
}

func (p *answerProvider) Name() string { return "answer" }

func (p *answerProvider) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.msgs = append([]provider.Message(nil), msgs...)
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{{Name: DraftToolName, Arguments: p.args}}}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

// olderCritic is a profile in the shapes the person's own take: a comment
// header, a mode, reviews, a round cap, xhigh reasoning and a prompt written
// as one block.
const olderCritic = `# critic — argues against a diff; kept read-only on purpose.
# Written by hand before the sections existed.

description = "reads a diff and argues"
reasoning = "xhigh" # deeper than a draft could propose
mode = "read-only"
reviews = true
max_rounds = 30
tools = [
  "read_file", # the diff's files
  "search",
]
prompt = """
You are a critic. Read the diff you are given and argue against it: what it breaks, what it assumes, what it leaves untested. Only read files; never edit or run anything. Give each objection a file and line."""
max_tokens = 400000
`

// The migration's answer is read for the five sections and nothing else:
// whatever it says about the name, tiers, tools or fields, the draft keeps
// the file's, so a reader cannot come out writing.
func TestMigrationTakesOnlyTheSectionsFromTheAnswer(t *testing.T) {
	src := config.AgentDefinition{
		Name: "critic", Description: "reads a diff and argues", Reasoning: "xhigh",
		Mode: "read-only", Reviews: true, MaxRounds: 30, Tools: []string{"read_file", "search"},
		Intent: "reading only", Deny: []string{"git push"},
		Prompt: "You are a critic. Only read files; never edit or run anything. Give each objection a file and line.",
	}
	p := &answerProvider{args: `{"profile":{"name":"writer","description":"edits everything","permissions":["write","execute"],
		"tools":["execute_command"],"model":"big","reasoning":"off","intent":"anything","deny":[],"max_tokens":900000,
		"sections":{"purpose":"You are a critic.","scope":"","restrictions":"Only read files; never edit or run anything.","method":"","report":"Give each objection a file and line."}}}`}
	o := NewDrafter(p, Config{Model: "m"}).Draft(context.Background(), Request{Kind: KindChat, Source: &src})
	if o.Failed || o.Draft == nil {
		t.Fatalf("outcome = %+v", o)
	}
	d := *o.Draft
	want := FromDefinition(src)
	if d.Name != want.Name || d.Description != want.Description || d.Model != "" || d.Reasoning != "xhigh" ||
		len(d.Permissions) != 0 || !slices.Equal(d.Tools, src.Tools) || d.Intent != src.Intent ||
		!slices.Equal(d.Deny, src.Deny) || d.MaxTokens != 0 || d.Writes() {
		t.Fatalf("a field was taken from the answer: %+v", d)
	}
	if d.Sections.Purpose != "You are a critic." || d.Sections.Scope != "" || d.Sections.Method != "" ||
		d.Sections.Report != "Give each objection a file and line." {
		t.Fatalf("sections = %+v", d.Sections)
	}
	if !strings.Contains(d.Prompt, "## Restrictions\n\nOnly read files") {
		t.Fatalf("prompt not written from the sections: %q", d.Prompt)
	}
	sent := p.msgs[len(p.msgs)-1].Content
	for _, want := range []string{"verbatim", "empty string", "never invent", "Only the five sections are taken", src.Prompt} {
		if !strings.Contains(sent, want) {
			t.Errorf("the request lacks %q:\n%s", want, sent)
		}
	}

	// An answer that moved nothing, or asked instead, is a failure in words.
	for _, args := range []string{
		`{"profile":{"name":"critic","sections":{"purpose":"","scope":"","restrictions":"","method":"","report":""}}}`,
		`{"questions":["what is it for?"]}`,
	} {
		p := &answerProvider{args: args}
		if o := NewDrafter(p, Config{Model: "m"}).Draft(context.Background(), Request{Kind: KindCode, Source: &src}); !o.Failed || o.Err == "" {
			t.Fatalf("%s: outcome = %+v", args, o)
		}
	}
}

// The profile's prompt is the author's text and may hold a pasted token, so
// it passes the session's scrub before the migration request leaves.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func TestMigrationScrubsTheProfilesPrompt(t *testing.T) {
	const declared = "hunter2-deploy-key-7f3a9c"
	v := secret.New()
	if err := v.Add("DEPLOY_KEY", declared); err != nil {
		t.Fatal(err)
	}
	src := config.AgentDefinition{Name: "deployer", Description: "deploys", Prompt: "Deploy with " + declared + "."}
	p := &sentProvider{}
	NewDrafter(p, Config{Model: "m", Scrub: v.Scrub}).Draft(context.Background(), Request{Kind: KindCode, Source: &src})
	if len(p.msgs) == 0 {
		t.Fatal("nothing was sent")
	}
	for _, m := range p.msgs {
		if strings.Contains(m.Content, declared) {
			t.Fatalf("the secret reached the provider: %q", m.Content)
		}
	}
	if !strings.Contains(p.msgs[len(p.msgs)-1].Content, secret.Placeholder("DEPLOY_KEY")) {
		t.Fatalf("not scrubbed to the placeholder: %q", p.msgs[len(p.msgs)-1].Content)
	}
}

// writeProfile puts a profile file in a fresh directory and opens it.
func writeProfile(t *testing.T, name, text string) (*Source, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return src, path
}

// sectioned moves a draft into the five sections the way a migration does.
func sectioned(d Draft) Draft {
	d.SetSection(config.SectionPurpose, "You are a critic.")
	d.SetSection(config.SectionScope, "The diff you are given.")
	d.SetSection(config.SectionRestrictions, "Only read files; never edit or run anything.")
	d.SetSection(config.SectionMethod, "Read the diff, then argue against it.")
	d.SetSection(config.SectionReport, "Give each objection a file and line.")
	return d
}

// Saving an opened profile rewrites the prompt in place and nothing else:
// every other key and every comment is the file's byte for byte, in each of
// the shapes a hand-written profile takes.
func TestPersona_SavingAnOpenedProfileKeepsEveryOtherKeyAndComment(t *testing.T) {
	cases := map[string]string{
		"a comment header, mode, reviews, rounds, xhigh and a tools list": olderCritic,
		"a literal prompt that is not last": `description = "reads a diff"
prompt = '''
You are a critic.'''   # the author's own
mode = "read-only"
`,
		"a basic one-line prompt": `# terse
description = "reads a diff"
permissions = ["web"]
prompt = "You are a critic. Only read."
inherit = 2
`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			src, path := writeProfile(t, "critic", text)
			before := src.Def
			d := sectioned(FromDefinition(src.Def))
			if _, err := SaveOpened(src, d); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(path)
			entries, err := scanTOML(text)
			if err != nil {
				t.Fatal(err)
			}
			var prompt tomlEntry
			for _, e := range entries {
				if e.key == "prompt" {
					prompt = e
				}
			}
			want := text[:prompt.valStart] + tomlPrompt(d.Prompt) + text[prompt.valEnd:]
			if string(got) != want {
				t.Fatalf("the file changed beyond its prompt:\n--- got\n%s\n--- want\n%s", got, want)
			}
			after, err := config.LoadAgentFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !after.Current() || after.Mode != before.Mode || after.Reviews != before.Reviews ||
				after.MaxRounds != before.MaxRounds || after.Reasoning != before.Reasoning ||
				!slices.Equal(after.Tools, before.Tools) || after.Inherit != before.Inherit ||
				!slices.Equal(after.Permissions, before.Permissions) {
				t.Fatalf("read back = %+v, opened = %+v", after, before)
			}
		})
	}

	// A field revised on the surface is written where the file has it, or
	// added above the prompt, and nothing else moves.
	src, path := writeProfile(t, "critic", olderCritic)
	d := FromDefinition(src.Def)
	if err := d.SetCommands("deny: git push"); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveOpened(src, d); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := strings.Replace(olderCritic, `prompt = """`, "deny = [\"git push\"]\nprompt = \"\"\"", 1); string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A profile whose prompt lives in a prompt_file is saved into that file, and
// its TOML is left alone.
func TestPersona_APromptFileProfileIsSavedIntoItsPromptFile(t *testing.T) {
	dir := t.TempDir()
	text := "# a long prompt kept apart\ndescription = \"reads a diff\"\nprompt_file = \"critic-prompt.txt\"\n"
	path := filepath.Join(dir, "critic.toml")
	promptPath := filepath.Join(dir, "critic-prompt.txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("You are a critic. Only read.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if src.PromptPath != promptPath || !src.Older() {
		t.Fatalf("opened = %+v", src)
	}
	d := sectioned(FromDefinition(src.Def))
	lines, err := src.Diff(d)
	if err != nil || len(lines) == 0 || !strings.Contains(lines[0], "critic-prompt.txt") {
		t.Fatalf("the diff should be the prompt file's: %q, %v", lines, err)
	}
	if _, err := SaveOpened(src, d); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != text {
		t.Fatalf("the TOML changed:\n%s", got)
	}
	if got, _ := os.ReadFile(promptPath); string(got) != d.Prompt+"\n" {
		t.Fatalf("prompt file = %q", got)
	}
	if def, err := config.LoadAgentFile(path); err != nil || !def.Current() {
		t.Fatalf("read back = %+v, %v", def, err)
	}
}

// A file changed on disk since it was opened is not overwritten, and neither
// is a file the loader would refuse: the save is refused in a sentence and
// the file is as it was.
func TestPersona_AFileChangedSinceItWasOpenedIsNotOverwritten(t *testing.T) {
	src, path := writeProfile(t, "critic", olderCritic)
	edited := strings.Replace(olderCritic, "max_rounds = 30", "max_rounds = 12", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := SaveOpened(src, sectioned(FromDefinition(src.Def)))
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "changed on disk since it was opened") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != edited {
		t.Fatal("the changed file was overwritten")
	}

	src, path = writeProfile(t, "critic", olderCritic)
	d := FromDefinition(src.Def)
	d.Permissions, d.Tools = []string{"write"}, []string{config.QualityGateTool}
	if _, err := SaveOpened(src, d); err == nil || !strings.Contains(err.Error(), "quality_gate") {
		t.Fatalf("a profile the loader refuses should be refused in its words, err = %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, []byte(olderCritic)) {
		t.Fatal("a refused save touched the file")
	}
}

// The scanner finds every top-level key's value however it is written.
func TestScanTOMLFindsEachValue(t *testing.T) {
	entries, err := scanTOML(olderCritic)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range entries {
		keys = append(keys, e.key)
	}
	want := []string{"description", "reasoning", "mode", "reviews", "max_rounds", "tools", "prompt", "max_tokens"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v", keys)
	}
	for _, e := range entries {
		if e.key == "reasoning" && olderCritic[e.valStart:e.valEnd] != `"xhigh"` {
			t.Fatalf("reasoning value = %q", olderCritic[e.valStart:e.valEnd])
		}
	}
}

// A file that starts with a byte-order mark is read past it, and the save
// keeps it.
func TestABOMProfileIsSavedInPlace(t *testing.T) {
	text := "\xef\xbb\xbf" + olderCritic
	src, path := writeProfile(t, "critic", text)
	if _, err := SaveOpened(src, sectioned(FromDefinition(src.Def))); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "\xef\xbb\xbf# critic") {
		t.Fatalf("the mark or the header was lost:\n%q", got[:20])
	}
	if def, err := config.LoadAgentFile(path); err != nil || !def.Current() {
		t.Fatalf("read back = %+v, %v", def, err)
	}
}
