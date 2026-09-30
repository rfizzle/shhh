package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitRun runs git in dir for a fixture and fails the test on an error. The
// home is the test's own (testHome), so no global configuration reaches it.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// gitWorkspace is a workspace that is a repository, resolved the way the
// spec spells paths.
func gitWorkspace(t *testing.T) (Policy, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	policy, ws := workspacePolicy(t)
	gitRun(t, ws, "init", "-q")
	return policy, resolvedPath(t, ws)
}

// argvIndex is where a flag and its paths first appear in an argv, or -1.
func argvIndex(argv []string, seq ...string) int {
	for i := 0; i+len(seq) <= len(argv); i++ {
		if slices.Equal(argv[i:i+len(seq)], seq) {
			return i
		}
	}
	return -1
}

// The repository's program paths are read-only to a contained command and
// the rest of the store is not, assembled after the grants the way the
// credential stores are: a hook, the config and info/ bound read-only over
// the workspace's writable bind, .git bound over itself before them so it
// cannot be renamed aside, and commondir — absent in an ordinary checkout —
// named as the one bubblewrap cannot hold rather than claimed.
func TestResolveMakesTheRepositorysProgramsReadOnlyUnderBubblewrap(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(dotgit, "config"), filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "info"),
	} {
		if !slices.Contains(s.gitReadOnly, want) {
			t.Errorf("%s must be read-only to a contained command, gitReadOnly=%v", want, s.gitReadOnly)
		}
	}
	if slices.ContainsFunc(s.gitReadOnly, func(p string) bool { return within(p, filepath.Join(dotgit, "objects")) }) {
		t.Errorf("the objects stay writable — staging is ordinary work: %v", s.gitReadOnly)
	}
	if !slices.Contains(s.gitPinned, dotgit) {
		t.Errorf(".git must be pinned so it cannot be renamed aside, gitPinned=%v", s.gitPinned)
	}
	if !slices.Contains(s.gitUnheld, filepath.Join(dotgit, "commondir")) {
		t.Errorf("an absent commondir is what bubblewrap cannot hold, and the spec must say so: %v", s.gitUnheld)
	}

	argv := bwrapPrefix(s)
	grant := argvIndex(argv, "--bind", ws, ws)
	pin := argvIndex(argv, "--bind", dotgit, dotgit)
	hooks := argvIndex(argv, "--ro-bind", filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "hooks"))
	if grant < 0 || pin < 0 || hooks < 0 {
		t.Fatalf("the grant, the pin and the read-only hooks must all be in the argv:\n%v", argv)
	}
	if grant >= pin || pin >= hooks {
		t.Errorf("the order must be grant, pin, read-only bind (got %d, %d, %d):\n%v", grant, pin, hooks, argv)
	}
}

// Seatbelt's rules are about names, so every entry goes in — the absent
// commondir too — and after the write allowances, which is what makes the
// deny the rule that holds inside the workspace.
func TestResolveMakesTheRepositorysProgramsReadOnlyUnderSeatbelt(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")

	s, err := resolvePolicy(policy, "sandbox-exec")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.gitUnheld) != 0 {
		t.Errorf("Seatbelt holds every entry by name: unheld=%v", s.gitUnheld)
	}
	profile := seatbeltProfile(s)
	allow := strings.Index(profile, "(allow file-write*\n  (subpath "+sbplQuote(ws)+")")
	commondir := strings.Index(profile, "(subpath "+sbplQuote(filepath.Join(dotgit, "commondir"))+")")
	hooks := strings.Index(profile, "(subpath "+sbplQuote(filepath.Join(dotgit, "hooks"))+")")
	// .git by its literal name: moving it aside, writing a hook and moving
	// it back is refused, and a lock file inside it is not.
	pin := strings.Index(profile, "(literal "+sbplQuote(dotgit)+")")
	if allow < 0 || commondir < 0 || hooks < 0 || pin < 0 {
		t.Fatalf("the workspace allowance, both denies and the pin must be in the profile:\n%s", profile)
	}
	if strings.Contains(profile, "(subpath "+sbplQuote(dotgit)+")") {
		t.Errorf("the store itself stays writable — only its program paths are denied:\n%s", profile)
	}
	if allow > hooks || allow > commondir || allow > pin {
		t.Errorf("the deny must come after the allowance it narrows:\n%s", profile)
	}
}

// A grant of the store is the person asking for contained commands to write
// there, so it unmasks every entry — the grant is the store, not the file in
// it. And where nothing is wrapping the command, nothing is claimed.
func TestResolveUnmasksTheRepositoryWhenTheStoreIsGranted(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)

	policy.WriteExtra = []string{filepath.Join(ws, ".git")}
	for _, mech := range []string{"bwrap", "sandbox-exec"} {
		s, err := resolvePolicy(policy, mech)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.gitReadOnly)+len(s.gitPinned)+len(s.gitUnheld) != 0 {
			t.Errorf("%s: a granted store stays masked: %v %v %v", mech, s.gitReadOnly, s.gitPinned, s.gitUnheld)
		}
	}

	policy.WriteExtra = nil
	s, err := resolvePolicy(policy, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.gitReadOnly) != 0 {
		t.Errorf("with no mechanism nothing is masked, and nothing may be claimed: %v", s.gitReadOnly)
	}
	if words := GitStoreWords(Availability{}, policy); words != "" {
		t.Errorf("an unavailable mechanism reports no mask, got %q", words)
	}
	if words := GitStoreWords(Availability{OK: true, Mechanism: "sandbox-exec"}, policy); words != gitStoreWords {
		t.Errorf("a masked store reports %q, got %q", gitStoreWords, words)
	}
}

