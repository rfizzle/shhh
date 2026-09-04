package cli

// The doctor host (docs/interface/surfaces.md#the-supporting-screens).
// The screen renders; every judgement is here, and every judgement is a pure
// function of what was probed — so this file checks the readings on a machine
// that has no containment mechanism, no provider key and no git repository,
// without needing any of them.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/lsp"
	"github.com/rfizzle/shhh/internal/migrate"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Every check's name has to fit the eight-column verb field with a gap
// after it, or the target beside it starts touching the name. This is the
// guard on the vocabulary rather than on any one row.
func TestDoctorProbes_NamesFitTheVerbField(t *testing.T) {
	for _, probe := range doctorProbes() {
		if len(probe.name) > 7 {
			t.Fatalf("check %q is %d columns; the verb field is 8 and needs a gap",
				probe.name, len(probe.name))
		}
	}
}

// `shhh code doctor` runs a slice of the same checks rather than a set of its
// own: two commands reporting differently on one machine is the thing the
// promotion
// was resolving.
func TestContainmentProbes_AreASliceOfTheWholeRun(t *testing.T) {
	all := map[string]bool{}
	for _, probe := range doctorProbes() {
		all[probe.name] = true
	}
	for _, probe := range containmentProbes() {
		if !all[probe.name] {
			t.Fatalf("shhh code doctor runs %q, which shhh doctor does not", probe.name)
		}
	}
	if len(containmentProbes()) >= len(doctorProbes()) {
		t.Fatal("the containment slice is not a slice")
	}
}

// The binary row names what is running, and a dev build says so rather than
// claiming a version — the update row below it is about to explain itself by
// this.
func TestDoctorBinary(t *testing.T) {
	f := doctorBinary("0.9.4", "darwin", "arm64", "/opt/homebrew/bin/shhh")
	if f.Subject != "shhh 0.9.4" || !strings.Contains(f.Detail, "darwin/arm64") {
		t.Fatalf("the binary row does not name the build: %+v", f)
	}
	if f.State != components.DoctorPassed {
		t.Fatal("a build that is running failed its own check")
	}

	dev := doctorBinary("dev", "linux", "amd64", "")
	if !strings.Contains(dev.Subject, "dev build") || dev.Outcome != "unversioned" {
		t.Fatalf("a dev build claims a version: %+v", dev)
	}
}

// No config file is not a fault — shhh runs on its defaults — so it is `⊘`
// with the words for it, and the fix says where one would go.
func TestDoctorConfig(t *testing.T) {
	read := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{
		Provider: config.ProviderConfig{Default: "anthropic", Model: "claude-opus-5"},
	}, config.Project{}, nil)
	if read.State != components.DoctorPassed || !strings.Contains(read.Detail, "2 settings set") {
		t.Fatalf("a config file that was read does not say what it set: %+v", read)
	}

	none := doctorConfig("", []string{"/home/u/.config/shhh/config.toml"}, config.Config{}, config.Project{}, nil)
	if none.State != components.DoctorSkipped {
		t.Fatalf("no config file was reported as a fault: %+v", none)
	}
	if len(none.Fix) == 0 || !strings.Contains(strings.Join(none.Fix, "\n"), "config.toml") {
		t.Fatalf("no config file does not say where one would go: %+v", none)
	}
}

// A file that would not load is the row's failure, in the words every other
// command refused with, and the fix names the file.
func TestDoctorConfig_RefusedFile(t *testing.T) {
	err := &config.UnknownKeyError{
		Path: "/home/u/.config/shhh/config.toml",
		Keys: []config.UnknownKey{{Key: "behaviour", Nearest: "behavior"}},
	}
	f := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{}, config.Project{}, err)
	if f.State != components.DoctorFailed || f.Outcome != "refused" {
		t.Fatalf("a file that would not load passed: %+v", f)
	}
	if f.Detail != `unknown key "behaviour"` {
		t.Fatalf("the detail is not the key alone, short enough to survive the column: %+v", f)
	}
	if len(f.Fix) != 1 || f.Fix[0] != "rename behaviour to behavior" {
		t.Fatalf("the fix does not offer the nearest key: %+v", f)
	}

	two := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{}, config.Project{}, &config.UnknownKeyError{
		Path: "/home/u/.config/shhh/config.toml",
		Keys: []config.UnknownKey{{Key: "behaviour", Nearest: "behavior"}, {Key: "top"}},
	})
	if two.Detail != `unknown keys "behaviour", "top"` || len(two.Fix) != 2 || !strings.HasPrefix(two.Fix[1], "remove top") {
		t.Fatalf("two keys are not two fix lines under one detail: %+v", two)
	}

	parse := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{}, config.Project{},
		errors.New("toml: line 1: expected '.' or ']'"))
	if parse.State != components.DoctorFailed || !strings.Contains(parse.Detail, "line 1") {
		t.Fatalf("a parse failure is not carried on the row: %+v", parse)
	}
	if len(parse.Fix) == 0 || !strings.Contains(parse.Fix[0], "config.toml") {
		t.Fatalf("the fix does not name the file: %+v", parse)
	}
}

