package todo

// The vocabulary a backlog is written in.
//
// Everything under this package — one file per item, the four statuses, the
// ready rule, the sprint and the archive — is the same whatever the work is.
// The words on an item are not: what it is called, which header fields it
// carries and which of those grades the effort a run spends on it are facts
// about the project, and they were Go constants that four surfaces drew from
// separately. A Profile is those words in one place, handed to every reader
// that needs them, so that a screen, a rail, a card, an extraction schema
// and a JSON document all say the same thing without any of them holding a
// set of its own.
//
// See docs/capabilities/todo.md#an-item-is-a-file-you-can-edit.

import (
	"fmt"
	"regexp"
	"strings"
)

// Profile is one backlog's vocabulary.
type Profile struct {
	// Name is what the profile is called, which is what a refusal made in
	// its terms says it came from.
	Name string
	// Noun is what one item is called, singular — "item", "story",
	// "question", "task". Every tally and every empty state says it.
	Noun string
	// Fields are the header fields an item carries, in the order they are
	// written into a new file and drawn on a row. Priority is one of them
	// on every profile: see PriorityField.
	Fields []Field
	// Grade names the field a run reads to decide how much ceremony an
	// item gets, or "" for a profile that does not grade its work. It is
	// the profile's own because what a run spends on is the project's to
	// define; the order the ready list is in is not (PriorityField).
	Grade string
	// SlugRefuse is a pattern a new item's slug may not match, and unset on
	// every profile shipped. It is here rather than in the slug grammar
	// because a name a project reserves — an identifier its planning uses
	// elsewhere, a prefix its tooling reads — is a rule of that project and
	// not of every backlog, and a rule written into the grammar refuses it
	// in projects that have never heard of it.
	SlugRefuse string
}

// Field is one header field: the key it is written under, the words it may
// say in the order a selector steps through them, and which of them a new
// item takes when nobody chose.
type Field struct {
	Name   string
	Values []Value
	// Default is a writer's, not a reader's: the card, the drafter and
	// `todo add` put it on an item they are making, and reading a file
	// leaves a field the header does not carry unset. Priority is the one
	// exception and has to be, because the ready list is ordered by it.
	Default string
}

// Value is one word a field may say.
type Value struct {
	// Name is the word as the file spells it.
	Name string
	// Gloss is the one line that says what the word means, for the prompt
	// that asks a model to choose between them. Empty for a word that
	// explains itself.
	Gloss string
	// Glyph is the one letter a compact row draws this word as, or "" for
	// a field no row letters. A backlog row and a rail row have space for
	// the two facts that decide what comes next and what it costs — the
	// priority and the grade — and a third letter is a column nobody can
	// read at sixty columns, so a profile says which of its fields is
	// worth one.
	Glyph string
}

// priorityValues are the words priority may say, with the letter a row
// draws each as.
var priorityValues = []Value{
	{Name: string(PriorityHigh), Glyph: "H"},
	{Name: string(PriorityMedium), Glyph: "M"},
	{Name: string(PriorityLow), Glyph: "L"},
}

// PriorityField is the ordering field every profile carries, and the one
// field a profile may not restate. The ready list's order has to be
// something a person can recompute from the headers, so it is one rule for
// every backlog: a profile that could rename priority or reorder its words
// would make that order a function of the profile, and two projects' ready
// lists would disagree under what reads as one rule.
// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
func PriorityField() Field {
	return Field{Name: keyPriority, Values: priorityValues, Default: string(PriorityMedium)}
}

// BuiltinCode is the vocabulary a checkout of code has always been written
// in, as a profile: a story, a bug or a chore, graded by the effort it is
// worth. It is the only profile this release ships.
func BuiltinCode() Profile {
	return Profile{
		Name: "code",
		Noun: "item",
		Fields: []Field{
			{Name: "kind", Values: []Value{
				{Name: "story"}, {Name: "bug"}, {Name: "chore"},
			}, Default: "story"},
			PriorityField(),
			{Name: "size", Values: []Value{
				{Name: "S", Gloss: "an hour, one or two files, no design decisions", Glyph: "S"},
				{Name: "M", Gloss: "an afternoon, a few files, some judgement", Glyph: "M"},
				{Name: "L", Gloss: "days, many files, or design decisions still open", Glyph: "L"},
			}},
		},
		Grade: "size",
	}
}

