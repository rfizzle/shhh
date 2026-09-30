package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sectionBodies(secs []PromptSection) map[string]string {
	out := map[string]string{}
	for _, s := range secs {
		out[s.Name] = s.Body
	}
	return out
}

// A sectioned prompt reads into the five, and it reads the same whether the
// profile carries it inline or in a prompt_file — the child is handed the
// text as written either way.
func TestProfilePromptReadsItsSections(t *testing.T) {
	const prompt = `## Purpose
Add table-driven tests.

## Scope
Test files only.

## Restrictions
Never edit the code under test.

## Method
Run the package's tests.

` + "```markdown\n## Report\nnot a heading: this is an example inside a fence\n```" + `

## Report
What was added, and how often it ran.
`
	dir := t.TempDir()
	writeAgent(t, dir, "inline.toml", "prompt = '''\n"+prompt+"'''\n")
	writeAgent(t, dir, "filed.md", prompt)
	writeAgent(t, dir, "filed.toml", `prompt_file = "filed.md"`)
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	inline, filed := defs["inline"], defs["filed"]
	if inline.Prompt != prompt || filed.Prompt != prompt {
		t.Fatalf("the prompt is not kept as written:\ninline %q\nfiled  %q", inline.Prompt, filed.Prompt)
	}
	for _, def := range []AgentDefinition{inline, filed} {
		got := sectionBodies(def.Sections())
		want := map[string]string{
			SectionPurpose:      "Add table-driven tests.",
			SectionScope:        "Test files only.",
			SectionRestrictions: "Never edit the code under test.",
			SectionMethod:       "Run the package's tests.\n\n```markdown\n## Report\nnot a heading: this is an example inside a fence\n```",
			SectionReport:       "What was added, and how often it ran.",
		}
		for name, body := range want {
			if got[name] != body {
				t.Errorf("%s: %s = %q, want %q", def.Name, name, got[name], body)
			}
		}
	}
}

// A profile written before the sections existed loads unchanged and reads
// as one Purpose section, the other four present and empty.
func TestAPromptWithNoHeadingsIsItsPurpose(t *testing.T) {
	dir := t.TempDir()
	const prompt = "# Reviewing\nYou are reviewing a change.\n\n## Findings\nRank by severity."
	writeAgent(t, dir, "old.toml", "prompt = '''\n"+prompt+"'''\n")
	defs, err := LoadAgentsFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	def := defs["old"]
	if def.Prompt != prompt {
		t.Fatalf("prompt = %q", def.Prompt)
	}
	secs := def.Sections()
	if len(secs) != 5 {
		t.Fatalf("sections = %+v", secs)
	}
	for i, name := range PromptSectionNames() {
		if secs[i].Name != name {
			t.Fatalf("section %d is %q, want %q", i, secs[i].Name, name)
		}
	}
	if secs[0].Body != prompt {
		t.Fatalf("purpose = %q", secs[0].Body)
	}
	for _, s := range secs[1:] {
		if s.Body != "" {
			t.Fatalf("%s is not empty: %q", s.Name, s.Body)
		}
	}
	if got := WritePromptSections(secs); got != prompt {
		t.Fatalf("a prompt with no headings was given one by being written back: %q", got)
	}
}

func TestReadPromptSectionsEdges(t *testing.T) {
	cases := []struct {
		name, prompt string
		want         map[string]string
	}{
		{
			"text above the first heading is Purpose",
			"Lead.\n## Scope\nHere.",
			map[string]string{SectionPurpose: "Lead.", SectionScope: "Here."},
		},
		{
			"a heading is matched case aside, trailing space aside",
			"## purpose  \nJob.\n## METHOD\nHow.",
			map[string]string{SectionPurpose: "Job.", SectionMethod: "How."},
		},
		{
			"an unknown heading belongs to its section",
			"## Method\nRun it.\n## Examples\nOne.",
			map[string]string{SectionMethod: "Run it.\n## Examples\nOne."},
		},
		{
			"a tilde fence hides a heading, and a shorter closer does not close it",
			"## Method\n~~~~\n## Report\n~~~\nstill fenced\n~~~~\n## Report\nDone.",
			map[string]string{
				SectionMethod: "~~~~\n## Report\n~~~\nstill fenced\n~~~~",
				SectionReport: "Done.",
			},
		},
		{
			"a backtick fence is not closed by tildes",
			"## Scope\n```\n~~~\n## Restrictions\n```\n## Restrictions\nNone.",
			map[string]string{
				SectionScope:        "```\n~~~\n## Restrictions\n```",
				SectionRestrictions: "None.",
			},
		},
		{
			"a heading written twice gathers both bodies",
			"## Report\nFirst.\n## Scope\nS.\n## Report\nSecond.",
			map[string]string{SectionReport: "First.\n\nSecond.", SectionScope: "S."},
		},
		{
			"a heading with no space, or indented, is text",
			"##Scope\n ## Scope\nx",
			map[string]string{SectionPurpose: "##Scope\n ## Scope\nx"},
		},
		{
			"CRLF line ends",
			"## Purpose\r\nJob.\r\n## Report\r\nThis.",
			map[string]string{SectionPurpose: "Job.", SectionReport: "This."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sectionBodies(ReadPromptSections(c.prompt))
			for _, name := range PromptSectionNames() {
				if got[name] != c.want[name] {
					t.Errorf("%s = %q, want %q", name, got[name], c.want[name])
				}
			}
		})
	}
}

// Writing is the stored form in order, an empty section left out of the text
// and present again when the text is read back.
func TestWritePromptSectionsRoundTrips(t *testing.T) {
	in := []PromptSection{
		{Name: SectionReport, Body: " What changed. "},
		{Name: SectionPurpose, Body: "Fix one bug."},
		{Name: SectionRestrictions, Body: ""},
		{Name: SectionMethod, Body: "Reproduce it first.\n\n```\n## Scope\n```"},
	}
	text := WritePromptSections(in)
	const want = "## Purpose\n\nFix one bug.\n\n## Method\n\nReproduce it first.\n\n```\n## Scope\n```\n\n## Report\n\nWhat changed."
	if text != want {
		t.Fatalf("written = %q\nwant      %q", text, want)
	}
	back := sectionBodies(ReadPromptSections(text))
	if back[SectionPurpose] != "Fix one bug." || back[SectionMethod] != "Reproduce it first.\n\n```\n## Scope\n```" ||
		back[SectionReport] != "What changed." || back[SectionRestrictions] != "" || back[SectionScope] != "" {
		t.Fatalf("read back = %+v", back)
	}
	if WritePromptSections(nil) != "" {
		t.Fatal("no sections wrote a prompt")
	}
}

// The shipped examples are the reference for the sectioned form: each loads,
// and each prompt fills the five.
func TestShippedExampleProfilesAreSectioned(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	prompted := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		def, err := LoadAgentFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if strings.TrimSpace(def.Prompt) == "" {
			continue
		}
		prompted++
		for _, s := range def.Sections() {
			if s.Body == "" {
				t.Errorf("%s: %s is empty", e.Name(), s.Name)
			}
		}
	}
	if prompted == 0 {
		t.Fatal("no shipped example carries a prompt")
	}
}

// A line of inline code that starts with three backticks is not a fence, so
// the headings after it are still headings.
func TestInlineTripleBacktickLineIsNotAFence(t *testing.T) {
	got := sectionBodies(ReadPromptSections("## Purpose\n```x``` is inline code.\n\n## Scope\nsrc only."))
	if got[SectionScope] != "src only." {
		t.Fatalf("scope = %q", got[SectionScope])
	}
}
