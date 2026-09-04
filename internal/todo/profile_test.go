package todo

// The profile as the seam it is: the built-in vocabulary reads and writes
// what the package always read and wrote, and a second profile — a reading
// list, graded by depth — goes through the same doors and comes out saying
// its own words.

import (
	"strings"
	"testing"
)

// research is a profile nothing ships: questions and readings, graded by
// how deep the answer has to go. It exists to hold the seam honest — a
// package that only ever sees one vocabulary has not been given one.
func research() Profile {
	return Profile{
		Name: "research",
		Noun: "question",
		Fields: []Field{
			{Name: "kind", Values: []Value{
				{Name: "question", Glyph: "Q"}, {Name: "reading", Glyph: "R"},
			}, Default: "question"},
			PriorityField(),
			{Name: "depth", Values: []Value{
				{Name: "quick", Gloss: "an afternoon in what is already to hand", Glyph: "Q"},
				{Name: "deep", Gloss: "a week, and sources nobody has read yet", Glyph: "D"},
			}},
		},
		Grade: "depth",
	}
}

// The built-in profile carries the words the four surfaces used to hold
// each for themselves, and ranks its grades in the order it declares them.
func TestBuiltinCode_IsTheVocabularyThatWas(t *testing.T) {
	p := BuiltinCode()
	if p.Noun != "item" || p.Grade != "size" {
		t.Fatalf("profile = %+v", p)
	}
	kind, ok := p.Field("kind")
	if !ok || strings.Join(kind.Words(), ",") != "story,bug,chore" {
		t.Errorf("kind = %+v", kind)
	}
	if got := words(t, p, "priority"); got != "high,medium,low" {
		t.Errorf("priority = %q", got)
	}
	if got := words(t, p, "size"); got != "S,M,L" {
		t.Errorf("size = %q", got)
	}
	for word, rank := range map[string]int{"S": 1, "M": 2, "L": 3, "": 0, "XL": 0} {
		if got := p.GradeRank(word); got != rank {
			t.Errorf("rank of %q = %d, want %d", word, got, rank)
		}
	}
	if p.Grades() != 3 {
		t.Errorf("grades = %d", p.Grades())
	}
}

// words is a field's values joined, for the assertions above.
func words(t *testing.T, p Profile, name string) string {
	t.Helper()
	f, ok := p.Field(name)
	if !ok {
		t.Fatalf("no %s field", name)
	}
	return strings.Join(f.Words(), ",")
}

// A profile's fields go into the file in the order it declares them, and
// come back off it under their own names.
func TestProfile_ParseAndRenderRoundTripASecondVocabulary(t *testing.T) {
	p := research()
	it := Item{
		Slug: "why-tabs", Title: "Why tabs", Priority: PriorityLow,
		Fields: map[string]string{"kind": "reading", "depth": "deep"},
	}
	rendered := Render(p, it)
	if want := "---\ntitle: Why tabs\nkind: reading\npriority: low\ndepth: deep\nstatus: open\n---\n"; rendered != want {
		t.Fatalf("rendered:\n%q\nwant:\n%q", rendered, want)
	}
	back, err := Parse(p, "/r/why-tabs.md", rendered)
	if err != nil {
		t.Fatal(err)
	}
	if back.Fields["kind"] != "reading" || back.Grade() != "deep" || back.Priority != PriorityLow {
		t.Errorf("round trip = %+v", back)
	}
	if len(back.Warnings) != 0 {
		t.Errorf("warnings = %v", back.Warnings)
	}
}