// The refusal every other command gives at startup is the doctor's config
// row: `shhh doctor` runs on a file that would not load and says why, while
// any other command stops with the same sentence.
func TestDoctor_RunsOnRefusedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	must(t, os.MkdirAll(filepath.Join(dir, "shhh"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "shhh", "config.toml"),
		[]byte("[behaviour]\nsilent_mode = true\n"), 0o644))

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"doctor", "--table"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("shhh doctor on a refused config: %v", err)
	}
	if !strings.Contains(out.String(), `"behaviour"`) || !strings.Contains(out.String(), "rename behaviour to behavior") {
		t.Fatalf("the config row does not carry the refusal:\n%s", out.String())
	}

	var quiet bytes.Buffer
	other := NewRootCmd()
	other.SetOut(&quiet)
	other.SetErr(&quiet)
	other.SetArgs([]string{"memory", "list"})
	err := other.Execute()
	var unknown *config.UnknownKeyError
	if !errors.As(err, &unknown) {
		t.Fatalf("another command ran on a config that would not load: %v", err)
	}
}

// The settings count is the same one `shhh config` states: a value the file
// supplied, not a value shhh chose.
func TestConfigSettingsSet(t *testing.T) {
	if n := configSettingsSet(config.Config{}); n != 0 {
		t.Fatalf("an empty config counts %d settings", n)
	}
	on := true
	cfg := config.Config{
		Provider: config.ProviderConfig{Default: "openai", APIKey: "sk-x"},
		Behavior: config.BehaviorConfig{MaxToolRounds: 40, SafetyWarnings: &on},
		Sandbox:  config.SandboxConfig{Profile: "workspace-netless"},
	}
	if n := configSettingsSet(cfg); n != 5 {
		t.Fatalf("counted %d settings, not 5", n)
	}
}

func place(kind resolve.PlaceKind, found bool, finding, detail string) resolve.Place {
	return resolve.Place{Kind: kind, Found: found, Finding: finding, Detail: detail}
}

// A key found anywhere is a pass, and the row says which of the four places
// answered — that is the question this check is actually asked.
func TestDoctorModelFinding_FindsAKeyInEachPlace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		place resolve.Place
		want  string
	}{
		{"env", place(resolve.PlaceEnv, true, "OPENAI_API_KEY ···4f9c", ""), "key OPENAI_API_KEY ···4f9c found"},
		{"config", place(resolve.PlaceConfig, true, "provider.api_key ···4f9c", ""), "key provider.api_key ···4f9c found"},
		{"profiles", place(resolve.PlaceProfiles, true, "acme", ""), "gateway profile acme is ready"},
		{"local", place(resolve.PlaceLocal, true, "localhost:11434", ""), "a local runtime is answering"},
	} {
		f := doctorModelFinding("openai", "gpt-5.2", resolve.Survey{Places: []resolve.Place{tc.place}})
		if f.State != components.DoctorPassed || f.Outcome != "ok" {
			t.Fatalf("%s: a key that was found did not pass: %+v", tc.name, f)
		}
		if !strings.Contains(f.Detail, tc.want) {
			t.Fatalf("%s: the row does not say where the key came from: %q", tc.name, f.Detail)
		}
		if !strings.Contains(f.Detail, "gpt-5.2") {
			t.Fatalf("%s: the row dropped the model: %q", tc.name, f.Detail)
		}
	}
}

// A key found is reported as found and never as accepted: accepting one means
// spending a request on it, and a diagnostic that billed you for running it
// would be a diagnostic nobody runs.
func TestDoctorModelFinding_NeverClaimsAKeyWasAccepted(t *testing.T) {
	f := doctorModelFinding("openai", "gpt-5.2", resolve.Survey{
		Places: []resolve.Place{place(resolve.PlaceEnv, true, "OPENAI_API_KEY ···4f9c", "")},
	})
	if strings.Contains(f.Detail, "accepted") {
		t.Fatalf("the row claims a key was accepted without a request: %q", f.Detail)
	}
}

// No key anywhere is the one check that stops a session outright, so it fails
// rather than warns, and its fix names all four places with what was in each.
func TestDoctorModelFinding_NoKeyFailsAndNamesTheFourPlaces(t *testing.T) {
	survey := resolve.Survey{
		Provider: "anthropic",
		Places: []resolve.Place{
			place(resolve.PlaceEnv, false, "", "SHHH_API_KEY, ANTHROPIC_API_KEY — unset"),
			place(resolve.PlaceConfig, false, "", "~/.config/shhh/config.toml — no such file"),
			place(resolve.PlaceProfiles, false, "", "no .toml in ~/.config/shhh/providers"),
			place(resolve.PlaceLocal, false, "", "localhost:11434 — nothing listening"),
		},
	}
	f := doctorModelFinding("anthropic", "claude-opus-5", survey)
	if f.State != components.DoctorFailed || f.Outcome != "no key" {
		t.Fatalf("no key anywhere did not fail: %+v", f)
	}
	if f.Consequence == "" {
		t.Fatal("no key anywhere does not say what it costs")
	}
	if len(f.Fix) != 4 {
		t.Fatalf("the fix names %d places, not the four that were looked in: %v", len(f.Fix), f.Fix)
	}
	for _, kind := range []string{"env", "config", "profiles", "local"} {
		if !strings.Contains(strings.Join(f.Fix, "\n"), kind) {
			t.Fatalf("the fix does not name the %s place: %v", kind, f.Fix)
		}
	}
}

// Where the survey has a likely way in, the fix ends with it rather than
// leaving the reader to pick between four dead ends.
func TestDoctorModelFinding_TheFixCarriesTheLikelyWayIn(t *testing.T) {
	survey := resolve.Survey{
		Places: []resolve.Place{place(resolve.PlaceEnv, false, "", "unset")},
		Likely: "a gateway profile is ready — start on it with --provider acme",
	}
	f := doctorModelFinding("openai", "", survey)
	if last := f.Fix[len(f.Fix)-1]; !strings.Contains(last, "gateway profile is ready") {
		t.Fatalf("the fix does not end with the likely way in: %q", last)
	}
}

