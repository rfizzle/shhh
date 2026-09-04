package run

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/todo"
)

// writeProfile puts a profile directory in a temporary tree: the table, and
// one wording per file named.
func writeProfile(t *testing.T, table string, wordings map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProfileFile), []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(wordings) == 0 {
		return dir
	}
	prompts := filepath.Join(dir, ProfileWordings)
	if err := os.MkdirAll(prompts, 0o700); err != nil {
		t.Fatal(err)
	}
	for key, text := range wordings {
		if err := os.WriteFile(filepath.Join(prompts, key+".md"), []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// oneStep is the shortest profile that loads: a field, priority, one turn and
// a finish. Tests that are about one rule start from it.
const oneStep = `
name = "notes"
noun = "note"

[[field]]
name = "kind"
values = [{ name = "note" }]

[[field]]
name = "priority"

[[step]]
name = "write"
kind = "turn"
mode = "write"
standards = true

[[step]]
name = "file"
kind = "finish"
finish = "archive"
`

func oneStepWordings() map[string]string {
	return map[string]string{WordingStandards: "STANDARDS.", "write": "WRITE."}
}

// Every profile shipped in the binary is read through the loader a person's
// directory goes through, so the grammar is exercised by everything the
// product ships rather than by the tests alone.
func TestBuiltinProfiles_EachLoadsAndValidates(t *testing.T) {
	want := map[string]struct {
		noun  string
		grade string
		strip int
	}{
		"code":      {"item", "size", 5},
		"research":  {"question", "depth", 4},
		"ops":       {"task", "", 4},
		"notes":     {"note", "", 2},
		"checklist": {"item", "", 0},
	}
	if len(BuiltinProfiles()) != len(want) {
		t.Fatalf("the profiles shipped are %v", BuiltinProfiles())
	}
	for _, name := range BuiltinProfiles() {
		words, pipeline, err := BuiltinProfile(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		w := want[name]
		if words.Name != name || words.Noun != w.noun || words.Grade != w.grade {
			t.Errorf("%s: noun %q grade %q", name, words.Noun, words.Grade)
		}
		if got := len(pipeline.Strip()); got != w.strip {
			t.Errorf("%s: %d steps on the strip, want %d", name, got, w.strip)
		}
		// Every wording the steps name is shipped beside the table: a
		// profile whose file is missing does not load at all, so reaching
		// here means every key has one.
		for _, key := range pipeline.WordingKeys() {
			if strings.TrimSpace(pipeline.Builtins()[key]) == "" {
				t.Errorf("%s: the %s wording is empty", name, key)
			}
		}
	}
}

// The profile a checkout of code has always run is shipped as files like any
// other, and those files are the pipeline this package holds in Go. Two
// descriptions of one run would drift, and only the Go one is under test
// everywhere else.
func TestBuiltinProfile_CodeIsWhatTheGoValuesSay(t *testing.T) {
	words, pipeline, err := BuiltinProfile("code")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(words, todo.BuiltinCode()) {
		t.Errorf("the code profile's vocabulary is not the one in Go:\n%+v\n%+v", words, todo.BuiltinCode())
	}
	if !reflect.DeepEqual(pipeline, BuiltinCode()) {
		t.Errorf("the code profile's run is not the one in Go:\n%+v\n%+v", pipeline, BuiltinCode())
	}
}

// The two readings a backlog takes of itself are the profile's, and a
// profile that ships neither has neither verb. Where one is shipped it is
// what the reading is told to check, and the answer's shape is never the
// file's.
func TestBuiltinProfiles_TheReadingsAreTheProfilesOwn(t *testing.T) {
	want := map[string]struct {
		grooms, plans bool
		stale         todo.Staleness
		releases      int
	}{
		"code":      {true, true, todo.Staleness{Measure: todo.MeasureCommits, Threshold: 50}, 2},
		"research":  {true, true, todo.Staleness{Measure: todo.MeasureDays, Threshold: 90}, 0},
		"ops":       {true, true, todo.Staleness{Measure: todo.MeasureDays, Threshold: 14}, 0},
		"notes":     {true, false, todo.Staleness{}, 0},
		"checklist": {false, false, todo.Staleness{}, 0},
	}
	for _, name := range BuiltinProfiles() {
		words, _, err := BuiltinProfile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		w := want[name]
		if words.Grooms() != w.grooms || words.Plans() != w.plans {
			t.Errorf("%s: grooms %v plans %v", name, words.Grooms(), words.Plans())
		}
		if words.Stale != w.stale {
			t.Errorf("%s: stale = %+v", name, words.Stale)
		}
		if len(words.Releases) != w.releases {
			t.Errorf("%s: %d release words", name, len(words.Releases))
		}
		// A wording the profile ships places the item where it wants it, the
		// way every step wording does; the shape comes after it either way.
		if w.grooms && !strings.Contains(words.Groom, PlaceholderItem) {
			t.Errorf("%s: the grooming wording places no item block", name)
		}
	}
}

// A profile with no run is a profile, not an unfinished file: it names no
// wording, so there is nothing to scaffold and nothing to replace.
func TestLoadProfile_AProfileMayStateNoRun(t *testing.T) {
	_, pipeline, err := BuiltinProfile("checklist")
	if err != nil {
		t.Fatal(err)
	}
	if pipeline.Runs() || len(pipeline.WordingKeys()) != 0 || len(pipeline.Builtins()) != 0 {
		t.Fatalf("a profile with no run named %d wordings", len(pipeline.WordingKeys()))
	}
	if !pipeline.Stated() {
		t.Fatal("a run of no steps somebody wrote down reads as one nobody stated")
	}
}

// A profile shipped for work that is not code works one item from end to end
// with no repository, no supervisor and no commit: what the runner asks of a
// session is what that profile's steps ask for and nothing else.
func TestBuiltinProfile_AReadingIsWorkedWithoutARepository(t *testing.T) {
	words, p, err := BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	it := todo.Item{Slug: "why-tabs", Title: "Why tabs", Profile: words,
		Fields: map[string]string{"kind": "reading", "depth": "quick"},
		Body:   "## Acceptance Criteria\n- [ ] say what the sources say\n"}

	// Every step of it reads, so it asks a session for nothing: no record of
	// what it changed, because it changes nothing; no command runner, no
	// supervisor and no repository, because no step of this run wants one.
	if ref, refused := p.Refuse(Can{}); refused {
		t.Fatalf("a run whose steps only read asked a session for something: %+v", ref)
	}

	s := Start(it, "sess", "manual", 1, Options{Pipeline: p, Notebook: true})
	step := s.First(it, "")
	if step.Stage != "scope" || !strings.Contains(step.Prompt, "depth: quick|deep") {
		t.Fatalf("the first step asks for %+v", step)
	}
	step = s.Observe(it, "## Plan:\n1. read it\nfiles: a.md\naction: read\n\ndepth: quick\n\nquestions: none")
	if step.Stage != "gather" {
		t.Fatalf("after the scope: %+v", step)
	}
	if step = s.Observe(it, "gathered it"); step.Stage != "review" {
		t.Fatalf("after the gathering: %+v", step)
	}
	// The gathering is the answer, so it is what the reader is handed —
	// there is no change to point one at.
	if task := s.ReviewTask(it, ""); !strings.Contains(task, "gathered it") {
		t.Fatalf("the reader was not given the gathering:\n%s", task)
	}
	if step = s.Observe(it, "verdict: clean"); step.Stage != "file" {
		t.Fatalf("after the reading: %+v", step)
	}
	step = s.Observe(it, "REPORT:\n## Report\nSummary: the sources agree\n")
	if step.Action != ActionDone || !s.Over() || !strings.Contains(s.Report, "the sources agree") {
		t.Fatalf("the run did not end in the write-up: %+v %q", step, s.Report)
	}
}

// Every refusal names the file, and the line wherever the mistake is on one:
// a profile is a table of words with no shape to it, so a rule without a line
// is a search rather than an edit.
func TestLoadProfile_RefusesWhatTheRunnerCannotCarryOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
		says  string
	}{{
		name:  "a key no profile has",
		table: oneStep + "\nmotto = \"go faster\"\n",
		says:  "not a key a profile has",
	}, {
		name:  "a step kind the runner has none of",
		table: strings.Replace(oneStep, `kind = "turn"`, `kind = "incantation"`, 1),
		says:  "no kind this runner has",
	}, {
		name:  "a block the run does not carry",
		table: oneStep + "\n",
		says:  "the blocks a run has are",
	}, {
		name:  "priority left out",
		table: strings.Replace(oneStep, "[[field]]\nname = \"priority\"\n", "", 1),
		says:  "does not place priority",
	}, {
		name:  "priority restated",
		table: strings.Replace(oneStep, "name = \"priority\"", "name = \"priority\"\nvalues = [{ name = \"urgent\" }]", 1),
		says:  "restates priority",
	}, {
		name:  "a grade that is not a field",
		table: strings.Replace(oneStep, `noun = "note"`, "noun = \"note\"\ngrade = \"depth\"", 1),
		says:  "which is not one of its fields",
	}, {
		name:  "a default off the field's own scale",
		table: strings.Replace(oneStep, `values = [{ name = "note" }]`, "default = \"idea\"\nvalues = [{ name = \"note\" }]", 1),
		says:  "which is not one of",
	}, {
		name:  "a pattern that would reserve nothing",
		table: strings.Replace(oneStep, `noun = "note"`, "noun = \"note\"\nslug_refuse = \"[\"", 1),
		says:  "will not compile",
	}, {
		name:  "a step that takes a reading's key",
		table: strings.Replace(oneStep, `name = "write"`, `name = "groom"`, 1),
		says:  "which is where this profile says how its backlog is read",
	}, {
		// A step is instructed from the key it names where it names one, so
		// the collision is with the key and not with the step's own word.
		name:  "a step instructed from a reading's file under another name",
		table: strings.Replace(oneStep, `name = "write"`, "name = \"write\"\nwording = \"plan\"", 1),
		says:  "which is where this profile says how its backlog is read",
	}, {
		name:  "a staleness measured in nothing the runner counts",
		table: oneStep + "\n[stale]\nmeasure = \"moons\"\nthreshold = 3\n",
		says:  "a reading falls behind in commits or days",
	}, {
		name:  "a staleness with no threshold",
		table: oneStep + "\n[stale]\nmeasure = \"days\"\n",
		says:  "every reading it ever takes is already stale",
	}, {
		name:  "a release word with nothing saying what it means",
		table: oneStep + "\n[[release]]\nname = \"patch\"\n",
		says:  "with nothing saying what it means",
	}, {
		name:  "a run with no finish",
		table: strings.Replace(oneStep, "\n[[step]]\nname = \"file\"\nkind = \"finish\"\nfinish = \"archive\"\n", "", 1),
		says:  "no finish step",
	}, {
		name:  "a persona on a step with nobody to be one",
		table: strings.Replace(oneStep, `standards = true`, "standards = true\npersona = \"editor\"", 1),
		says:  "there is nobody for the persona to be",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			table := tc.table
			if tc.name == "a block the run does not carry" {
				table = strings.Replace(oneStep, `standards = true`, "standards = true\nblocks = [\"weather\"]", 1)
			}
			dir := writeProfile(t, table, oneStepWordings())
			_, _, err := LoadProfile(dir)
			if err == nil {
				t.Fatalf("the profile loaded")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("the refusal does not say the rule: %v", err)
			}
			if !strings.Contains(err.Error(), ProfileFile) {
				t.Fatalf("the refusal does not name the file: %v", err)
			}
		})
	}
}

