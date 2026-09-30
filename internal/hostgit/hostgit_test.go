package hostgit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/hostgit/hostgittest"
)

// An inherited override set is replaced whole rather than extended: the
// variables are numbered, and GIT_CONFIG_PARAMETERS is read after them.
func TestEnvReplacesWhateverConfigWasInherited(t *testing.T) {
	base := []string{
		"HOME=/home/someone",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=/tmp/attacker",
		"GIT_CONFIG_PARAMETERS='core.fsmonitor'='/tmp/attacker'",
	}
	env := Env(base)
	want := []string{
		"HOME=/home/someone",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=diff.ignoreSubmodules", "GIT_CONFIG_VALUE_1=all",
		"GIT_CONFIG_KEY_2=submodule.recurse", "GIT_CONFIG_VALUE_2=false",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("Env(base) =\n%q\nwant\n%q", env, want)
	}
	for _, pair := range env {
		if strings.Contains(pair, "attacker") {
			t.Fatalf("an inherited program survived: %q", pair)
		}
	}
}

func TestCommandPutsEachVerbsFlagsAfterIt(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"status", "--porcelain"},
			[]string{"--no-pager", "-C", "/w", "status", IgnoreSubmodules, "--porcelain"}},
		{[]string{"diff", "HEAD", "--binary"},
			[]string{"--no-pager", "-C", "/w", "diff", "--no-ext-diff", "--no-textconv", IgnoreSubmodules, "HEAD", "--binary"}},
		{[]string{"diff-index", "HEAD"},
			[]string{"--no-pager", "-C", "/w", "diff-index", IgnoreSubmodules, "HEAD"}},
		{[]string{"log", "-1"},
			[]string{"--no-pager", "-C", "/w", "log", "--no-ext-diff", "--no-textconv", "--no-show-signature", "-1"}},
		// A -c's value is not the verb, and a verb with nothing to shut
		// takes nothing.
		{[]string{"-c", "user.name=shhh", "commit", "-m", "x"},
			[]string{"--no-pager", "-C", "/w", "-c", "user.name=shhh", "commit", "-m", "x"}},
		{[]string{"rev-parse", "HEAD"},
			[]string{"--no-pager", "-C", "/w", "rev-parse", "HEAD"}},
	}
	for _, c := range cases {
		cmd := Command(context.Background(), "/w", c.args...)
		if got := cmd.Args[1:]; !slices.Equal(got, c.want) {
			t.Errorf("Command(%q) =\n%q\nwant\n%q", c.args, got, c.want)
		}
		if !slices.Contains(cmd.Env, "GIT_CONFIG_KEY_0=core.fsmonitor") {
			t.Errorf("Command(%q) should run under Env", c.args)
		}
	}
	if got := Command(context.Background(), "", "merge-file", "-p").Args[1:]; !slices.Equal(got, []string{"--no-pager", "merge-file", "-p"}) {
		t.Errorf("an empty dir should pass no -C, got %q", got)
	}
}

// A submodule a contained command made and staged, with a store it wrote,
// is not asked anything by the readings that compare the working tree.
func TestTheReadingsRunNothingFromAPlantedSubmodule(t *testing.T) {
	p := hostgittest.PlantedSubmodule(t)
	p.Control(t, "status", "--porcelain")

	for _, args := range [][]string{
		{"status", "--porcelain=v2", "--branch", "--untracked-files=normal", "-z"},
		{"status", "--porcelain", "-z", "-uall"},
		{"diff"},
		{"diff", "HEAD", "--binary"},
		{"diff-index", "HEAD"},
		{"diff-files"},
	} {
		p.Stir(t)
		if out, err := Command(context.Background(), p.Root, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		if p.Ran() {
			t.Fatalf("git %s ran the planted submodule's filter", strings.Join(args, " "))
		}
	}
}