// A value off a field's scale is a warning naming the field and its own
// words, and what it costs the item depends on what the field is for: the
// grade is dropped because a run spends against it, priority falls back
// because the list is ordered by it, and anything else is kept as written.
func TestProfile_ValuesOffTheScale(t *testing.T) {
	p := research()
	it, err := Parse(p, "/r/why-tabs.md",
		"---\ntitle: Why tabs\nkind: essay\npriority: urgent\ndepth: shallow\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`unknown kind "essay" (question, reading)`,
		`unknown priority "urgent", ordered as medium (high, medium, low)`,
		`unknown depth "shallow", treated as ungraded (quick, deep)`,
	}
	if strings.Join(it.Warnings, "|") != strings.Join(want, "|") {
		t.Fatalf("warnings = %v", it.Warnings)
	}
	if it.Fields["kind"] != "essay" {
		t.Errorf("a field nothing computes from should keep what the file said: %q", it.Fields["kind"])
	}
	if it.Priority != PriorityMedium {
		t.Errorf("priority = %q", it.Priority)
	}
	if it.Grade() != "" {
		t.Errorf("grade = %q, want ungraded", it.Grade())
	}
}

// A field's words are read in whatever case the file typed them, and the
// item carries the profile's own spelling from then on.
func TestProfile_ValuesAreReadCaseInsensitively(t *testing.T) {
	it, err := Parse(BuiltinCode(), "/x/x.md", "---\ntitle: x\nkind: STORY\nsize: m\n---\n")
	if err != nil {
		t.Fatal(err)
	}
	if it.Fields["kind"] != "story" || it.Grade() != "M" || len(it.Warnings) != 0 {
		t.Fatalf("item = %+v", it)
	}
}

// The schema and the prompt a reading is asked with are the profile's
// fields, so a backlog of questions graded by depth is asked for in its own
// words and this package holds none of them.
func TestProfile_ExtractionSpeaksTheProfile(t *testing.T) {
	schema := string(extractSchema(research()))
	for _, want := range []string{
		`"kind": {"type": "string", "enum": ["question", "reading"]}`,
		`"depth": {"type": "string", "enum": ["quick", "deep"]}`,
		`"required": ["title", "kind", "priority", "depth", "story",`,
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("the schema lacks %q:\n%s", want, schema)
		}
	}
	if strings.Contains(schema, "size") {
		t.Errorf("the schema still names a field the profile does not declare:\n%s", schema)
	}
	prompt := extractPrompt(research())
	for _, want := range []string{
		"- kind: question or reading.\n",
		"- priority: high, medium or low, from what the conversation implied.\n",
		"- depth: quick (an afternoon in what is already to hand) or deep (a week, and sources nobody has read yet).\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// The words a proposal answered with land on the item under their own
// names, and a field the reading left empty takes the profile's default.
func TestProfile_ProposalsCarryTheProfilesFields(t *testing.T) {
	p := research()
	ps, ok := ParseProposals(p, `{"items": [{"title": "Why tabs", "kind": "Reading", "depth": "deep"}]}`)
	if !ok {
		t.Fatal("nothing parsed")
	}
	it := ps[0].Item(p, "why-tabs", "2026-09-05", "sess")
	if it.Fields["kind"] != "reading" || it.Grade() != "deep" || it.Priority != PriorityMedium {
		t.Fatalf("item = %+v", it)
	}
}

// The flag a set's budget is asked for through is the profile's grading
// field, so a backlog of readings is bounded in depths and one that grades
// nothing is offered no budget at all.
func TestProfile_BudgetFlagIsTheGradingField(t *testing.T) {
	name, shape, ok := BudgetFlag(BuiltinCode())
	if !ok || name != "size" || shape != "S=n,M=n,L=n" {
		t.Errorf("code = %q %q %v", name, shape, ok)
	}
	name, shape, ok = BudgetFlag(research())
	if !ok || name != "depth" || shape != "quick=n,deep=n" {
		t.Errorf("research = %q %q %v", name, shape, ok)
	}
	ungraded := Profile{Noun: "note", Fields: []Field{PriorityField()}}
	if _, _, ok := BudgetFlag(ungraded); ok {
		t.Error("a profile that grades nothing has no budget to state")
	}
	if _, err := ParseSprintBudget(ungraded, "S=1"); err == nil {
		t.Error("a budget spec for an ungraded profile should be refused")
	}
}