// The line is what makes a refusal an edit rather than a search: a profile
// with eleven steps and one bad word in it is a file somebody has to find the
// word in.
func TestLoadProfile_ARefusalNamesTheLineTheMistakeIsOn(t *testing.T) {
	table := strings.Replace(oneStep, `kind = "turn"`, `kind = "incantation"`, 1)
	dir := writeProfile(t, table, oneStepWordings())
	_, _, err := LoadProfile(dir)
	if err == nil {
		t.Fatal("the profile loaded")
	}
	// The step is named on the line the refusal points at, which is the one
	// somebody opens the file to.
	at := 1 + strings.Count(table[:strings.Index(table, `name = "write"`)], "\n")
	want := fmt.Sprintf("%s:%d:", filepath.Join(dir, ProfileFile), at)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal does not name the line the step is on (%s): %v", want, err)
	}
}

// A rule stated by grade is written smallest grade first, and the entry that
// always stops names the grade it sits at — a bare word in a list of three
// is a rule one place out from where its author meant it, and nothing could
// tell that from a slip.
func TestLoadProfile_AlwaysNamesTheGradeItPausesFrom(t *testing.T) {
	// The gate sits before the step that writes, which is the only place a
	// gate may be: it decides what gets built.
	graded := func(rule string) string {
		return `
name = "notes"
noun = "note"
grade = "depth"

[[field]]
name = "depth"
values = [{ name = "quick" }, { name = "deep" }]

[[field]]
name = "priority"

[[step]]
name = "read"
kind = "turn"
mode = "read"

[[step]]
name = "pause"
kind = "gate"
under = "read"
pause = ["never", ` + rule + `]

[[step]]
name = "write"
kind = "turn"
mode = "write"
standards = true

[[step]]
name = "file"
kind = "finish"
finish = "archive"
`
	}
	wordings := map[string]string{WordingStandards: "STANDARDS.", "read": "READ.", "write": "WRITE."}

	dir := writeProfile(t, graded(`"always"`), wordings)
	if _, _, err := LoadProfile(dir); err == nil || !strings.Contains(err.Error(), "without saying from which grade") {
		t.Fatalf("a bare always was taken as read: %v", err)
	}

	dir = writeProfile(t, graded(`"always-when shallow"`), wordings)
	if _, _, err := LoadProfile(dir); err == nil || !strings.Contains(err.Error(), "not a grade this profile has") {
		t.Fatalf("a grade the profile does not have was taken as read: %v", err)
	}

	dir = writeProfile(t, graded(`"always-when quick"`), wordings)
	if _, _, err := LoadProfile(dir); err == nil || !strings.Contains(err.Error(), "at the place of deep") {
		t.Fatalf("a rule one place out from the grade it names was taken as read: %v", err)
	}

	// And a list that is not one entry per grade: a shorter one is stretched
	// over the scale, so the entry that always stops fires a grade early.
	short := strings.Replace(graded(`"always-when deep"`), `pause = ["never", "always-when deep"]`, `pause = ["always-when deep"]`, 1)
	if _, _, err := LoadProfile(writeProfile(t, short, wordings)); err == nil ||
		!strings.Contains(err.Error(), "one entry per grade") {
		t.Fatalf("a rule stated once over a scale of two was taken as read: %v", err)
	}

	dir = writeProfile(t, graded(`"always-when deep"`), wordings)
	_, p, err := LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	step, _ := p.Step("pause")
	if !reflect.DeepEqual(step.Pause, []PauseRule{PauseNever, PauseAlways}) {
		t.Fatalf("the gate's rules are %v", step.Pause)
	}
}

