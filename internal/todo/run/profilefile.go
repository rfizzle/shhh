package run

// A profile as a directory: what the work is called, which run works it, and
// the words each step is instructed with.
//
// The vocabulary and the pipeline are Go values, and a project that had to
// write Go to change them would be forking the tool rather than configuring
// it. So they are also a file: `profile.toml` beside a `prompts/` directory
// of one wording per step. The five profiles shipped are read through exactly
// this loader from files embedded in the binary, which is the only way to
// know the grammar can say what the built-ins say — a shipped profile written
// in Go and a person's written in TOML would be two grammars, and only one of
// them would be exercised.
//
// What a file may say is bounded by what the code can carry out. The step
// kinds, the finishes, the pause rules and the substitutions are closed sets
// (step.go), a value off one of them is refused here with the line it is on,
// and the pipeline is put to the validator before anything is built on it: a
// run that reached a step nothing can do would have spent every turn before
// it.
// See docs/capabilities/todo.md#a-profile-says-what-the-work-is.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/todo"
)

// ProfileFile is what a profile directory holds: the table, and the wordings
// under it.
const (
	ProfileFile = "profile.toml"
	// ProfileWordings is the directory inside a profile that holds one file
	// per wording key, named the way the settings name it.
	ProfileWordings = "prompts"
	// WordingGroom and WordingPlan are the two readings a backlog takes of
	// itself rather than of one run: one item against what it claims, and
	// the ready list against what belongs together. Both are optional — a
	// profile that ships neither has neither verb — and both are named here
	// rather than by a step, so a step may not take either key.
	WordingGroom = "groom"
	WordingPlan  = "plan"
)

// profileFile is the table as TOML states it. Every field is a string or a
// list of them: the words that mean something to the runner are turned into
// its own types below, so that a misspelt one is refused with the closed set
// it missed rather than silently read as a zero value.
type profileFile struct {
	Name       string        `toml:"name"`
	Noun       string        `toml:"noun"`
	Grade      string        `toml:"grade"`
	SlugRefuse string        `toml:"slug_refuse"`
	Stale      staleFile     `toml:"stale"`
	Field      []fieldFile   `toml:"field"`
	Release    []releaseFile `toml:"release"`
	Step       []stepFile    `toml:"step"`
}

// staleFile is how far a reading may fall behind before the surfaces say so:
// the distance it is counted in, and how much of it is too much.
type staleFile struct {
	Measure   string `toml:"measure"`
	Threshold int    `toml:"threshold"`
}

// releaseFile is one word a proposed set may say about what it releases.
type releaseFile struct {
	Name  string `toml:"name"`
	Gloss string `toml:"gloss"`
}

// fieldFile is one header field: the key it is written under and the words it
// may say, in the order a selector steps through them.
type fieldFile struct {
	Name    string      `toml:"name"`
	Default string      `toml:"default"`
	Values  []valueFile `toml:"values"`
}

type valueFile struct {
	Name  string `toml:"name"`
	Gloss string `toml:"gloss"`
	Glyph string `toml:"glyph"`
}

// stepFile is one step of the run.
type stepFile struct {
	Name      string   `toml:"name"`
	Kind      string   `toml:"kind"`
	Mode      string   `toml:"mode"`
	Wording   string   `toml:"wording"`
	Tail      string   `toml:"tail"`
	Blocks    []string `toml:"blocks"`
	Reads     []string `toml:"reads"`
	Standards bool     `toml:"standards"`
	Pause     []string `toml:"pause"`
	Rounds    []int    `toml:"rounds"`
	Back      string   `toml:"back"`
	Under     string   `toml:"under"`
	When      string   `toml:"when"`
	Solo      string   `toml:"solo"`
	Persona   string   `toml:"persona"`
	Finish    string   `toml:"finish"`
	Command   string   `toml:"command"`
}

