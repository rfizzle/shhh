package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
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
	got, err := loadPrompts(config.PromptsConfig{})
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
	})
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
	_, err := loadPrompts(config.PromptsConfig{Steer: missing})
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
	_, err := loadPrompts(config.PromptsConfig{Steer: path})
	if err == nil {
		t.Fatal("a substitution that does not exist must stop the session")
	}
	if !strings.Contains(err.Error(), "{{targt}}") || !strings.Contains(err.Error(), path) {
		t.Fatalf("the error must name the placeholder and the path, got %q", err)
	}
	if _, err := loadPrompts(config.PromptsConfig{
		Summary: writeWording(t, "summary.md", "judge "+agent.PlaceholderTarget),
	}); err == nil {
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
		_, err := loadPrompts(config.PromptsConfig{Steer: path})
		if err == nil {
			t.Fatalf("an empty wording (%q) must stop the session", body)
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("the error must name the path and say what is wrong, got %q", err)
		}
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
	got, err := loadPrompts(config.PromptsConfig{Steer: "steer.md"})
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
