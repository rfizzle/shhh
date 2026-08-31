package cli

// What `--help` shows, and where. The renders here go through the same
// dressing the binary does, because the shape being asserted — sections,
// groups, the order flags are listed in — is entirely the dressing's.

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/spf13/cobra"
)

// renderHelp is `shhh <path> --help` as a terminal 80 columns wide and no
// colour would see it. The width is fang's own test hook, and it is resolved
// once per process, so every help render in this package has to ask for the
// same one.
func renderHelp(t *testing.T, path ...string) string {
	t.Helper()
	t.Setenv("__FANG_TEST_WIDTH", "80")
	t.Setenv("NO_COLOR", "1")
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(append([]string{}, path...), "--help"))
	if err := execute(context.Background(), cmd); err != nil {
		t.Fatalf("shhh %s --help: %v", strings.Join(path, " "), err)
	}
	return out.String()
}

// modelFlags are the four that decide where a request goes. A command either
// sends one or it does not, and its help should say which.
var modelFlags = []string{"--provider", "--model", "--api-key", "--reasoning"}

func TestHelpShowsModelFlagsOnlyWhereTheyAct(t *testing.T) {
	for _, path := range [][]string{{}, {"chat"}, {"code"}, {"chats"}} {
		help := renderHelp(t, path...)
		for _, flag := range modelFlags {
			if !strings.Contains(help, flag) {
				t.Errorf("shhh %s --help should offer %s, got:\n%s", strings.Join(path, " "), flag, help)
			}
		}
	}
	// Every other command reaches no provider, so none of the four is
	// listed — and a `--model` on `history clear` was the loudest of them.
	for _, path := range [][]string{
		{"history", "clear"}, {"snippets"}, {"memory"}, {"providers"},
		{"completion"}, {"config", "set"}, {"doctor"}, {"todo"}, {"metrics"},
		{"chats", "list"},
	} {
		help := renderHelp(t, path...)
		for _, flag := range modelFlags {
			if strings.Contains(help, flag) {
				t.Errorf("shhh %s --help offers %s, which it cannot use:\n%s",
					strings.Join(path, " "), flag, help)
			}
		}
	}
}

func TestModelFlagOnARecordCommandIsRejected(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"history", "clear", "--model", "gpt-4o"})
	err := execute(context.Background(), cmd)
	if err == nil {
		t.Fatal("`shhh history clear --model` should be refused, not accepted and ignored")
	}
	if !strings.Contains(err.Error(), "unknown flag: --model") {
		t.Errorf("the refusal should name the flag, got %q", err)
	}
	if !strings.Contains(out.String(), "ERROR") {
		t.Errorf("the refusal should arrive as the labelled block, got:\n%s", out.String())
	}
}

func TestProviderFlagNamesEveryProviderTheRegistryHas(t *testing.T) {
	names := provider.Available()
	sort.Strings(names)
	usage := providerFlagUsage()
	if !strings.Contains(usage, strings.Join(names, ", ")) {
		t.Errorf("--provider should offer the registry join %q, got %q", strings.Join(names, ", "), usage)
	}
	for _, name := range names {
		if !strings.Contains(usage, name) {
			t.Errorf("--provider does not name %q, which resolves", name)
		}
	}
	// A profile resolves exactly like a built-in but is registered after the
	// command tree is built, so it is named as a kind rather than listed.
	if !strings.Contains(usage, "profile") {
		t.Errorf("--provider should say a profile name works too, got %q", usage)
	}
	// Every command carrying the flag says the same thing about it: one
	// vocabulary, generated once.
	for _, cmd := range modelFlagCommands(t) {
		if got := cmd.Flags().Lookup("provider").Usage; got != usage {
			t.Errorf("%s --provider says %q, not the generated text", cmd.CommandPath(), got)
		}
	}
}

// modelFlagCommands is every command that declares --provider.
func modelFlagCommands(t *testing.T) []*cobra.Command {
	t.Helper()
	root := NewRootCmd()
	var found []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("provider") != nil {
			found = append(found, c)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if len(found) == 0 {
		t.Fatal("no command declares --provider")
	}
	return found
}

func TestEveryCommandIsGrouped(t *testing.T) {
	root := NewRootCmd()
	for _, sub := range root.Commands() {
		if sub.Hidden {
			continue
		}
		if sub.GroupID == "" {
			t.Errorf("%q is in no group, so help lists it under a heading of its own",
				sub.Name())
		}
	}
}

func TestHelpDoesNotListTheHelpCommand(t *testing.T) {
	help := renderHelp(t)
	if strings.Contains(help, "help [command]") {
		t.Errorf("`help` is how cobra spells --help, not a destination:\n%s", help)
	}
}

// TestRootHelpGolden holds the whole page rather than substrings of it: the
// grouping, the order inside each group and the break in FLAGS are all facts
// about the shape, which only a whole render can regress on. It shares the
// report fixtures because it is the same question — what the CLI leaves in
// the scrollback.
func TestRootHelpGolden(t *testing.T) {
	assertReportGolden(t, "help.w80", strings.TrimRight(renderHelp(t), "\n"))
}
