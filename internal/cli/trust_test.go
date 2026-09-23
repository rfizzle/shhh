package cli

import (
	"io"
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

	answer := project.Trust{Root: "/repo", Fingerprint: "fp1", Present: []project.Kind{project.KindSkills, project.KindGate},
		Digests: map[project.Kind]string{project.KindGate: "d1"}}
	note, err := setProjectTrust(db, answer, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"skills", "quality suites", ".shhh/quality.json", "shhh trust off"} {
		if !strings.Contains(note, want) {
			t.Errorf("the answer does not say what it covers (%q):\n%s", want, note)
		}
	}
	if kinds, ok := db.ProjectTrusted("/repo"); !ok || kinds[string(project.KindGate)] != "d1" {
		t.Fatalf("recorded = %v %v", kinds, ok)
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

	// An edited checkout is still a trusted one: the row says what moved and
	// offers nothing, because there is no answer left to give.
	edited := held
	edited.Granted, edited.Changed = true, []project.Kind{project.KindSkills}
	if f := doctorTrust(edited, true); f.Outcome != "changed" || f.State != components.DoctorPassed ||
		f.Apply != nil || !strings.Contains(f.Consequence, "skills") {
		t.Errorf("an edited checkout reads as %+v", f)
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
	if !strings.Contains(note, "MCP servers") || !strings.Contains(note, "`shhh trust`") {
		t.Errorf("note = %q", note)
	}
	withProjectTrust(t, project.Trust{Root: "/repo", Granted: true, Present: []project.Kind{project.KindServers}})
	if note := trustStartupNote(); note != "" {
		t.Errorf("a trusted checkout still complained: %q", note)
	}
}

// readTrustAt is what a session's first ask would read in the checkout at
// root, held the way that ask holds it, without changing directory.
func readTrustAt(t *testing.T, root string) project.Trust {
	t.Helper()
	db, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	read := project.ReadTrust(root, db)
	_ = db.Close()
	trustHeld.mu.Lock()
	trustHeld.read = &read
	trustHeld.mu.Unlock()
	return read
}

// The answer is about the checkout: an edit to its suites leaves the gate
// registered, the first session after the edit says which kind moved, that
// session's start re-stamps the record so the next one says nothing, and
// `shhh trust off` is what takes the gate away.
func TestATrustedCheckoutKeepsItsGateAndIsToldOnce(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Cleanup(forgetProjectTrust)
	root := t.TempDir()
	suite := filepath.Join(root, ".shhh", "quality.json")
	if err := os.MkdirAll(filepath.Dir(suite), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(suite, []byte(`{"suites":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setProjectTrust(db, readTrustAt(t, root), true); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	if err := os.WriteFile(suite, []byte(`{"suites":{"default":{"checks":[{"name":"t","run":"true"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The first session after the edit.
	first := readTrustAt(t, root)
	if openQualityGate(config.Config{}, nil, nil) == nil {
		t.Fatal("an edit to the suites took the gate away")
	}
	note := trustStartupNote()
	if !strings.Contains(note, "quality suites") || !strings.Contains(note, "changed") {
		t.Errorf("the first session after the edit said %q", note)
	}
	if chat := chatTrust(nil); len(chat.Changed) != 1 || chat.Changed[0] != string(project.KindGate) || len(chat.Withheld) != 0 {
		t.Errorf("the start screen was handed %+v", chat)
	}
	restampProjectTrust()
	// The held reading is this session's, and still says what it found.
	if held := projectTrust(); len(held.Changed) == 0 {
		t.Error("the re-stamp rewrote what this session is showing")
	}
	// A second session in the same process — `shhh serve` opens many over
	// one held reading — does not say it again.
	if note := trustStartupNote(); note != "" {
		t.Errorf("a second session in the same process said it again: %q", note)
	}

	// The second session.
	forgetProjectTrust()
	readTrustAt(t, root)
	if note := trustStartupNote(); note != "" {
		t.Errorf("the second session said it again: %q", note)
	}
	if openQualityGate(config.Config{}, nil, nil) == nil {
		t.Fatal("the second session lost the gate")
	}

	// `shhh trust off` withdraws it.
	cmd := newTrustCmd()
	cmd.SetArgs([]string{"off"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	readTrustAt(t, root)
	if openQualityGate(config.Config{}, nil, nil) != nil {
		t.Error("`shhh trust off` left the gate registered")
	}
	if _, ok := first.Digests[project.KindGate]; !ok {
		t.Error("the reading carried no digest for the kind it named")
	}
}

// A project server's definition edited in a trusted checkout is still
// admitted: the connect is handed the answer, and the answer did not move.
func TestAnEditedServerDefinitionIsStillAdmitted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Cleanup(forgetProjectTrust)
	root := t.TempDir()
	defs := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(defs, []byte(`{"mcpServers":{"gh":{"command":"a"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setProjectTrust(db, readTrustAt(t, root), true); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.WriteFile(defs, []byte(`{"mcpServers":{"gh":{"command":"b"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	readTrustAt(t, root)
	if opts := mcpOptions(config.Config{}, false); !opts.Project.Granted {
		t.Error("an edited definition was refused its checkout's answer")
	}
}

// `shhh trust` takes the one word it has and nothing else.
func TestTrustCommandTakesOffAndNothingElse(t *testing.T) {
	for _, args := range [][]string{{"on"}, {"off", "now"}} {
		cmd := newTrustCmd()
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "shhh trust [off]") {
			t.Errorf("%v = %v", args, err)
		}
	}
}

// The doctor's trust row in each standing it can report: trusted, trusted and
// changed since a session last read it, and not answered for.
func TestDoctorTrustRowGolden(t *testing.T) {
	present := []project.Kind{project.KindSkills, project.KindGate}
	checks := []components.DoctorCheck{
		doctorCheck("trust", doctorTrust(project.Trust{Root: "/repo", Granted: true, Present: present}, true), 0),
		doctorCheck("trust", doctorTrust(project.Trust{Root: "/repo", Granted: true, Present: present,
			Changed: []project.Kind{project.KindGate}}, true), 0),
		doctorCheck("trust", doctorTrust(project.Trust{Root: "/repo", Present: present}, true), 0),
	}
	assertReportGolden(t, "doctor.trust", doctorReportOf("shhh doctor", "check", "checks", checks).Render(80))
}