// A store that will not open is the check that explains four other surfaces
// being empty, so it says so.
func TestDoctorStore(t *testing.T) {
	ok := doctorStore("/home/u/.local/share/shhh/shhh.db", 57344, nil)
	if ok.State != components.DoctorPassed || !strings.Contains(ok.Detail, "56 kB") {
		t.Fatalf("an open store does not report itself: %+v", ok)
	}

	broken := doctorStore("", 0, errors.New("disk is read-only"))
	if broken.State != components.DoctorFailed {
		t.Fatalf("an unreadable store did not fail: %+v", broken)
	}
	for _, want := range []string{"history", "metrics"} {
		if !strings.Contains(broken.Consequence, want) {
			t.Fatalf("the consequence does not name %s: %q", want, broken.Consequence)
		}
	}
}

// A store nobody has written to yet is stated as such: `0 B` there reads like
// a fault where the truth is a fresh install.
func TestDoctorBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "nothing recorded yet"}, {512, "512 B"}, {57344, "56 kB"}, {3 << 20, "3.0 MB"},
	} {
		if got := doctorBytes(tc.n); got != tc.want {
			t.Fatalf("%d bytes reads as %q, not %q", tc.n, got, tc.want)
		}
	}
}

// A machine with a mechanism says which one and under which profile.
func TestDoctorSandbox_Contained(t *testing.T) {
	f := doctorSandbox(sandbox.Availability{
		OK: true, Mechanism: "bwrap", Detail: "bubblewrap with unprivileged user namespaces",
	}, "workspace", "linux")
	if f.State != components.DoctorPassed || f.Subject != "bwrap" {
		t.Fatalf("a contained host does not say so: %+v", f)
	}
	if !strings.Contains(f.Detail, "workspace profile") {
		t.Fatalf("the row does not name the profile: %q", f.Detail)
	}
	if f.Consequence != "" || len(f.Fix) != 0 {
		t.Fatalf("a passing check offered a fix: %+v", f)
	}
}

// The consequence is quoted from the surface the reader will actually meet it
// on: the approval card promotes ⚠ UNCONTAINED to its title bar when nothing
// wraps the command.
func TestDoctorSandbox_UncontainedQuotesTheApprovalCard(t *testing.T) {
	f := doctorSandbox(sandbox.Availability{Detail: "sandbox-exec not found"}, "workspace", "darwin")
	if f.State != components.DoctorFailed {
		t.Fatalf("an uncontained host did not fail: %+v", f)
	}
	if !strings.Contains(f.Consequence, "⚠ UNCONTAINED") {
		t.Fatalf("the consequence is not in the card's own words: %q", f.Consequence)
	}
	if len(f.Fix) == 0 {
		t.Fatal("an uncontained host is offered no fix")
	}
}

// The fix is the one for this host, and a platform with no mechanism at all
// is told what to do instead rather than told to install something that does
// not exist for it.
func TestDoctorSandbox_TheFixIsPerHost(t *testing.T) {
	linux := doctorSandbox(sandbox.Availability{Detail: "bwrap not found"}, "workspace", "linux")
	if !strings.Contains(strings.Join(linux.Fix, "\n"), "bubblewrap") {
		t.Fatalf("linux is not told about bubblewrap: %v", linux.Fix)
	}
	darwin := doctorSandbox(sandbox.Availability{Detail: "not found"}, "workspace", "darwin")
	if !strings.Contains(strings.Join(darwin.Fix, "\n"), "sandbox-exec") {
		t.Fatalf("macOS is not told about sandbox-exec: %v", darwin.Fix)
	}
	other := doctorSandbox(sandbox.Availability{Detail: "unsupported"}, "workspace", "windows")
	fix := strings.Join(other.Fix, "\n")
	if strings.Contains(fix, "apt install") || !strings.Contains(fix, "--sandbox") {
		t.Fatalf("a platform with no mechanism is told to install one: %v", other.Fix)
	}
}

// Container sandboxes are opt-in, so a machine with no engine is `⊘ not
// checked` rather than a failure — and the row says what does need one.
func TestDoctorEngine_NoEngineIsNotAFailure(t *testing.T) {
	f := doctorEngine(sandbox.Engine{Detail: "no podman or docker on PATH"}, "", nil, 0)
	if f.State != components.DoctorSkipped {
		t.Fatalf("a missing container engine was reported as a fault: %+v", f)
	}
	if !strings.Contains(f.Consequence, "--sandbox") {
		t.Fatalf("the row does not say what needs an engine: %q", f.Consequence)
	}
}

// An engine with no image is the one worth a warning: everything is in place
// except the setting.
func TestDoctorEngine_AnEngineWithNoImageWarns(t *testing.T) {
	f := doctorEngine(sandbox.Engine{OK: true, Name: "podman", Detail: "podman ready"},
		"", errors.New("no sandbox image configured"), 0)
	if f.State != components.DoctorWarned || f.Outcome != "no image" {
		t.Fatalf("an engine with no image did not warn: %+v", f)
	}
	if strings.Count(f.Detail, "podman") > 0 {
		t.Fatalf("the detail repeats the subject: %q", f.Detail)
	}
	if len(f.Fix) != 1 || !strings.Contains(f.Fix[0], "sandbox.container_image") {
		t.Fatalf("the fix does not name the setting: %v", f.Fix)
	}
}

