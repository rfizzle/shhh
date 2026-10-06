package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// declaredCheckout is a trusted checkout declaring two tools and their
// install lines, with an `apk add` line the host is never offered, under a
// home and a cache of the test's own. It hands back the toolchain directory
// shhh installs into, and leaves the captured commands' PATH as it found it.
func declaredCheckout(t *testing.T, hosts string) (root, dir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	root = t.TempDir()
	decl := "install = [\n" +
		"  \"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0\",\n" +
		"  \"apk add shellcheck=0.10.0-r0\",\n" +
		"  \"go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4\",\n" +
		"]\n" + hosts + "check = [\"golangci-lint\", \"gosec\", \"sh\"]\n"
	path := filepath.Join(root, filepath.FromSlash(project.ToolchainFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(decl), 0o644); err != nil {
		t.Fatal(err)
	}
	withProjectTrust(t, project.Trust{Root: root, Granted: true})
	t.Cleanup(func() { runner.SetPathAfter("") })
	dir, err := sandbox.ToolchainDir()
	if err != nil {
		t.Fatal(err)
	}
	return root, dir
}

// What is missing is judged against the PATH a command is handed, which ends
// in shhh's own toolchain directory: a tool an earlier session installed
// there is present, and one nowhere on it is named — to the headless run as
// well, which is told it cannot be offered the install.
func TestAMissingDeclaredToolIsReadAgainstTheContainedPath(t *testing.T) {
	_, dir := declaredCheckout(t, "")
	t.Setenv("PATH", "/usr/bin:/bin")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gosec"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := openToolchain()
	if !r.declared || r.bin != bin {
		t.Fatalf("reading = %+v", r)
	}
	if !slices.Equal(r.missing, []string{"golangci-lint"}) {
		t.Fatalf("missing = %v, want golangci-lint alone", r.missing)
	}
	if !strings.HasSuffix(runner.PathValue(), string(os.PathListSeparator)+bin) {
		t.Fatalf("a captured command's PATH does not end in shhh's own: %q", runner.PathValue())
	}
	if os.Getenv("PATH") != "/usr/bin:/bin" {
		t.Fatalf("shhh's own PATH moved: %q", os.Getenv("PATH"))
	}
	note := toolchainStartupNote(r)
	for _, want := range []string{"golangci-lint", "/setup", "a run with nobody to ask does not"} {
		if !strings.Contains(note, want) {
			t.Errorf("the startup note never says %q: %q", want, note)
		}
	}
}

// The model is told only where something is missing, names what, and is
// told whether a person was offered the install — so it asks one where one
// is there, and neither asks nor installs where nobody is.
func TestTheModelIsToldOnlyWhereSomethingIsMissing(t *testing.T) {
	if b := toolchainPromptBlock(nil, true); b != "" {
		t.Fatalf("nothing missing wrote a paragraph: %q", b)
	}
	offered := toolchainPromptBlock([]string{"golangci-lint", "gosec"}, true)
	for _, want := range []string{"golangci-lint and gosec", "The user has been offered an install", "Do not install them yourself", "ask the user to install it"} {
		if !strings.Contains(offered, want) {
			t.Errorf("the session's paragraph never says %q: %q", want, offered)
		}
	}
	alone := toolchainPromptBlock([]string{"gosec"}, false)
	for _, want := range []string{"names gosec", "Nobody here can install it", "must not install it yourself"} {
		if !strings.Contains(alone, want) {
			t.Errorf("the unattended paragraph never says %q: %q", want, alone)
		}
	}
	if strings.Contains(alone, "offered") {
		t.Errorf("a run with nobody to ask was told a person was offered the install: %q", alone)
	}
}

// The install runs under the session's own wall, narrowed: the workspace
// read-only, no directory of the working scope, starting in shhh's own
// directory, the installers pointed at its bin rather than ~/go/bin, and the
// declaration's hosts as the host list.
func TestTheInstallRunsUnderTheSessionsWallNarrowed(t *testing.T) {
	_, dir := declaredCheckout(t, "hosts = [\"proxy.golang.org\", \"sum.golang.org\"]\n")
	r := openToolchain()
	runner.SetSessionEnv([]string{"DEPLOY_KEY=hunter2"})
	t.Cleanup(func() { runner.SetSessionEnv(nil) })
	cfg := config.Config{}
	cfg.Sandbox.WriteExtra = []string{"/srv/granted-by-config"}
	p, err := installPolicy(cfg, r)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadOnlyWorkspace || p.Cwd != dir {
		t.Fatalf("policy = %+v", p)
	}
	if !slices.Equal(p.AllowHosts, []string{"proxy.golang.org", "sum.golang.org"}) {
		t.Fatalf("host list = %v, want the declaration's", p.AllowHosts)
	}
	if slices.Contains(p.SecretNames, "DEPLOY_KEY") {
		t.Fatalf("the install is handed the session's secrets: %v", p.SecretNames)
	}
	if !slices.Equal(p.WriteExtra, cfg.Sandbox.WriteExtra) {
		t.Fatalf("write grants = %v, want the configured ones and no scope", p.WriteExtra)
	}
	bin := filepath.Join(dir, "bin")
	if !slices.Contains(p.Env, "GOBIN="+bin) || !slices.Contains(p.SecretNames, "GOBIN") {
		t.Fatalf("GOBIN does not reach the contained command: %v / %v", p.Env, p.SecretNames)
	}
	for _, pair := range p.Env {
		if strings.HasPrefix(pair, "GOBIN=") && pair != "GOBIN="+bin {
			t.Fatalf("an install is pointed somewhere else: %s", pair)
		}
	}
	if !strings.HasPrefix(bin, mustCacheDir(t)) {
		t.Fatalf("%s is not under the cache grant %s", bin, mustCacheDir(t))
	}
}

func mustCacheDir(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

// Where a mechanism holds, the line is the mechanism's argv over that
// policy: the installers' variables set inside, the command started in
// shhh's own directory, and the checkout not bound writable. This host may
// have no mechanism at all, so the argv is what is tested, not a run.
func TestAContainedInstallIsTheWrapOfTheNarrowedPolicy(t *testing.T) {
	_, dir := declaredCheckout(t, "")
	r := openToolchain()
	line := r.tc.HostInstall()[0]
	// installLine makes the directory before it asks for the argv, because
	// the mechanism resolves the directory it starts the line in.
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	argv, err := installArgv(r, config.Config{}, sandbox.Availability{OK: true, Mechanism: "bwrap"}, line)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	ws, _ := os.Getwd()
	// The mechanism starts the line in the directory as resolved, and a
	// temporary directory is behind a link on macOS (/var is /private/var).
	start, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--setenv GOBIN " + filepath.Join(dir, "bin"), "--chdir " + start, line} {
		if !strings.Contains(joined, want) {
			t.Errorf("the wrap never carries %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "--bind "+ws+" "+ws) {
		t.Errorf("the checkout is bound writable under the install:\n%s", joined)
	}
}

// With no mechanism and nothing requiring one the line runs as the session's
// own commands do, with the installers' variables in front of it; where one
// is required and none is, nothing runs and nothing is created.
func TestAnInstallRunsAsContainedAsTheSessionsCommands(t *testing.T) {
	_, dir := declaredCheckout(t, "")
	r := openToolchain()
	line := r.tc.HostInstall()[0]
	argv, err := installArgv(r, config.Config{}, noMechanism, line)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "env" || !slices.Contains(argv, "GOBIN="+filepath.Join(dir, "bin")) || argv[len(argv)-1] != line {
		t.Fatalf("bare argv = %v", argv)
	}
	got := installLine(context.Background(), r, config.Config{}, noMechanism, "containment is required and none is here", line)
	if got.Outcome != tools.ExecDidNotStart || !strings.Contains(got.Output, "containment is required") {
		t.Fatalf("a refused install = %+v", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a refused install created %s: %v", dir, err)
	}
}

// The card is handed the host's lines, the declaration's hosts only where the
// mechanism holds a list, and the refusal where there is one — which is what
// keeps it from offering a yes that could not be answered.
func TestTheCardIsHandedWhatTheInstallWillDo(t *testing.T) {
	_, _ = declaredCheckout(t, "hosts = [\"proxy.golang.org\"]\n")
	r := openToolchain()
	held := toolchainCard(r, config.Config{}, sandbox.Availability{OK: true, Mechanism: "bwrap"}, "")
	if len(held.Lines) != 2 || strings.Contains(strings.Join(held.Lines, "\n"), "apk add") {
		t.Fatalf("lines = %v", held.Lines)
	}
	if !slices.Equal(held.Hosts, []string{"proxy.golang.org"}) || !held.Network || !held.Runnable() {
		t.Fatalf("held = %+v", held)
	}
	if unheld := toolchainCard(r, config.Config{}, standInMechanism, ""); len(unheld.Hosts) != 0 {
		t.Fatalf("a mechanism that holds no list named hosts: %v", unheld.Hosts)
	}
	if refused := toolchainCard(r, config.Config{}, noMechanism, "required"); refused.Runnable() {
		t.Fatal("a session that may not run the install was offered it")
	}
	if empty := toolchainCard(toolchainReading{}, config.Config{}, noMechanism, ""); empty.Install != nil || len(empty.Declared) != 0 {
		t.Fatalf("a checkout that declares nothing was offered something: %+v", empty)
	}
}

// A session reads the declaration again when something in this process
// changes what it would find — the checkout trusted, a declaration written —
// so the card is handed the way to read it even where nothing was declared
// yet, and each of those acts moves the count it is read again at.
func TestTheSessionReadsTheDeclarationAgainOnceItMoves(t *testing.T) {
	root, _ := declaredCheckout(t, "")
	answer := project.Trust{Root: root}
	withProjectTrust(t, project.Trust{})
	projectTrust = func() project.Trust { return answer }

	tc := toolchainCard(openToolchain(), config.Config{}, noMechanism, "")
	if len(tc.Declared) != 0 || tc.Reread == nil || tc.Moved == nil {
		t.Fatalf("an untrusted checkout's card = %+v", tc)
	}
	at := tc.Moved()
	answer.Granted = true
	forgetProjectTrust()
	if tc.Moved() != at+1 {
		t.Fatalf("recording the trust answer did not move the count: %d → %d", at, tc.Moved())
	}
	if fresh := tc.Reread(); !slices.Equal(fresh.Declared, []string{"golangci-lint", "gosec", "sh"}) || len(fresh.Lines) != 2 {
		t.Fatalf("the reading after trust = %+v", fresh)
	}
	if _, err := writeToolchainDraft(root)(project.Toolchain{Check: []string{"gosec"}}.Render()); err != nil {
		t.Fatal(err)
	}
	if tc.Moved() != at+2 {
		t.Fatalf("writing a declaration did not move the count: %d → %d", at, tc.Moved())
	}
	if fresh := tc.Reread(); !slices.Equal(fresh.Declared, []string{"gosec"}) {
		t.Fatalf("the reading after the write = %+v", fresh.Declared)
	}
}

// A process the session starts is handed the PATH a captured command is,
// so a tool the declaration installed is found by a server started bare as
// it is by a command.
func TestAProcessStartFindsWhatTheToolchainInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	_, dir := declaredCheckout(t, "")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "shhh-declared-tool"), []byte("#!/bin/sh\necho declared-tool-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	openToolchain()
	sup := openProcessSupervisor(nil)
	if sup == nil {
		t.Fatal("no supervisor")
	}
	t.Cleanup(func() {
		sup.Close()
		runner.SetAdopter(nil)
	})
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"tool","command":"shhh-declared-tool"}`)); err != nil {
		t.Fatal(err)
	}
	var out string
	var err error
	if !eventually(func() bool {
		out, err = sup.Execute(json.RawMessage(`{"action":"read","name":"tool"}`))
		return err == nil && strings.Contains(out, "declared-tool-ran")
	}) {
		t.Fatalf("a started process did not find what the toolchain installed: %q %v", out, err)
	}
}

// The doctor's row: nothing declared is nothing to check, a withheld
// declaration is the trust row's, a broken one fails, and a missing tool is
// a warning naming the one way it is installed.
func TestTheDoctorReadsTheDeclaration(t *testing.T) {
	if f := doctorToolchain(toolchainReading{}, false); f.State != components.DoctorSkipped || f.Outcome != "empty" {
		t.Errorf("nothing declared: %+v", f)
	}
	if f := doctorToolchain(toolchainReading{}, true); f.Outcome != "untrusted" {
		t.Errorf("withheld: %+v", f)
	}
	if f := doctorToolchain(toolchainReading{err: os.ErrPermission}, false); f.State != components.DoctorFailed {
		t.Errorf("broken: %+v", f)
	}
	present := toolchainReading{declared: true, tc: project.Toolchain{Check: []string{"gosec"}}}
	if f := doctorToolchain(present, false); f.Outcome != "ok" {
		t.Errorf("present: %+v", f)
	}
	missing := toolchainReading{declared: true, bin: "/c/shhh/toolchain/bin", missing: []string{"gosec"},
		tc: project.Toolchain{Check: []string{"gosec"}, Install: []string{"go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4"}}}
	f := doctorToolchain(missing, false)
	if f.State != components.DoctorWarned || f.Detail != "gosec" || len(f.Fix) != 2 || !strings.HasPrefix(f.Fix[0], "/setup") || !strings.Contains(f.Fix[1], "/c/shhh/toolchain/bin") {
		t.Errorf("missing: %+v", f)
	}
}