// core.hooksPath moves the hooks, so the mask follows it into the working
// tree; and a linked worktree names its store in a .git file, which is the
// entry that redirects it.
func TestResolveFollowsTheHooksPathAndALinkedWorktreesPointer(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)
	husky := mkdir(t, filepath.Join(ws, ".husky"))
	gitRun(t, ws, "config", "core.hooksPath", ".husky")

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.gitReadOnly, resolvedPath(t, husky)) {
		t.Errorf("the hooks directory core.hooksPath names must be read-only: %v", s.gitReadOnly)
	}

	gitRun(t, ws, "commit", "-q", "--allow-empty", "-m", "seed")
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, ws, "worktree", "add", "-q", "--detach", linked)
	policy.Workspace = linked
	s, err = resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.gitReadOnly, filepath.Join(resolvedPath(t, linked), ".git")) {
		t.Errorf("a linked worktree's .git file names its store and must be read-only: %v", s.gitReadOnly)
	}
}

// A workspace below the checkout's top is where git looks first, so a .git
// made there would be taken as the repository.
func TestResolveKeepsAShadowRepositoryOutOfASubdirectoryWorkspace(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)
	sub := mkdir(t, filepath.Join(ws, "sub"))
	policy.Workspace = sub

	s, err := resolvePolicy(policy, "sandbox-exec")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.gitReadOnly, filepath.Join(sub, ".git")) {
		t.Errorf("a .git in a subdirectory workspace must not be writable: %v", s.gitReadOnly)
	}
	if slices.Contains(s.gitReadOnly, filepath.Join(ws, ".git", "config")) {
		t.Errorf("the store above the workspace is outside every grant and needs no rule: %v", s.gitReadOnly)
	}
}

// A contained command can break the store for one command — a HEAD that is
// not a ref makes git answer "not a repository" — and a mask that fell open
// on that would let the next command write the config and put HEAD back. The
// store's ordinary layout is the floor git's answer is added to.
func TestResolveKeepsTheMaskWhenGitCannotReadTheStore(t *testing.T) {
	testHome(t)
	stubHostTemp(t)
	policy, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")
	if err := os.WriteFile(filepath.Join(dotgit, "HEAD"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, asked := askGit(ws); asked {
		t.Fatal("the fixture must leave git unable to answer, or this proves nothing")
	}
	for _, mech := range []string{"bwrap", "sandbox-exec"} {
		s, err := resolvePolicy(policy, mech)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{filepath.Join(dotgit, "config"), filepath.Join(dotgit, "hooks")} {
			if !slices.Contains(s.gitReadOnly, want) {
				t.Errorf("%s: %s must stay read-only when git cannot read the store: %v", mech, want, s.gitReadOnly)
			}
		}
	}
}

// A wider grant above the workspace is a directory the workspace could be
// renamed out of, so the pins run up to it; and a dangling link at an entry
// is not something bubblewrap can bind, so it is named as not held rather
// than failing every wrap.
func TestResolvePinsUpToTheWidestGrantAndSkipsADanglingLink(t *testing.T) {
	home := testHome(t)
	stubHostTemp(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	parent := resolvedPath(t, mkdir(t, filepath.Join(home, "proj")))
	ws := mkdir(t, filepath.Join(parent, "app"))
	gitRun(t, ws, "init", "-q")
	info := filepath.Join(ws, ".git", "info")
	if err := os.RemoveAll(info); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "nowhere"), info); err != nil {
		t.Fatal(err)
	}
	policy := Policy{Workspace: ws, Profile: ProfileWorkspace, WriteExtra: []string{parent}}

	s, err := resolvePolicy(policy, "bwrap")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.gitPinned, ws) {
		t.Errorf("the workspace below a wider grant must be pinned: %v", s.gitPinned)
	}
	if slices.Contains(s.gitReadOnly, info) || !slices.Contains(s.gitUnheld, info) {
		t.Errorf("a dangling link is not held under bubblewrap, and must be named: ro=%v unheld=%v", s.gitReadOnly, s.gitUnheld)
	}
}

// GitStoreOf answers for the store and every program path, and for nothing
// else in the checkout.
// The store a workspace's repository keeps is named from the checkout's
// top, from below it and from a linked worktree, whose store is the main
// checkout's — and nothing is named outside a repository.
func TestGitStoreForNamesTheWorkspacesStore(t *testing.T) {
	testHome(t)
	_, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")
	gitRun(t, ws, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "seed")
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, ws, "worktree", "add", "-q", linked)

	for _, dir := range []string{ws, mkdir(t, filepath.Join(ws, "src")), linked} {
		if store, ok := GitStoreFor(dir); !ok || store != dotgit {
			t.Errorf("GitStoreFor(%s) = %q, %v; want %s", dir, store, ok, dotgit)
		}
	}
	if store, ok := GitStoreFor(t.TempDir()); ok {
		t.Errorf("a directory in no repository has no store: %q", store)
	}
}

func TestGitStoreOfNamesTheStoreAndItsProgramPathsOnly(t *testing.T) {
	testHome(t)
	_, ws := gitWorkspace(t)
	dotgit := filepath.Join(ws, ".git")
	src := mkdir(t, filepath.Join(ws, "src"))

	for _, dir := range []string{dotgit, filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "objects")} {
		if store, ok := GitStoreOf(dir); !ok || store != dotgit {
			t.Errorf("GitStoreOf(%s) = %q, %v; want %s", dir, store, ok, dotgit)
		}
	}
	if store, ok := GitStoreOf(src); ok {
		t.Errorf("the working tree is not the store: GitStoreOf(%s) = %q", src, store)
	}
	if store, ok := GitStoreOf(t.TempDir()); ok {
		t.Errorf("a directory in no repository has no store: %q", store)
	}
	if _, err := os.Stat(filepath.Join(dotgit, "commondir")); err == nil {
		t.Error("reading the store must not create what it masks")
	}
}