// A working engine states its image by a digest short enough for a row, and
// counts the containers this machine still owns.
func TestDoctorEngine_ReadyStatesItsImageAndWhatItOwns(t *testing.T) {
	image := "ghcr.io/acme/sandbox@sha256:" + strings.Repeat("a", 64)
	f := doctorEngine(sandbox.Engine{OK: true, Name: "podman", Detail: "podman ready"}, image, nil, 2)
	if f.State != components.DoctorPassed {
		t.Fatalf("a ready engine did not pass: %+v", f)
	}
	if !strings.Contains(f.Detail, "2 containers owned") {
		t.Fatalf("the row does not count what it owns: %q", f.Detail)
	}
	if strings.Contains(f.Detail, strings.Repeat("a", 64)) {
		t.Fatalf("the row carries the whole digest: %q", f.Detail)
	}
	if !strings.Contains(f.Detail, "sha256:aaaaaaaaaaaa…") {
		t.Fatalf("the row does not carry enough digest to tell two pins apart: %q", f.Detail)
	}
}

// What hangs on git is undo: outside a work tree an edit has nothing to be
// restored from, so it warns and says which key that costs.
func TestDoctorGit(t *testing.T) {
	clean := doctorGit(doctorGitState{Repo: true, Root: "/src/shhh"})
	if clean.State != components.DoctorPassed || clean.Detail != "clean" {
		t.Fatalf("a clean work tree does not say so: %+v", clean)
	}

	changed := doctorGit(doctorGitState{Repo: true, Root: "/src/shhh", Changed: 3})
	if changed.Detail != "3 files changed, all tracked" {
		t.Fatalf("a dirty tree does not count what changed: %q", changed.Detail)
	}

	untracked := doctorGit(doctorGitState{Repo: true, Root: "/src/shhh", Changed: 4, Untracked: 2})
	if untracked.Detail != "4 files changed, 2 untracked" {
		t.Fatalf("an untracked file is not counted apart: %q", untracked.Detail)
	}

	none := doctorGit(doctorGitState{Dir: "/tmp/scratch"})
	if none.State != components.DoctorWarned {
		t.Fatalf("a directory that is not a repository did not warn: %+v", none)
	}
	if !strings.Contains(none.Consequence, "undone") {
		t.Fatalf("the consequence does not name what it costs: %q", none.Consequence)
	}

	missing := doctorGit(doctorGitState{Dir: "/src/shhh", Err: errors.New("exec: \"git\": not found")})
	if missing.State != components.DoctorWarned || !strings.Contains(missing.Subject, "git did not answer") {
		t.Fatalf("a machine with no git does not say so: %+v", missing)
	}
}

// A repository the walk found but could not name falls back to where the walk
// started rather than to a blank subject.
func TestDoctorGit_NamesWhereItLookedWhenGitWouldNot(t *testing.T) {
	f := doctorGit(doctorGitState{Repo: true, Dir: "/src/shhh"})
	if !strings.Contains(f.Subject, "shhh") {
		t.Fatalf("the row names nothing: %+v", f)
	}
}

// None of the tools is required — every structural tool has a built-in
// fallback and the LSP integration is a clean no-op — so this check never
// fails. It states what is there and what is not, because "why did it not use
// ast-grep" is a question with an answer.
func TestDoctorTools(t *testing.T) {
	full := doctorTools([]string{"fd", "ast-grep"}, []string{"sd"}, []string{"gopls"})
	if full.State != components.DoctorPassed {
		t.Fatalf("a machine with tools on it did not pass: %+v", full)
	}
	if !strings.Contains(full.Subject, "ast-grep") || !strings.Contains(full.Detail, "no sd") {
		t.Fatalf("the row does not say what is and is not there: %+v", full)
	}
	if !strings.Contains(full.Detail, "gopls") {
		t.Fatalf("the row drops the language server: %q", full.Detail)
	}

	bare := doctorTools(nil, []string{"fd", "sd"}, nil)
	if bare.State != components.DoctorSkipped || bare.Outcome != "built-ins only" {
		t.Fatalf("a bare machine is reported as a fault: %+v", bare)
	}
	if !strings.Contains(bare.Detail, "no language server") {
		t.Fatalf("a bare machine does not say the servers are missing too: %q", bare.Detail)
	}
}

// A binary that is on PATH but is not the program the tool wraps is neither
// found nor plainly missing, and the row has to say which — "yq is installed
// and shhh says it is not" is otherwise a dead end for the reader.
func TestDoctorTools_SaysWhyAToolOnPathIsUnusable(t *testing.T) {
	f := doctorTools([]string{"fd"}, []string{"yq (not mikefarah's Go yq)"}, []string{"gopls"})
	if !strings.Contains(f.Detail, "not mikefarah's Go yq") {
		t.Fatalf("the row drops the reason: %q", f.Detail)
	}
	if strings.Contains(f.Subject, "yq") {
		t.Fatalf("an unusable binary must not be listed as found: %q", f.Subject)
	}
}

// Registration and the doctor ask the same question about a wrapped binary,
// so a machine the session would refuse cannot read as a machine it accepts.
func TestDoctorTools_AsksRegistrationsOwnQuestion(t *testing.T) {
	if reason := structural.UnsupportedBinary("yq", "/nonexistent/yq"); reason == "" {
		t.Fatal("a yq that cannot answer its version must not count as usable")
	}
	for _, tool := range structural.ToolBinaries() {
		if tool == "yq" {
			continue
		}
		if reason := structural.UnsupportedBinary(tool, "/nonexistent/"+tool); reason != "" {
			t.Fatalf("%s should need no probe, got %q", tool, reason)
		}
	}
}

