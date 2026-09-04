package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// withProjectTrust states this process's answer for the length of one test,
// so a case can be about what the answer does rather than about writing a
// checkout and a store to imply one.
func withProjectTrust(t *testing.T, answer project.Trust) {
	t.Helper()
	back := projectTrust
	projectTrust = func() project.Trust { return answer }
	t.Cleanup(func() { projectTrust = back })
}

// A cloned repository names check commands in a file, and the gate tool runs
// them without an approval. Until the person has answered for the checkout
// there is no runner at all, so nothing registers the tool and the model is
// never offered something that would refuse.
func TestQualityGateIsWithheldUntilTheCheckoutIsTrusted(t *testing.T) {
	store, err := evidence.Open(filepath.Join(t.TempDir(), "evidence"), evidence.NewSessionID())
	if err != nil {
		t.Fatal(err)
	}
	red := evidence.NewReducer(store)

	withProjectTrust(t, project.Trust{Root: "/repo", Present: []project.Kind{project.KindGate}})
	if gate := openQualityGate(config.Config{}, red, nil); gate != nil {
		t.Fatal("an untrusted checkout's suites are loaded")
	}
	withProjectTrust(t, project.Trust{Root: "/repo", Granted: true})
	if gate := openQualityGate(config.Config{}, red, nil); gate == nil {
		t.Fatal("a trusted checkout has its gate")
	}
}

// The project scope is not searched at all until the checkout is trusted;
// the person's own skills are theirs and are read either way.
func TestSkillRootsFollowTheCheckoutsStanding(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(t.TempDir(), "skills")

	withheld := skill.Roots(repo, []string{mine}, false)
	for _, r := range withheld {
		if r.Scope == skill.ScopeProject {
			t.Fatalf("an untrusted checkout offered %s", r.Path)
		}
	}
	if len(withheld) == 0 || withheld[0].Path != mine {
		t.Fatalf("the user's own skills went missing: %+v", withheld)
	}
	trusted := skill.Roots(repo, []string{mine}, true)
	if len(trusted) <= len(withheld) {
		t.Fatalf("trusting added no project root: %+v", trusted)
	}
}

// Trusting is one answer about the checkout, recorded outside it, and
// withdrawing it is the same answer given back.
func TestSetProjectTrustRecordsAndWithdrawsTheAnswer(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "shhh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	answer := project.Trust{Root: "/repo", Fingerprint: "fp1", Present: []project.Kind{project.KindSkills, project.KindGate}}
	note, err := setProjectTrust(db, answer, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"skills", "quality suites", ".shhh/quality.json"} {
		if !strings.Contains(note, want) {
			t.Errorf("the answer does not say what it covers (%q):\n%s", want, note)
		}
	}
	if fp, ok := db.ProjectTrusted("/repo"); !ok || fp != "fp1" {
		t.Fatalf("recorded = %q %v", fp, ok)
	}
	if _, err := setProjectTrust(db, answer, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.ProjectTrusted("/repo"); ok {
		t.Error("still trusted after it was withdrawn")
	}
	if _, err := setProjectTrust(nil, answer, true); err == nil {
		t.Error("trust was recorded with nowhere to record it")
	}
}

// The doctor and `shhh mcp` both re-run their checks when an offer on a row
// is taken, and both read the checkout's standing to answer. An answer this
// process is still holding from before the write would make the row under
// the offer contradict the offer the reader just accepted.
func TestRecordingTrustDropsTheHeldReading(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "shhh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	answer := project.Trust{Root: "/repo", Fingerprint: "fp1", Present: []project.Kind{project.KindSkills}}
	hold := func() {
		t.Helper()
		trustHeld.mu.Lock()
		held := answer
		trustHeld.read = &held
		trustHeld.mu.Unlock()
	}
	stillHeld := func() bool {
		trustHeld.mu.Lock()
		defer trustHeld.mu.Unlock()
		return trustHeld.read != nil
	}
	t.Cleanup(forgetProjectTrust)

	hold()
	if _, err := setProjectTrust(db, answer, true); err != nil {
		t.Fatal(err)
	}
	if stillHeld() {
		t.Error("the reading survived the answer, so a re-run would report the state before it")
	}
	hold()
	if _, err := setProjectTrust(db, answer, false); err != nil {
		t.Fatal(err)
	}
	if stillHeld() {
		t.Error("the reading survived the withdrawal")
	}
	// A write that could not happen leaves the reading alone: there is
	// nothing new to read.
	hold()
	if _, err := setProjectTrust(nil, answer, true); err == nil {
		t.Fatal("a write with nowhere to write reported success")
	}
	if !stillHeld() {
		t.Error("a refused write dropped a reading that is still current")
	}
}

// Withholding is a diagnostic. The doctor's row is a skip with the list on
// it, never a failure, because the session started — with less in it.
func TestDoctorTrustReadsAsADiagnostic(t *testing.T) {
	held := project.Trust{Root: "/repo", Present: []project.Kind{project.KindSkills, project.KindAgents}}
	f := doctorTrust(held, true)
	if f.State != components.DoctorSkipped {
		t.Fatalf("withholding read as %v rather than a diagnostic", f.State)
	}
	if f.Outcome != "untrusted" || !strings.Contains(f.Detail, "agent profiles") {
		t.Errorf("row = %+v", f)
	}
	if f.Apply == nil || f.Action == "" {
		t.Error("the row offers no way to answer")
	}

	edited := held
	edited.Changed = true
	if f := doctorTrust(edited, true); f.Outcome != "changed" {
		t.Errorf("an edited checkout reads as %q", f.Outcome)
	}
	if f := doctorTrust(project.Trust{Root: "/repo", Granted: true}, true); f.Outcome != "ok" {
		t.Errorf("a trusted checkout reads as %q", f.Outcome)
	}
	// A repository that declares nothing has nothing to withhold, and a
	// warning on every empty checkout is a warning nobody reads.
	if f := doctorTrust(project.Trust{Root: "/repo"}, true); f.Action != "" {
		t.Errorf("an empty checkout was offered trust: %+v", f)
	}
	if f := doctorTrust(project.Trust{}, true); f.Outcome != "empty" {
		t.Errorf("outside a project = %+v", f)
	}
}

// The session says once, before it starts, what the checkout was holding
// back — the headless run has no screen to read it off later.
func TestTrustStartupNoteNamesWhatIsMissing(t *testing.T) {
	withProjectTrust(t, project.Trust{Root: "/repo", Present: []project.Kind{project.KindServers}})
	note := trustStartupNote()
	if !strings.Contains(note, "MCP servers") || !strings.Contains(note, "shhh doctor trust") {
		t.Errorf("note = %q", note)
	}
	withProjectTrust(t, project.Trust{Root: "/repo", Granted: true, Present: []project.Kind{project.KindServers}})
	if note := trustStartupNote(); note != "" {
		t.Errorf("a trusted checkout still complained: %q", note)
	}
}