// A header is typed by hand and matched case-insensitively, so a field takes
// a default in whatever case the file wrote it — and keeps it in its own
// spelling, because a new item must not be written with a word the field's
// own list does not hold.
func TestLoadProfile_ADefaultIsKeptInTheFieldsOwnSpelling(t *testing.T) {
	table := strings.Replace(oneStep, `values = [{ name = "note" }]`,
		"default = \"NOTE\"\nvalues = [{ name = \"note\" }]", 1)
	words, _, err := LoadProfile(writeProfile(t, table, oneStepWordings()))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := words.Field("kind")
	if f.Default != "note" {
		t.Fatalf("a new item would be written with %q", f.Default)
	}
}

// A step whose wording is not in the directory stops the load rather than
// falling back to shhh's own words: a run that sent the code profile's
// implement wording to a reading step would be telling the model to build
// something, and the person who wrote the directory is the one who can see
// the file is missing.
func TestLoadProfile_AWordingTheProfileNamesMustBeThere(t *testing.T) {
	dir := writeProfile(t, oneStep, map[string]string{WordingStandards: "STANDARDS."})
	_, _, err := LoadProfile(dir)
	if err == nil || !strings.Contains(err.Error(), "is not there") {
		t.Fatalf("a profile loaded with a wording missing: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(ProfileWordings, "write.md")) {
		t.Fatalf("the refusal does not name the file to write: %v", err)
	}
}

// The wordings a directory ships are the instructions the run sends, and the
// file's last newline is the editor's rather than the wording's.
func TestLoadProfile_TheDirectorysWordsAreWhatTheRunSends(t *testing.T) {
	dir := writeProfile(t, oneStep, oneStepWordings())
	_, p, err := LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Standards != "STANDARDS." {
		t.Fatalf("the shared sentence is %q", p.Standards)
	}
	step, _ := p.Step("write")
	if step.Builtin != "WRITE." {
		t.Fatalf("the step's instruction is %q", step.Builtin)
	}
	prompt := p.prompt(promptArgs{step: step, item: note()}, nil, todo.BuiltinCode())
	if !strings.Contains(prompt, "WRITE.") || !strings.Contains(prompt, "STANDARDS.") {
		t.Fatalf("the run sends words the profile did not write:\n%s", prompt)
	}
}

// A step that hands work to a colleague may name one. The name is read off
// the file like every other word about a step, because a profile whose
// reader could only be named in Go would be a profile a project cannot
// write.
func TestLoadProfile_AnAgentStepNamesItsReader(t *testing.T) {
	table := oneStep + `
[[step]]
name = "read"
kind = "agent"
mode = "read"
persona = "editor"
`
	dir := writeProfile(t, table, map[string]string{
		WordingStandards: "STANDARDS.", "write": "WRITE.", "read": "READ.", "read_task": "READ IT.",
	})
	_, pipeline, err := LoadProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := pipeline.Step("read")
	if !ok {
		t.Fatal("the reading step did not load")
	}
	if step.Persona != "editor" {
		t.Fatalf("the reading step names %q as its reader", step.Persona)
	}
}

// The reader of a shipped profile's gathering is named in its file, so a
// conversation with a colleague of that name sends the work to them and every
// other session falls back to the role.
func TestBuiltinProfile_TheReadingStepNamesItsReader(t *testing.T) {
	_, p, err := BuiltinProfile("research")
	if err != nil {
		t.Fatal(err)
	}
	step, ok := p.Step("review")
	if !ok {
		t.Fatal("the research profile has no reading step")
	}
	if step.Persona == "" {
		t.Fatal("the reading step names no reader, so a conversation cannot send it to one")
	}
}