// The server names the row states are the ones the session would actually
// start, read off the same registry.
func TestDoctorTools_NamesTheServersTheSessionWouldStart(t *testing.T) {
	names := make([]string, 0, 4)
	for _, spec := range lsp.DetectServers() {
		names = append(names, spec.Name)
	}
	f := doctorTools(nil, nil, names)
	for _, name := range names {
		if !strings.Contains(f.Detail, name) {
			t.Fatalf("the row does not name %q: %q", name, f.Detail)
		}
	}
}

// An empty memory store is the ordinary state of a new project rather than a
// fault, so it is `⊘` with the words for it.
func TestDoctorMemory(t *testing.T) {
	empty := doctorMemory("/src/shhh", 0, 0, nil)
	if empty.State != components.DoctorSkipped || empty.Outcome != "empty" {
		t.Fatalf("an empty memory store is reported as a fault: %+v", empty)
	}

	full := doctorMemory("/src/shhh", 4, 2, nil)
	if full.State != components.DoctorPassed {
		t.Fatalf("a memory store with entries did not pass: %+v", full)
	}
	if !strings.Contains(full.Detail, "4 entries for this project") || !strings.Contains(full.Detail, "2 global") {
		t.Fatalf("the row does not separate the two scopes: %q", full.Detail)
	}

	broken := doctorMemory("/src/shhh", 0, 0, errors.New("no such table"))
	if broken.State != components.DoctorWarned || broken.Consequence == "" {
		t.Fatalf("an unreadable memory store says nothing: %+v", broken)
	}
}

// The project row lists every instruction file a session here would read, in
// the order the prompt states them, and names the three it looked for when
// it found none.
func TestDoctorProject(t *testing.T) {
	empty := doctorProject(nil)
	if empty.State != components.DoctorSkipped || empty.Outcome != "empty" {
		t.Fatalf("a checkout that has said nothing is reported as a fault: %+v", empty)
	}
	for _, name := range []string{".shhh/project.md", "AGENTS.md", "CLAUDE.md"} {
		if !strings.Contains(empty.Detail, name) {
			t.Fatalf("the empty row does not name %q: %q", name, empty.Detail)
		}
	}

	loaded := doctorProject([]project.Instruction{
		{Path: "/src/shhh/AGENTS.md"},
		{Path: "/src/shhh/svc/CLAUDE.md"},
	})
	if loaded.State != components.DoctorPassed {
		t.Fatalf("a checkout with instruction files did not pass: %+v", loaded)
	}
	if !strings.Contains(loaded.Subject, "2 instruction files") {
		t.Fatalf("the row does not count what loaded: %q", loaded.Subject)
	}
	root := strings.Index(loaded.Detail, "AGENTS.md")
	nested := strings.Index(loaded.Detail, "CLAUDE.md")
	if root < 0 || nested < 0 || root > nested {
		t.Fatalf("the row does not list both files root first: %q", loaded.Detail)
	}
}

// The update row says nothing about this machine when it has nothing to
// compare, and says which of the two reasons that is.
func TestDoctorUpdate(t *testing.T) {
	dev := doctorUpdate("dev", "")
	if dev.State != components.DoctorSkipped || !strings.Contains(dev.Detail, "dev build") {
		t.Fatalf("a dev build does not explain itself: %+v", dev)
	}

	quiet := doctorUpdate("0.9.4", "")
	if quiet.State != components.DoctorSkipped {
		t.Fatalf("an unreachable release feed was reported as a fault: %+v", quiet)
	}
	if !strings.Contains(quiet.Detail, "says nothing about your install") {
		t.Fatalf("the row blames the machine for the feed: %q", quiet.Detail)
	}

	current := doctorUpdate("0.9.4", "0.9.4")
	if current.State != components.DoctorPassed {
		t.Fatalf("the latest release did not pass: %+v", current)
	}

	old := doctorUpdate("0.9.4", "0.9.5")
	if old.State != components.DoctorWarned || !strings.Contains(old.Subject, "0.9.5") {
		t.Fatalf("an out-of-date install does not name the release: %+v", old)
	}
	if len(old.Fix) == 0 {
		t.Fatal("an out-of-date install is offered no way to upgrade")
	}
}

// The duration field is blank under half a second, the same rule every
// activity row in the product follows. Most checks are a stat, so most
// of this column is deliberately empty.
func TestDoctorDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{40 * time.Millisecond, ""}, {900 * time.Millisecond, "0.9s"}, {14 * time.Second, "14s"},
	} {
		if got := doctorDuration(tc.d); got != tc.want {
			t.Fatalf("%s reads as %q, not %q", tc.d, got, tc.want)
		}
	}
}

// The text report carries the consequences and the fixes as well as the rows,
// because those are the half of the run somebody else needs in order to help.
func TestDoctorReport_CarriesTheWholeRun(t *testing.T) {
	checks := []components.DoctorCheck{
		doctorCheck("binary", doctorBinary("0.9.4", "linux", "amd64", ""), 0),
		doctorCheck("sandbox", doctorSandbox(sandbox.Availability{Detail: "bwrap not found"},
			"workspace", "linux"), 120*time.Millisecond),
		doctorCheck("engine", doctorEngine(sandbox.Engine{Detail: "none"}, "", nil, 0), 0),
	}
	report := doctorReportOf("shhh doctor", "check", "checks", checks).String()
	for _, want := range []string{
		"shhh doctor — 3 checks",
		"✓ binary", "✗ sandbox", "⊘ engine",
		"⚠ UNCONTAINED",
		"sudo apt install bubblewrap",
		"1 failed · 1 passed · 1 not checked",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("the report does not carry %q:\n%s", want, report)
		}
	}
}

