package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// writeWording puts one wording in the test's own directory and returns its
// absolute path, so nothing here depends on where the test was run from.
func writeWording(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Nothing named is nothing read: every field stays empty, which is what the
// built-in wordings stand on.
func TestLoadPrompts_UnsetReadsNothing(t *testing.T) {
	got, err := loadPrompts(config.PromptsConfig{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != (sessionPrompts{}) {
		t.Fatalf("an empty block must read nothing, got %+v", got)
	}
}

// Each named file becomes the wording it was named for.
func TestLoadPrompts_ReadsEachWording(t *testing.T) {
	got, err := loadPrompts(config.PromptsConfig{
		Steer:      writeWording(t, "steer.md", "drifted: "+agent.PlaceholderTarget),
		CheckIn:    writeWording(t, "checkin.md", "used "+agent.PlaceholderRounds),
		Summary:    writeWording(t, "summary.md", "read it"),
		Classifier: writeWording(t, "classifier.md", "decide it"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := sessionPrompts{
		steer:      "drifted: " + agent.PlaceholderTarget,
		checkIn:    "used " + agent.PlaceholderRounds,
		summary:    "read it",
		classifier: "decide it",
	}
	if got != want {
		t.Fatalf("wordings:\ngot  %+v\nwant %+v", got, want)
	}
}

// A file that cannot be read stops the session and says which key and which
// path, because falling back silently would leave a session running the
// built-in wording while its operator believes it is running theirs.
func TestLoadPrompts_UnreadableFileIsFatal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there.md")
	_, err := loadPrompts(config.PromptsConfig{Steer: missing}, "")
	if err == nil {
		t.Fatal("a wording that cannot be read must stop the session")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "prompts.steer") {
		t.Fatalf("the error must name the key and the path, got %q", err)
	}
}

// So does a file naming a substitution its wording does not take: the value
// would never arrive and nothing on the way past would look wrong. The two
// wordings sent as written take none at all, so any is a mistake there.
func TestLoadPrompts_UnknownPlaceholderIsFatal(t *testing.T) {
	path := writeWording(t, "steer.md", "asked for {{targt}}")
	_, err := loadPrompts(config.PromptsConfig{Steer: path}, "")
	if err == nil {
		t.Fatal("a substitution that does not exist must stop the session")
	}
	if !strings.Contains(err.Error(), "{{targt}}") || !strings.Contains(err.Error(), path) {
		t.Fatalf("the error must name the placeholder and the path, got %q", err)
	}
	if _, err := loadPrompts(config.PromptsConfig{
		Summary: writeWording(t, "summary.md", "judge "+agent.PlaceholderTarget),
	}, ""); err == nil {
		t.Fatal("a wording sent as written must refuse a substitution")
	}
}

// An empty file is the unreadable one wearing a disguise: every reader
// downstream takes an empty wording as "not configured", so the built-in
// words would go back and the record would show a session that overrode
// nothing.
func TestLoadPrompts_EmptyFileIsFatal(t *testing.T) {
	for _, body := range []string{"", "   \n\t\n"} {
		path := writeWording(t, "steer.md", body)
		_, err := loadPrompts(config.PromptsConfig{Steer: path}, "")
		if err == nil {
			t.Fatalf("an empty wording (%q) must stop the session", body)
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("the error must name the path and say what is wrong, got %q", err)
		}
		if !strings.Contains(err.Error(), "remove the key") {
			t.Fatalf("a file the settings named is put back by removing the key, got %q", err)
		}
	}
	// A checkout's file is put back by deleting it; there is no key to
	// remove, and sending the reader to their settings would send them to a
	// file that has nothing to do with it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todo_commit.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadPrompts(config.PromptsConfig{}, dir)
	if err == nil || !strings.Contains(err.Error(), "delete it") {
		t.Fatalf("an empty checkout wording must say how to put the built-in back, got %v", err)
	}
}

// A path written relative to the config file resolves beside it, so a wording
// kept next to the settings travels with them instead of depending on which
// directory the session was opened in.
func TestPromptPath_RelativeToTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	beside := filepath.Join(dir, "shhh")
	if err := os.MkdirAll(beside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beside, "steer.md"), []byte("beside the settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPrompts(config.PromptsConfig{Steer: "steer.md"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.steer != "beside the settings" {
		t.Fatalf("a relative path must resolve beside the config file, got %q", got.steer)
	}
}

// The numbers reach the machinery from their own keys, and an unset key
// stays zero so the built-in constant is what stands.
func TestSteeringFromConfig(t *testing.T) {
	var cfg config.Config
	if got := steering(cfg, sessionPrompts{}); got != (agent.Steering{}) {
		t.Fatalf("an unconfigured session must run the built-in set, got %+v", got)
	}
	cfg.Behavior.CheckInIntervalRounds = 15
	cfg.Behavior.CheckInMaxDoublings = -1
	cfg.Summary.SteerTargetChars = 120
	got := steering(cfg, sessionPrompts{steer: "S", checkIn: "C"})
	want := agent.Steering{
		CheckInInterval: 15, CheckInDoublings: -1, SteerTargetChars: 120,
		CheckIn: "C", Steer: "S",
	}
	if got != want {
		t.Fatalf("steering:\ngot  %+v\nwant %+v", got, want)
	}
}

// A wording is part of what a session was sent, so it is part of the
// fingerprint: overriding nothing hashes as it always did, the same file
// hashes the same, and an edit to it splits the record.
func TestPrompts_FoldIntoTheFingerprint(t *testing.T) {
	const sys = "the system prompt"
	if got := (sessionPrompts{}).fingerprintOf(sys); fingerprint(got) != fingerprint(sys) {
		t.Fatal("a session that overrode nothing must hash exactly as it did before")
	}

	one := sessionPrompts{steer: "steer as written"}
	if fingerprint(one.fingerprintOf(sys)) == fingerprint(sys) {
		t.Fatal("an override that does not split the record is a knob with no instrument on it")
	}
	same := sessionPrompts{steer: "steer as written"}
	if fingerprint(one.fingerprintOf(sys)) != fingerprint(same.fingerprintOf(sys)) {
		t.Fatal("the same wording must hash the same on every run")
	}
	edited := sessionPrompts{steer: "steer as edited"}
	if fingerprint(one.fingerprintOf(sys)) == fingerprint(edited.fingerprintOf(sys)) {
		t.Fatal("an edit to a wording must split the cohorts")
	}
	// Which wording changed matters too: two files swapped are two
	// different sessions, not one.
	swapped := sessionPrompts{checkIn: "steer as written"}
	if fingerprint(one.fingerprintOf(sys)) == fingerprint(swapped.fingerprintOf(sys)) {
		t.Fatal("the same text in a different wording must hash differently")
	}
}

// A stage wording is read like any other: the file becomes what the run
// sends, and a stage nothing named keeps the built-in words.
func TestLoadPrompts_ReadsTheStageWordings(t *testing.T) {
	got, err := loadPrompts(config.PromptsConfig{
		TodoStandards: writeWording(t, "standards.md", "our standards"),
		TodoResearch:  writeWording(t, "research.md", "read "+agent.PlaceholderItem),
		TodoCommit:    writeWording(t, "commit.md", "commit it"),
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := run.Wordings{
		Standards: "our standards",
		Research:  "read " + agent.PlaceholderItem,
		Commit:    "commit it",
	}
	if got.todo != want {
		t.Fatalf("stage wordings = %+v, want %+v", got.todo, want)
	}
}

// A stage file that cannot be read stops the session with the key and the
// path, for the reason a steer that cannot be read does: a run on the
// built-in words while the person believes it is running theirs.
func TestLoadPrompts_AnUnreadableStageWordingIsFatal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there.md")
	_, err := loadPrompts(config.PromptsConfig{TodoReview: missing}, "")
	if err == nil {
		t.Fatal("a stage wording that cannot be read must stop the session")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "prompts.todo_review") {
		t.Fatalf("the error must name the key and the path, got %q", err)
	}
	if _, err := loadPrompts(config.PromptsConfig{
		TodoRemediate: writeWording(t, "remediate.md", "fix "+agent.PlaceholderPlan),
	}, ""); err == nil {
		t.Fatal("a stage wording naming a block its stage has not got must be refused")
	}
}

// A checkout's own file beats the settings for that project, and only for
// that project: the same settings outside it read the person's file.
func TestLoadPrompts_ACheckoutsFileBeatsTheUserFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo_research.md"), []byte("the checkout's own"), 0o600); err != nil {
		t.Fatal(err)
	}
	mine := writeWording(t, "research.md", "mine")
	inside, err := loadPrompts(config.PromptsConfig{TodoResearch: mine}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if inside.todo.Research != "the checkout's own" {
		t.Fatalf("inside the checkout the project's file must win, got %q", inside.todo.Research)
	}
	outside, err := loadPrompts(config.PromptsConfig{TodoResearch: mine}, "")
	if err != nil {
		t.Fatal(err)
	}
	if outside.todo.Research != "mine" {
		t.Fatalf("outside it the person's file must stand, got %q", outside.todo.Research)
	}
	// A checkout with no file for a key takes nothing away.
	kept, err := loadPrompts(config.PromptsConfig{TodoCommit: writeWording(t, "commit.md", "mine too")}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if kept.todo.Commit != "mine too" {
		t.Fatalf("a checkout that says nothing about a key must leave it alone, got %q", kept.todo.Commit)
	}
}

// The wordings a checkout may hand a session are behind its trust answer,
// the way every other file it declares is.
func TestProjectPrompts_IsBehindTrust(t *testing.T) {
	withProjectTrust(t, project.Trust{Root: "/repo"})
	if got := projectPrompts(); got != "" {
		t.Fatalf("an untrusted checkout must hand over no wordings, got %q", got)
	}
	withProjectTrust(t, project.Trust{Root: "/repo", Granted: true})
	if got := projectPrompts(); got != filepath.Join("/repo", filepath.FromSlash(project.PromptsDir)) {
		t.Fatalf("a trusted checkout's wordings = %q", got)
	}
	withProjectTrust(t, project.Trust{Granted: true})
	if got := projectPrompts(); got != "" {
		t.Fatalf("no checkout is no directory to read, got %q", got)
	}
}

// The substitutions the loader checks a file against and the ones the runner
// places are the same names. They are written down twice, so this is what
// keeps them one vocabulary.
func TestStagePlaceholdersAreOneVocabulary(t *testing.T) {
	for _, pair := range [][2]string{
		{agent.PlaceholderItem, run.PlaceholderItem},
		{agent.PlaceholderPlan, run.PlaceholderPlan},
		{agent.PlaceholderAnswers, run.PlaceholderAnswers},
		{agent.PlaceholderFindings, run.PlaceholderFindings},
		{agent.PlaceholderDiff, run.PlaceholderDiff},
	} {
		if pair[0] != pair[1] {
			t.Errorf("the loader checks for %q and the runner places %q", pair[0], pair[1])
		}
	}
}

// A stage wording is part of what a run was sent, so it divides the record
// the way a steer file does.
func TestStageWordings_FoldIntoTheFingerprint(t *testing.T) {
	const sys = "system prompt"
	one := sessionPrompts{todo: run.Wordings{Research: "as written"}}
	if fingerprint(one.fingerprintOf(sys)) == fingerprint(sys) {
		t.Fatal("a replaced stage wording must divide the record")
	}
	edited := sessionPrompts{todo: run.Wordings{Research: "as edited"}}
	if fingerprint(edited.fingerprintOf(sys)) == fingerprint(one.fingerprintOf(sys)) {
		t.Fatal("an edit to a stage wording must divide it again")
	}
	swapped := sessionPrompts{todo: run.Wordings{Commit: "as written"}}
	if fingerprint(swapped.fingerprintOf(sys)) == fingerprint(one.fingerprintOf(sys)) {
		t.Fatal("which stage holds the text is part of what was sent")
	}
}

// The three ranks, in order. A checkout's file beats a key, a key beats the
// directory beside the settings, and the directory beside the settings beats
// the built-in words — which is what makes a file's presence the override
// with nothing pointing at it.
func TestPromptSource_MostSpecificFileWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	mine := filepath.Join(dir, "shhh", "prompts")
	must(t, os.MkdirAll(mine, 0o700))
	must(t, os.WriteFile(filepath.Join(mine, "steer.md"), []byte("beside my settings"), 0o600))

	checkout := t.TempDir()
	must(t, os.WriteFile(filepath.Join(checkout, "steer.md"), []byte("the checkout's own"), 0o600))
	named := writeWording(t, "steer.md", "the key's own")

	for _, tc := range []struct {
		name       string
		configured string
		project    string
		want       string
	}{
		{"the directory beside the settings", "", "", "beside my settings"},
		{"a key beats it", named, "", "the key's own"},
		{"the checkout beats both", named, checkout, "the checkout's own"},
		{"the checkout beats the directory too", "", checkout, "the checkout's own"},
	} {
		got, err := loadPrompts(config.PromptsConfig{Steer: tc.configured}, tc.project)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.steer != tc.want {
			t.Errorf("%s: steer = %q, want %q", tc.name, got.steer, tc.want)
		}
	}
}

// The directory beside the settings is read the way every other rank is: an
// empty file there is the same failure as an empty one anywhere, named by the
// path so the reader knows which of the three to go to.
func TestPromptSource_AnEmptyFileBesideTheSettingsIsFatal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	mine := filepath.Join(dir, "shhh", "prompts")
	must(t, os.MkdirAll(mine, 0o700))
	path := filepath.Join(mine, "summary.md")
	must(t, os.WriteFile(path, []byte("  \n"), 0o600))

	_, err := loadPrompts(config.PromptsConfig{}, "")
	if err == nil {
		t.Fatal("an empty wording beside the settings started the session")
	}
	if msg := err.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, "delete it") {
		t.Fatalf("the refusal does not name the file and the way back: %s", msg)
	}
}

// A key naming a file that is not there is an answer, and a wrong one. It
// must not fall through to the directory below: a person who wrote a path
// and got the wording from beside their settings would have no way to see
// the typo, and the record would say they were running theirs.
func TestPromptSource_AKeyNamingAMissingFileDoesNotFallThrough(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	mine := filepath.Join(dir, "shhh", "prompts")
	must(t, os.MkdirAll(mine, 0o700))
	must(t, os.WriteFile(filepath.Join(mine, "steer.md"), []byte("beside my settings"), 0o600))

	missing := filepath.Join(t.TempDir(), "typo.md")
	_, err := loadPrompts(config.PromptsConfig{Steer: missing}, "")
	if err == nil {
		t.Fatal("a key naming a file that is not there fell through to the directory below")
	}
	if msg := err.Error(); !strings.Contains(msg, missing) || !strings.Contains(msg, "prompts.steer") {
		t.Fatalf("the refusal does not name the key and the path it named: %s", msg)
	}
}