// LoadProfile reads the profile directory at dir. It answers with the two
// halves of one profile — the vocabulary an item is written in and the run
// it is worked by — because a project states both in one directory and a
// caller that could load one without the other would be able to work a
// backlog of readings through a run built for code.
func LoadProfile(dir string) (todo.Profile, Pipeline, error) {
	return readProfile(os.DirFS(dir), func(rel string) string {
		return filepath.Join(dir, filepath.FromSlash(rel))
	})
}

// readProfile is that read over any tree, which is what lets the built-ins
// come out of the binary through the same loader a person's directory does.
// show names one of its files the way a message about it should read, which
// is a path for a directory on disk and a description for one that is not.
func readProfile(fsys fs.FS, show func(rel string) string) (todo.Profile, Pipeline, error) {
	shown := show(ProfileFile)
	data, err := fs.ReadFile(fsys, ProfileFile)
	if err != nil {
		// The path the reader is shown is the one they would open, not the
		// one inside whatever tree this was read through, so the wrapped
		// error's own path is dropped and its reason kept.
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return todo.Profile{}, Pipeline{}, fmt.Errorf("%s: %w", shown, err)
	}
	var f profileFile
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return todo.Profile{}, Pipeline{}, fmt.Errorf("%s: %w", shown, err)
	}
	src := source{text: string(data), path: shown}
	if left := md.Undecoded(); len(left) > 0 {
		keys := make([]string, 0, len(left))
		for _, k := range left {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return todo.Profile{}, Pipeline{}, src.at(keys[0], "%s is not a key a profile has", keys[0])
	}
	words, err := f.profile(src)
	if err != nil {
		return todo.Profile{}, Pipeline{}, err
	}
	pipeline, err := f.pipeline(src, words)
	if err != nil {
		return todo.Profile{}, Pipeline{}, err
	}
	if err := readWordings(fsys, show, &pipeline); err != nil {
		return todo.Profile{}, Pipeline{}, err
	}
	if err := readReadings(fsys, show, &words); err != nil {
		return todo.Profile{}, Pipeline{}, err
	}
	return words, pipeline, nil
}

// readReadings puts the two wordings a backlog is read by on the profile.
// Both are optional, and a profile that ships neither has neither verb —
// which is the honest answer for a list nobody reads against anything, and
// better than a grooming pass that asked a reading list about `path:line`.
func readReadings(fsys fs.FS, show func(rel string) string, words *todo.Profile) error {
	for _, r := range []struct {
		key  string
		into *string
	}{{WordingGroom, &words.Groom}, {WordingPlan, &words.Plan}} {
		rel := ProfileWordings + "/" + r.key + ".md"
		text, err := fs.ReadFile(fsys, rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", show(rel), err)
		}
		*r.into = strings.TrimSuffix(string(text), "\n")
	}
	return nil
}

// source is the file a refusal names, with the text it can find a line in.
type source struct {
	text string
	path string
}

// at is one refusal, naming the file and the line the mistake is on. The line
// is worth finding because a profile is a table of words with no shape to it:
// "kind is not a step kind" over eleven steps is a search, and the same
// sentence with a line number is an edit.
func (s source) at(needle, format string, args ...any) error {
	where := s.path
	if n := lineOf(s.text, needle); n > 0 {
		where = fmt.Sprintf("%s:%d", s.path, n)
	}
	return fmt.Errorf("%s: %s", where, fmt.Sprintf(format, args...))
}