// The report a pipe gets and the report `[c]` copies are the same text, which
// is why a run can be pasted into an issue whichever way it was read.
func TestDoctorReport_IsWhatTheSurfaceCopies(t *testing.T) {
	m := newDoctorModel(config.Config{}, containmentProbes())
	if got := m.report(); !strings.Contains(got, "shhh doctor — 2 checks") {
		t.Fatalf("the copied report is not the run: %s", got)
	}
}

// A run that has not started says what each check is going to look at: a
// queued row that said nothing would be a row the reader cannot read.
func TestNewDoctorModel_QueuesEveryCheckWithSomethingToSay(t *testing.T) {
	m := newDoctorModel(config.Config{}, doctorProbes())
	if !m.screen.Running {
		t.Fatal("a run that has not started does not say it is running")
	}
	for _, check := range m.screen.Checks {
		if check.State != components.DoctorQueued {
			t.Fatalf("check %q did not start queued", check.Name)
		}
		if check.Subject == "" || check.Subject == check.Name {
			t.Fatalf("queued check %q says nothing about itself", check.Name)
		}
		if check.Duration != components.NoDuration {
			t.Fatalf("queued check %q claims a duration: %q", check.Name, check.Duration)
		}
	}
}

// The summary the surface states and the summary the text report states are
// the same count.
func TestDoctorSummaryLine(t *testing.T) {
	checks := []components.DoctorCheck{
		{State: components.DoctorFailed}, {State: components.DoctorWarned},
		{State: components.DoctorPassed}, {State: components.DoctorSkipped},
	}
	if got := doctorSummaryLine(checks); got != "1 failed · 1 warning · 1 passed · 1 not checked" {
		t.Fatalf("the summary reads %q", got)
	}
	if got := doctorSummaryLine(nil); got != "no checks to run" {
		t.Fatalf("an empty run summarises as %q", got)
	}
}

// A whole run answers without a provider key, a container engine or a git
// repository, and every check comes back with something to say.
func TestRunDoctorChecks_EveryCheckAnswers(t *testing.T) {
	was := updateCheck
	updateCheck = func(string) string { return "" }
	t.Cleanup(func() { updateCheck = was })

	checks := runDoctorChecks(t.Context(), config.Config{}, doctorProbes())
	if len(checks) != len(doctorProbes()) {
		t.Fatalf("%d checks answered out of %d", len(checks), len(doctorProbes()))
	}
	for _, check := range checks {
		if check.Subject == "" {
			t.Fatalf("check %q answered with nothing", check.Name)
		}
		if check.Outcome == "" {
			t.Fatalf("check %q answered with no outcome", check.Name)
		}
		if check.State == components.DoctorQueued || check.State == components.DoctorRunning {
			t.Fatalf("check %q never resolved: %+v", check.Name, check)
		}
		// A check that went wrong names the fix. A `⊘` is not a
		// check that went wrong — it is one with nothing to look at — so
		// only the two that ask something of the reader are held to it.
		switch check.State {
		case components.DoctorFailed, components.DoctorWarned:
			if check.Consequence == "" && len(check.Fix) == 0 {
				t.Fatalf("check %q went wrong and names neither a consequence nor a fix: %+v",
					check.Name, check)
			}
		}
	}
}

// The run walks the checks one at a time: each answer stamps its row, marks
// the next one running, and asks for it. That is what makes the artboard's
// `▸ running` / `· queued` picture something the reader can actually watch.
func TestDoctorModel_WalksTheChecksOneAtATime(t *testing.T) {
	m := newDoctorModel(config.Config{}, doctorProbes())
	m.markRunning(0)
	if m.screen.Checks[0].State != components.DoctorRunning {
		t.Fatal("the first check did not start running")
	}

	next, cmd := m.Update(doctorDoneMsg{at: 0, finding: doctorBinary("0.9.4", "linux", "amd64", ""), took: 0})
	m = next.(doctorModel)
	if cmd == nil {
		t.Fatal("the run stopped after the first check answered")
	}
	if m.screen.Checks[0].State != components.DoctorPassed {
		t.Fatalf("the answered check did not take its answer: %+v", m.screen.Checks[0])
	}
	if m.screen.Checks[1].State != components.DoctorRunning {
		t.Fatalf("the next check is not running: %+v", m.screen.Checks[1])
	}
	if m.screen.Checks[1].Duration != "" {
		t.Fatalf("a running check kept its em-dash duration: %q", m.screen.Checks[1].Duration)
	}
	if m.screen.Checks[2].State != components.DoctorQueued {
		t.Fatalf("a check after the running one is not queued: %+v", m.screen.Checks[2])
	}
	if !m.screen.Running {
		t.Fatal("the run says it has finished with checks still queued")
	}
}

// The last answer stops the run, which is what puts `[r]` on offer and stops
// the spinner: a frame still turning over a finished run would say the screen
// is doing something.
func TestDoctorModel_TheLastAnswerStopsTheRun(t *testing.T) {
	m := newDoctorModel(config.Config{}, containmentProbes())
	m.markRunning(0)
	for at := range containmentProbes() {
		next, _ := m.Update(doctorDoneMsg{at: at,
			finding: doctorSandbox(sandbox.Availability{OK: true, Mechanism: "bwrap"}, "workspace", "linux")})
		m = next.(doctorModel)
	}
	if m.screen.Running {
		t.Fatal("the run is still going after every check answered")
	}
	if _, cmd := m.Update(doctorTickMsg(time.Now())); cmd != nil {
		t.Fatal("the spinner is still ticking over a finished run")
	}
}