// Reserved is the compiled rule, nil for a profile that reserves nothing,
// and an error for a pattern that will not compile. Whatever reads a profile
// out of a file asks before the profile is in force, because a pattern that
// does not compile reserves nothing at all: a project that typed one and got
// no refusals would believe its rule was being kept.
func (p Profile) Reserved() (*regexp.Regexp, error) {
	if p.SlugRefuse == "" {
		return nil, nil
	}
	return regexp.Compile(p.SlugRefuse)
}

// RefuseSlug reports the profile refusing a name for a new item, and nil
// where it has no rule or the name is not one it reserves. Only a slug
// being chosen is put to it — an item already on disk keeps whatever name
// it has, since a refusal that stranded a file would lose the work rather
// than rename it.
//
// A pattern that will not compile refuses nothing here, because refusing
// every name would leave a project unable to write an item at all. It cannot
// reach this far: a profile is refused at load over a pattern that will not
// compile, so one in force always has one that does.
func (p Profile) RefuseSlug(slug string) error {
	re, err := p.Reserved()
	if err != nil || re == nil || !re.MatchString(slug) {
		return nil
	}
	return fmt.Errorf("slug %q is a name the %s profile reserves; name the work instead", slug, p.Name)
}

// Field returns the field with the name.
func (p Profile) Field(name string) (Field, bool) {
	for _, f := range p.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// GradeField is the field a run spends against, and false for a profile
// that does not grade.
func (p Profile) GradeField() (Field, bool) {
	if p.Grade == "" {
		return Field{}, false
	}
	return p.Field(p.Grade)
}

// GradeRank is where a grade sits on its field's scale, counting from one,
// and zero for a word the profile does not rank — which is what an ungraded
// item and a misspelt one both are. A run reads the rank rather than the
// word so that a profile graded `quick · deep` spends what a profile graded
// `S · M · L` spends, without the runner knowing either word.
func (p Profile) GradeRank(value string) int {
	f, ok := p.GradeField()
	if !ok {
		return 0
	}
	return f.Rank(value)
}

// Grades is how many grades the scale has, and zero for a profile that does
// not grade. It is what "the largest" means to a runner.
func (p Profile) Grades() int {
	f, ok := p.GradeField()
	if !ok {
		return 0
	}
	return len(f.Values)
}

// Rank is a value's position on the field's scale, counting from one, and
// zero for a word the field does not hold.
func (f Field) Rank(value string) int {
	for i, v := range f.Values {
		if v.Name == value {
			return i + 1
		}
	}
	return 0
}

// Orders reports the field being the one the ready list is ordered by. It
// is the one field every surface treats apart from the rest: it has its own
// filter key, its own letter on a row, and its default is the only one a
// read applies.
func (f Field) Orders() bool { return f.Name == keyPriority }

// Canonical is the field's own spelling of a word, and false where the
// field does not hold it. The match is case-insensitive because a header is
// typed by hand: `size: m` says M, and a file is not wrong for being lower
// case.
func (f Field) Canonical(value string) (string, bool) {
	for _, v := range f.Values {
		if strings.EqualFold(v.Name, value) {
			return v.Name, true
		}
	}
	return "", false
}

// Glyph is the one letter a compact row draws the value as, or "" where
// there is none — a field no row letters, or a word off the scale.
func (f Field) Glyph(value string) string {
	for _, v := range f.Values {
		if v.Name == value {
			return v.Glyph
		}
	}
	return ""
}

// Words are the field's values in display order, which is what a filter
// cycles and a selector steps through.
func (f Field) Words() []string {
	out := make([]string, 0, len(f.Values))
	for _, v := range f.Values {
		out = append(out, v.Name)
	}
	return out
}

// List is the values as a listing reads them: `story, bug, chore`. It is
// what a warning names the scale with.
func (f Field) List() string { return strings.Join(f.Words(), ", ") }

// Sentence is the values as a prompt states them: `high, medium or low`, or
// each word followed by its gloss where it has one. It is built from the
// values so that a word added to a field is a word the prompt offers
// without anybody remembering to add it.
func (f Field) Sentence() string {
	parts := make([]string, 0, len(f.Values))
	for _, v := range f.Values {
		if v.Gloss == "" {
			parts = append(parts, v.Name)
			continue
		}
		parts = append(parts, v.Name+" ("+v.Gloss+")")
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}