// lineOf is the 1-based line the text first mentions needle on, and 0 for
// text that does not mention it at all — a value the file left out, whose
// mistake is the absence and has no line of its own.
//
// A comment is passed over. A profile carries prose about what its steps are
// for, and the words a refusal searches for are exactly the words that prose
// uses, so the first mention is otherwise a paragraph explaining the thing
// rather than the line stating it.
func lineOf(text, needle string) int {
	if needle == "" {
		return 0
	}
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// profile is the vocabulary half of the table.
func (f profileFile) profile(src source) (todo.Profile, error) {
	p := todo.Profile{Name: f.Name, Noun: f.Noun, Grade: f.Grade, SlugRefuse: f.SlugRefuse}
	if p.Name == "" {
		return p, src.at("", "a profile has no name")
	}
	if p.Noun == "" {
		return p, src.at("noun", "profile %q does not say what one item is called", p.Name)
	}
	orders := false
	seen := map[string]bool{}
	for _, ff := range f.Field {
		field, err := ff.field(src, p.Name)
		if err != nil {
			return todo.Profile{}, err
		}
		if seen[field.Name] {
			return todo.Profile{}, src.at(field.Name, "profile %q declares the field %q twice", p.Name, field.Name)
		}
		seen[field.Name] = true
		orders = orders || field.Orders()
		p.Fields = append(p.Fields, field)
	}
	// Priority is every profile's and says the same three words in every one,
	// so the file places it rather than states it. A profile that left it out
	// would be ordered by a field nobody wrote down, which is the one thing
	// about a backlog a person has to be able to recompute by reading the
	// headers.
	// See docs/capabilities/todo.md#ready-means-the-dependencies-are-done.
	if !orders {
		return todo.Profile{}, src.at("[[field]]",
			"profile %q does not place priority; every profile carries it, so name it in the field list where its column belongs", p.Name)
	}
	if p.Grade != "" {
		if _, ok := p.Field(p.Grade); !ok {
			return todo.Profile{}, src.at("grade", "profile %q grades on %q, which is not one of its fields", p.Name, p.Grade)
		}
	}
	if _, err := p.Reserved(); err != nil {
		return todo.Profile{}, src.at("slug_refuse",
			"profile %q reserves slugs matching a pattern that will not compile, so it would reserve none: %v", p.Name, err)
	}
	stale, err := f.Stale.staleness(src, p.Name)
	if err != nil {
		return todo.Profile{}, err
	}
	p.Stale = stale
	for _, rf := range f.Release {
		switch {
		case rf.Name == "":
			return todo.Profile{}, src.at("[[release]]", "profile %q has a release word with no name", p.Name)
		case rf.Gloss == "":
			return todo.Profile{}, src.at(rf.Name,
				"profile %q offers the release word %q with nothing saying what it means, and the reading has to choose between them", p.Name, rf.Name)
		}
		p.Releases = append(p.Releases, todo.ReleaseWord{Name: rf.Name, Gloss: rf.Gloss})
	}
	return p, nil
}

// staleness is the distance a reading is measured by, and the zero value for
// a profile that says nothing about readings falling behind. A measure with
// no threshold is refused rather than read as zero: zero is the distance
// every reading has already fallen, so the profile would call its whole
// backlog stale the moment anything was groomed.
func (sf staleFile) staleness(src source, profile string) (todo.Staleness, error) {
	if sf.Measure == "" && sf.Threshold == 0 {
		return todo.Staleness{}, nil
	}
	m := todo.Measure(sf.Measure)
	if !m.Known() {
		return todo.Staleness{}, src.at("measure",
			"profile %q measures staleness in %q, and a reading falls behind in %s", profile, sf.Measure, measureWords())
	}
	if sf.Threshold <= 0 {
		return todo.Staleness{}, src.at("threshold",
			"profile %q counts staleness in %s and says how many at %d; below one, every reading it ever takes is already stale", profile, m, sf.Threshold)
	}
	return todo.Staleness{Measure: m, Threshold: sf.Threshold}, nil
}

// measureWords is the closed set on one line, for the refusal.
func measureWords() string {
	out := make([]string, 0, len(todo.Measures()))
	for _, m := range todo.Measures() {
		out = append(out, string(m))
	}
	return strings.Join(out, " or ")
}

// field is one header field, with priority read as the placement it is.
func (ff fieldFile) field(src source, profile string) (todo.Field, error) {
	if ff.Name == "" {
		return todo.Field{}, src.at("[[field]]", "profile %q has a field with no name", profile)
	}
	if todo.PriorityField().Name == ff.Name {
		if len(ff.Values) > 0 || ff.Default != "" {
			return todo.Field{}, src.at(ff.Name,
				"profile %q restates priority; it says the same words in every profile, so the file only says where its column goes", profile)
		}
		return todo.PriorityField(), nil
	}
	if len(ff.Values) == 0 {
		return todo.Field{}, src.at(ff.Name, "profile %q declares the field %q with no values, so nothing could ever be written in it", profile, ff.Name)
	}
	field := todo.Field{Name: ff.Name}
	for _, v := range ff.Values {
		if v.Name == "" {
			return todo.Field{}, src.at(ff.Name, "profile %q gives the field %q a value with no name", profile, ff.Name)
		}
		field.Values = append(field.Values, todo.Value{Name: v.Name, Gloss: v.Gloss, Glyph: v.Glyph})
	}
	if ff.Default != "" {
		// Kept in the field's own spelling rather than the file's: a header
		// is matched case-insensitively, so `default = "s"` is the S grade,
		// and writing it into a new item as the file typed it would put a
		// word on disk that the field's own list does not hold.
		canon, ok := field.Canonical(ff.Default)
		if !ok {
			return todo.Field{}, src.at(ff.Default,
				"profile %q writes %q into a new item's %s, which is not one of %s", profile, ff.Default, ff.Name, field.List())
		}
		field.Default = canon
	}
	return field, nil
}

// pipeline is the run half of the table, validated as a whole once every step
// has been read.
func (f profileFile) pipeline(src source, words todo.Profile) (Pipeline, error) {
	p := Pipeline{Name: words.Name}
	if len(f.Step) == 0 {
		// A profile may have no run at all — a checklist is a list of things
		// to do and not a thing to work — and an empty pipeline is that
		// answer rather than an unfinished file. The surfaces say so when
		// somebody asks for a run.
		return p, nil
	}
	for _, sf := range f.Step {
		step, err := sf.step(src, words)
		if err != nil {
			return Pipeline{}, err
		}
		p.Steps = append(p.Steps, step)
	}
	if err := p.Validate(); err != nil {
		return Pipeline{}, fmt.Errorf("%s: %w", src.path, err)
	}
	return p, nil
}

// step is one step, with every word put to the closed set it comes from.
func (sf stepFile) step(src source, words todo.Profile) (PipelineStep, error) {
	if sf.Name == "" {
		return PipelineStep{}, src.at("[[step]]", "profile %q has a step with no name", words.Name)
	}
	at := func(format string, args ...any) error {
		return src.at(sf.Name, format, args...)
	}
	ps := PipelineStep{
		Name: sf.Name, Kind: Kind(sf.Kind), Wording: sf.Wording, Tail: sf.Tail,
		Standards: sf.Standards, Rounds: sf.Rounds, Back: sf.Back, Under: sf.Under,
		Persona: sf.Persona, Command: sf.Command,
	}
	if !ps.Kind.Known() {
		return PipelineStep{}, at("step %q is of no kind this runner has (%s)", sf.Name, strings.Join(kindWords(), ", "))
	}
	// The two readings a backlog takes of itself are instructed from the
	// same directory the steps are, so their keys are not a step's to take.
	// A step that took one would be instructed by the reading's file and the
	// reading by nothing, and both would read as if they had their own.
	if key := ps.Key(); key == WordingGroom || key == WordingPlan {
		return PipelineStep{}, at("step %q is instructed from %s.md, which is where this profile says how its backlog is read; name the step or its wording something else",
			sf.Name, key)
	}
	// A step that says nothing about the tree does not touch it, which is
	// the answer for the kinds that send no turn at all — a command, a gate
	// — and for a turn whose file left the mode out.
	if sf.Mode != "" {
		if a := Access(sf.Mode); a != Read && a != Write {
			return PipelineStep{}, at("step %q runs in %q, and a step either reads or writes", sf.Name, sf.Mode)
		}
		ps.Access = Access(sf.Mode)
	}
	for _, name := range sf.Blocks {
		block, ok := placeholder(name)
		if !ok {
			return PipelineStep{}, at("step %q carries a block called %q, and the blocks a run has are %s",
				sf.Name, name, strings.Join(placeholderWords(), ", "))
		}
		ps.Blocks = append(ps.Blocks, block)
	}
	for _, name := range sf.Reads {
		reads, ok := readsOf(name)
		if !ok {
			return PipelineStep{}, at("step %q reads %q out of its answer, and the parts a step may read are %s",
				sf.Name, name, strings.Join(readsWords(), ", "))
		}
		ps.Reads |= reads
	}
	if err := statedByGrade(sf.Name, "pause", len(sf.Pause), words); err != nil {
		return PipelineStep{}, at("%s", err)
	}
	if err := statedByGrade(sf.Name, "rounds", len(sf.Rounds), words); err != nil {
		return PipelineStep{}, at("%s", err)
	}
	for i, rule := range sf.Pause {
		parsed, err := pauseRule(rule, i, words)
		if err != nil {
			return PipelineStep{}, at("step %q %s", sf.Name, err)
		}
		ps.Pause = append(ps.Pause, parsed)
	}
	if sf.When != "" {
		if When(sf.When) != WhenLargest {
			return PipelineStep{}, at("step %q is taken %q, and the only grade a step may be kept for is %q",
				sf.Name, sf.When, WhenLargest)
		}
		ps.When = When(sf.When)
	}
	if sf.Solo != "" {
		rank := words.GradeRank(sf.Solo)
		if rank == 0 {
			return PipelineStep{}, at("step %q reads its own work at %q, which is not a grade this profile has", sf.Name, sf.Solo)
		}
		ps.Solo = rank
	}
	if sf.Finish != "" {
		ps.Finish = Finish(sf.Finish)
		if !ps.Finish.Known() {
			return PipelineStep{}, at("step %q ends the run in a way this runner has no answer for (%q)", sf.Name, sf.Finish)
		}
	}
	return ps, nil
}

// statedByGrade refuses a rule stated by grade with the wrong number of
// entries. A profile writes its own scale and its own run together, so a list
// with one entry per grade is the only one whose entries can be read back to
// the grade its author meant — a shorter one is silently stretched over the
// scale, and the entry that always stops would fire a grade early.
func statedByGrade(step, what string, n int, words todo.Profile) error {
	if n == 0 {
		return nil
	}
	want := words.Grades()
	if want == 0 {
		// Ungraded work reads one rule for every item, so there is one entry
		// and nothing for a second to be about.
		if n != 1 {
			return fmt.Errorf("step %q states %s %d times and this profile does not grade its work, so there is one rule for every item", step, what, n)
		}
		return nil
	}
	if n != want {
		f, _ := words.GradeField()
		return fmt.Errorf("step %q states %s %d times and %s runs over %s, so there is one entry per grade, smallest first", step, what, n, words.Grade, f.List())
	}
	return nil
}

// pauseRule is one gate rule as the file writes it, at its place on the
// scale. `always` names the grade it applies from, because the rules are
// written smallest grade first and a bare word in a list of three is a rule
// one place out from where its author meant it — with the grade named, the
// file says which grade it meant and the loader can tell it apart from a
// slip.
func pauseRule(rule string, at int, words todo.Profile) (PauseRule, error) {
	if grade, ok := strings.CutPrefix(rule, string(PauseAlways)+"-when "); ok {
		grade = strings.TrimSpace(grade)
		rank := words.GradeRank(grade)
		switch {
		case rank == 0:
			return "", fmt.Errorf("pauses always from %q, which is not a grade this profile has", grade)
		case rank != at+1:
			return "", fmt.Errorf("pauses always from %q at the place of %s on the scale; the rules are written smallest grade first", grade, ordinalGrade(at, words))
		}
		return PauseAlways, nil
	}
	if PauseRule(rule) == PauseAlways && words.Grade != "" {
		return "", fmt.Errorf("pauses %q without saying from which grade; write `%s-when <grade>`", rule, PauseAlways)
	}
	if !PauseRule(rule).Known() {
		return "", fmt.Errorf("pauses on %q, which is not a rule this runner has (%s)", rule, strings.Join(pauseWords(), ", "))
	}
	return PauseRule(rule), nil
}

// ordinalGrade is the grade a rule at that place on the scale is about,
// which is what a rule written one place out is actually saying.
func ordinalGrade(at int, words todo.Profile) string {
	f, ok := words.GradeField()
	if !ok || at >= len(f.Values) {
		return "no grade this profile has"
	}
	return f.Values[at].Name
}

// placeholder is the substitution a block name stands for. The file writes
// `item` where the wording writes `{{item}}`: the braces are the wording's
// way of marking a hole in prose, and a list of block names is not prose.
func placeholder(name string) (string, bool) {
	for _, p := range placeholders() {
		if strings.Trim(p, "{}") == name {
			return p, true
		}
	}
	return "", false
}

// placeholders is the closed set, in the order prompt.go declares it.
func placeholders() []string {
	return []string{PlaceholderItem, PlaceholderPlan, PlaceholderAnswers, PlaceholderFindings, PlaceholderDiff}
}

func placeholderWords() []string {
	out := make([]string, 0, len(placeholders()))
	for _, p := range placeholders() {
		out = append(out, strings.Trim(p, "{}"))
	}
	return out
}

// readsOf is what a word in the `reads` list takes out of the answer.
func readsOf(name string) (Reads, bool) {
	for _, r := range readsSet() {
		if r.word == name {
			return r.reads, true
		}
	}
	return 0, false
}

// readsSet is the closed set of things a step may declare it reads, in the
// order step.go declares them.
func readsSet() []struct {
	word  string
	reads Reads
} {
	return []struct {
		word  string
		reads Reads
	}{{"plan", ReadsPlan}, {"grade", ReadsGrade}, {"questions", ReadsQuestions}, {"findings", ReadsFindings}}
}

func readsWords() []string {
	out := make([]string, 0, len(readsSet()))
	for _, r := range readsSet() {
		out = append(out, r.word)
	}
	return out
}

func pauseWords() []string {
	out := make([]string, 0, len(PauseRules()))
	for _, r := range PauseRules() {
		word := string(r)
		if r == PauseAlways {
			word += "-when <grade>"
		}
		out = append(out, word)
	}
	return out
}

// readWordings puts the profile's own prose on the pipeline: one file per key
// the steps name, under the profile's prompts directory.
//
// A key with no file stops the load rather than falling back to shhh's own
// words. A profile is a set of instructions for one kind of work, and a run
// that sent the code profile's implement wording to a reading step would be
// telling the model to build something; the person who wrote the directory is
// the one who can see the file is missing.
func readWordings(fsys fs.FS, show func(rel string) string, p *Pipeline) error {
	for _, key := range p.WordingKeys() {
		rel := ProfileWordings + "/" + key + ".md"
		text, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return fmt.Errorf("%s: profile %q instructs its %s step from a file that is not there",
				show(rel), p.Name, wordingStep(key))
		}
		// The file's last newline is the editor's, not the wording's: every
		// text file ends in one and a wording that kept it would differ from
		// the same words held in the binary by exactly that byte.
		p.setWording(key, strings.TrimSuffix(string(text), "\n"))
	}
	return nil
}

// wordingStep is the step a key belongs to, for the sentence about a file
// that is not there.
func wordingStep(key string) string {
	if key == WordingStandards {
		return "shared standards"
	}
	return strings.TrimSuffix(key, "_task")
}

// setWording keeps one wording where the run reads it back: the shared
// sentence on the pipeline, a step's own and its child's on the step.
func (p *Pipeline) setWording(key, text string) {
	if key == WordingStandards {
		p.Standards = text
		return
	}
	for i := range p.Steps {
		switch key {
		case p.Steps[i].Key():
			p.Steps[i].Builtin = text
		case p.Steps[i].TaskKey():
			p.Steps[i].TaskBuiltin = text
		}
	}
}