// `[r]` puts every row back to queued and starts again, so a fix applied in
// another terminal can be checked without leaving this one.
func TestDoctorModel_RerunStartsOver(t *testing.T) {
	m := newDoctorModel(config.Config{}, containmentProbes())
	m.markRunning(0)
	next, _ := m.Update(doctorDoneMsg{at: 0,
		finding: doctorSandbox(sandbox.Availability{OK: true, Mechanism: "bwrap"}, "workspace", "linux")})
	next, _ = next.(doctorModel).Update(doctorDoneMsg{at: 1,
		finding: doctorEngine(sandbox.Engine{Detail: "none"}, "", nil, 0)})
	m = next.(doctorModel)

	again, cmd := m.apply(components.DoctorCommand{Act: components.DoctorRerun})
	fresh := again.(doctorModel)
	if cmd == nil {
		t.Fatal("[r] asked for nothing")
	}
	if !fresh.screen.Running || fresh.screen.Checks[1].State != components.DoctorQueued {
		t.Fatalf("[r] did not start over: %+v", fresh.screen.Checks)
	}
	if fresh.screen.Checks[0].State != components.DoctorRunning {
		t.Fatalf("[r] did not start the first check: %+v", fresh.screen.Checks[0])
	}
}

// The migration row. Nothing is broken when one is pending — shhh starts and
// runs — so it is a warning, and the consequence line is where "running
// without whatever is in the old place" gets said.
func TestDoctorMigrate(t *testing.T) {
	none := doctorMigrate(nil)
	if none.State != components.DoctorPassed || none.Action != "" {
		t.Fatalf("a machine with nothing to migrate is not a clean pass: %+v", none)
	}

	pending := doctorMigrate([]migrate.Pending{{
		Name:        "the old directories",
		Summary:     "~/Library/… · 3 entries to move",
		Consequence: "shhh is reading none of it",
		Steps:       []string{"a  →  b"},
		Apply:       func() ([]string, error) { return []string{"moved a to b"}, nil },
	}})
	if pending.State != components.DoctorWarned || pending.Outcome != "pending" {
		t.Fatalf("a pending migration does not read as a warning: %+v", pending)
	}
	if pending.Action == "" || pending.ActionPrompt == "" || pending.Apply == nil {
		t.Fatalf("a migration shhh can make offers no way to make it: %+v", pending)
	}
	if !strings.Contains(strings.Join(pending.Fix, "\n"), "a  →  b") {
		t.Fatalf("the fix does not say what would move: %+v", pending.Fix)
	}
}

// A migration shhh will not make itself is still reported — the reader has to
// know it is due — but it offers no key, because an offer that cannot be
// honoured is worse than none (invariant 5).
func TestDoctorMigrate_OffersNoKeyForAMigrationItCannotMake(t *testing.T) {
	f := doctorMigrate([]migrate.Pending{{
		Name:        "two files that both claim to be the config",
		Summary:     "1 conflict to settle",
		Consequence: "shhh is reading the new one",
		Steps:       []string{"pick one"},
	}})
	if f.Action != "" || f.Apply != nil {
		t.Fatalf("a migration shhh cannot make offered to make it: %+v", f)
	}
	if !strings.Contains(strings.Join(f.Fix, "\n"), "cannot make this one for you") {
		t.Fatalf("the reader is left waiting for a key that never comes: %+v", f.Fix)
	}
}

// Applying stops at the first failure and keeps what it already did, so the
// notice the surface shows can say how far it got.
func TestApplyMigrations_KeepsWhatItDidBeforeFailing(t *testing.T) {
	lines, err := applyMigrations([]migrate.Pending{
		{Apply: func() ([]string, error) { return []string{"one"}, nil }},
		{Apply: func() ([]string, error) { return []string{"two"}, errors.New("no room") }},
		{Apply: func() ([]string, error) { return []string{"three"}, nil }},
	})
	if err == nil {
		t.Fatal("a migration that failed was reported as done")
	}
	if strings.Join(lines, ",") != "one,two" {
		t.Fatalf("the changes made before the failure were not reported: %v", lines)
	}
}

// The action has to survive the trip to the screen. The screen draws `[a]`
// from these two fields, so a check that dropped them would report a pending
// migration and offer no way to make it.
func TestDoctorCheck_CarriesTheActionToTheScreen(t *testing.T) {
	check := doctorCheck("migrate", doctorFinding{
		Subject: "1 migration pending", Action: "make the change",
		ActionPrompt: "Make the change now?",
	}, 0)
	if check.Action != "make the change" || check.ActionPrompt != "Make the change now?" {
		t.Fatalf("the action did not reach the screen: %+v", check)
	}
}

// What an applied action did is said once, at the foot, and the run behind it
// is started again — the answer to "did that work" is the report itself.
func TestDoctorModel_AppliedSaysWhatChangedAndRerunsTheChecks(t *testing.T) {
	m := newDoctorModel(config.Config{}, []doctorProbe{{name: "binary", run: probeBinary}})
	m.screen.Checks[0].State = components.DoctorPassed

	next, cmd := m.applied(doctorAppliedMsg{lines: []string{"moved a to b", "moved c to d"}})
	fresh, ok := next.(doctorModel)
	if !ok {
		t.Fatalf("applied returned %T", next)
	}
	if !strings.Contains(fresh.screen.Notice, "2 changes made") {
		t.Fatalf("the notice does not say what changed: %q", fresh.screen.Notice)
	}
	if !fresh.screen.Running || cmd == nil {
		t.Fatal("the checks were not started again after the change")
	}

	stopped, _ := m.applied(doctorAppliedMsg{lines: []string{"moved a to b"}, err: errors.New("no room")})
	notice := stopped.(doctorModel).screen.Notice
	if !strings.Contains(notice, "1 change made") || !strings.Contains(notice, "no room") {
		t.Fatalf("a partial run does not say how far it got: %q", notice)
	}
}

// A file holding a key rather than naming one is a warning worded as what it
// costs. "api_key is deprecated" sends the reader looking for the
// replacement; "this file is a copy of your key" is the fact that makes them
// want one, and the fix under it is the two commands.
func TestDoctorConfig_WarnsOnAKeyTheFileHolds(t *testing.T) {
	t.Setenv("SHHH_PROVIDER", "")
	held := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{
		Provider: config.ProviderConfig{Default: "anthropic", APIKey: "sk-ant-in-the-file"},
	}, config.Project{}, nil)
	if held.State != components.DoctorWarned || held.Outcome != "key in the file" {
		t.Fatalf("a file holding a key passed: %+v", held)
	}
	if !strings.Contains(held.Consequence, "this file is a copy of your key") {
		t.Fatalf("the row does not say what it costs: %+v", held)
	}
	if !strings.Contains(held.Detail, "provider.api_key holds the key itself") {
		t.Fatalf("the row does not name the key: %+v", held)
	}
	fix := strings.Join(held.Fix, "\n")
	if !strings.Contains(fix, "ANTHROPIC_API_KEY") || !strings.Contains(fix, "provider.api_key_env") {
		t.Fatalf("the fix is not the two commands: %+v", held)
	}
	if strings.Contains(held.Detail+held.Consequence+fix, "sk-ant-in-the-file") {
		t.Fatalf("the row echoed the key: %+v", held)
	}

	named := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{
		Provider: config.ProviderConfig{Default: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
	}, config.Project{}, nil)
	if named.State != components.DoctorPassed || named.Outcome != "ok" {
		t.Fatalf("a file that names a variable was warned about: %+v", named)
	}

	search := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{
		Web: config.WebConfig{SearchAPIKey: "brave-in-the-file"},
	}, config.Project{}, nil)
	if search.State != components.DoctorWarned || !strings.Contains(search.Detail, "web.search_api_key") {
		t.Fatalf("the second credential is not warned about the same way: %+v", search)
	}
}

// Two credentials held at once are one row, and the sentence agrees with how
// many there are: a row reading "provider.api_key hold the key itself" is a
// row the reader stops trusting about the rest of what it says.
func TestDoctorConfig_TwoHeldKeysAreOneRowThatReads(t *testing.T) {
	t.Setenv("SHHH_PROVIDER", "")
	both := doctorConfig("/home/u/.config/shhh/config.toml", nil, config.Config{
		Provider: config.ProviderConfig{Default: "anthropic", APIKey: "sk-ant-one"},
		Web:      config.WebConfig{SearchAPIKey: "brave-two"},
	}, config.Project{}, nil)
	if !strings.Contains(both.Detail, "provider.api_key and web.search_api_key hold the key itself") {
		t.Fatalf("two keys do not read as two: %+v", both)
	}
	fix := strings.Join(both.Fix, "\n")
	if !strings.Contains(fix, "shhh config set web.search_api_key_env ") {
		t.Fatalf("the fix does not name the second key's companion: %+v", both)
	}
}

// The row a reader looks at to answer "are my sessions leaving this
// machine". Off is not a fault — it is what almost every machine is — and an
// endpoint that cannot be sent to is a warning rather than a failure,
// because nothing about the session stops working.
func TestDoctorOtel(t *testing.T) {
	off := doctorOtel("  ")
	if off.State != components.DoctorSkipped {
		t.Fatalf("no endpoint should not be a fault: %+v", off)
	}
	if !strings.Contains(off.Subject, "stays on this machine") {
		t.Fatalf("the row does not say where the record is: %q", off.Subject)
	}

	on := doctorOtel("http://localhost:4318")
	if on.State != components.DoctorPassed {
		t.Fatalf("a usable endpoint did not pass: %+v", on)
	}
	if !strings.Contains(on.Subject, "http://localhost:4318") {
		t.Fatalf("the row does not name the endpoint: %q", on.Subject)
	}

	bad := doctorOtel("localhost:4318")
	if bad.State != components.DoctorWarned {
		t.Fatalf("an endpoint with no scheme did not warn: %+v", bad)
	}
	if !strings.Contains(bad.Consequence, "recorded locally") {
		t.Fatalf("the consequence does not say what still works: %q", bad.Consequence)
	}
}

// The probe reads the config and nothing else, so the row is the same on a
// machine with a collector and on one without.
func TestProbeOtel_ReadsTheConfiguredEndpoint(t *testing.T) {
	var cfg config.Config
	if f := probeOtel(t.Context(), cfg); f.State != components.DoctorSkipped {
		t.Fatalf("an empty config is export off: %+v", f)
	}
	cfg.Otel.Endpoint = "https://otel.example:4318"
	if f := probeOtel(t.Context(), cfg); !strings.Contains(f.Subject, "otel.example") {
		t.Fatalf("the probe did not read the endpoint: %+v", f)
	}
}
