package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// uncomment turns the scaffold into the file a person gets by uncommenting
// every line of it. A key line is a `#` against the key; a sentence is a `#`
// and a space, which is what tells them apart without a parser.
func uncomment(text string) (string, int) {
	var out []string
	keys := 0
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "#"); ok && rest != "" && !strings.HasPrefix(rest, " ") {
			out = append(out, rest)
			keys++
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), keys
}

func TestScaffold_EveryKeyIsThereAndUncommentsToAFileThatLoads(t *testing.T) {
	text, keys := uncomment(Scaffold(Config{}, false))
	if want := ScaffoldKeys(false); keys != want {
		t.Fatalf("scaffold wrote %d keys, want %d", keys, want)
	}
	var cfg Config
	meta, err := toml.Decode(text, &cfg)
	if err != nil {
		t.Fatalf("the uncommented scaffold does not parse: %v", err)
	}
	if left := meta.Undecoded(); len(left) > 0 {
		t.Fatalf("the uncommented scaffold holds keys no setting reads: %v", left)
	}
}

// A default written as a sentence — `2 MiB`, `8000 tokens` — must reach the
// file as the number the file takes. Reading the leading token back out of
// the sentence is what would make `2 MiB` mean two bytes, so the table
// states the literal and this is the check that it did.
func TestScaffold_ADefaultStatedAsASentenceIsWrittenAsItsValue(t *testing.T) {
	text, _ := uncomment(Scaffold(Config{}, false))
	var cfg Config
	if _, err := toml.Decode(text, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"web.fetch_max_bytes", "2097152"},
		{"behavior.context_max_tokens", "8000"},
		{"behavior.mode_cycle", "manual, accept-edits, auto, plan"},
		{"observe.retention_days", "180"},
	} {
		if got, _ := Value(cfg, tc.key); got != tc.want {
			t.Errorf("%s uncommented to %q, want %q", tc.key, got, tc.want)
		}
	}
}

// Every setting whose default is a sentence has to say what the file would
// write, or the scaffold quietly offers the key's zero where a real default
// stands. A key added without one fails here rather than in a file somebody
// pasted.
func TestScaffold_EverySentenceDefaultStatesItsLiteral(t *testing.T) {
	for _, s := range settings {
		if s.written() != "" || strings.HasPrefix(s.Default, "(") {
			continue
		}
		t.Errorf("%s: default %q is a sentence and states no literal", s.Key, s.Default)
	}
}

func TestScaffold_ACheckoutsFileLeavesOutWhatACheckoutMayNotDecide(t *testing.T) {
	text := Scaffold(Config{}, true)
	for _, absent := range []string{"[sandbox]", "[prompts]", "api_key", "\nenv = "} {
		if strings.Contains(text, absent) {
			t.Errorf("a checkout's scaffold holds %q", absent)
		}
	}
	// The rest of a partly refused table stays: `secrets.env` is refused and
	// `secrets.env_mask` beside it is not, and dropping the table would take
	// a key with it that a checkout may perfectly well set.
	if !strings.Contains(text, "#env_mask = true") {
		t.Error("a checkout's scaffold dropped the keys beside a refused one")
	}
	if _, keys := uncomment(text); keys != ScaffoldKeys(true) {
		t.Errorf("a checkout's scaffold wrote %d keys, want %d", keys, ScaffoldKeys(true))
	}
}

func TestScaffold_AValueTheFileAlreadyHoldsIsWrittenUncommented(t *testing.T) {
	cfg := Config{}
	if err := Set(&cfg, "provider.default", "anthropic"); err != nil {
		t.Fatal(err)
	}
	if err := Set(&cfg, "behavior.command_allowlist", "ls, git status"); err != nil {
		t.Fatal(err)
	}
	text := Scaffold(cfg, false)
	for _, want := range []string{"\ndefault = \"anthropic\"\n", "\ncommand_allowlist = [\"ls\", \"git status\"]\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scaffold did not fill in %q", strings.TrimSpace(want))
		}
	}
	if strings.Contains(text, "#default = \"openai\"") {
		t.Error("the scaffold wrote the default beside the value in force")
	}
	// What it filled in has to read back as what it was handed, or the paste
	// this exists for loses an answer already given.
	var back Config
	if _, err := toml.Decode(text, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, _ := Value(back, "provider.default"); got != "anthropic" {
		t.Errorf("provider.default read back as %q", got)
	}
}

// A per-role model is the one key with a segment the person chooses. The
// roles a config names are written out, and the shape a new one takes is
// left commented — quoted, so uncommenting it is still a file that parses.
func TestScaffold_APerRoleModelIsWrittenPerRoleAndAsItsShape(t *testing.T) {
	cfg := Config{}
	if err := Set(&cfg, "agents.profiles.reviewer.model", "sonnet"); err != nil {
		t.Fatal(err)
	}
	text := Scaffold(cfg, false)
	if !strings.Contains(text, "profiles.\"reviewer\".model = \"sonnet\"") {
		t.Error("the scaffold did not write the role this config names")
	}
	if !strings.Contains(text, "#profiles.\"<role>\".model") {
		t.Error("the scaffold did not leave the shape a new role takes")
	}
}

func TestScaffold_EveryKeyCarriesTheSentenceThatSaysWhatItDecides(t *testing.T) {
	prose := commentProse(Scaffold(Config{}, false))
	for _, s := range settings {
		if !strings.Contains(prose, s.Desc) {
			t.Errorf("%s: the scaffold carries nothing of its description", s.Key)
		}
	}
}

// The two wordings that take substitutions name them in the comment above
// their key and nowhere else. The file a wording lives in is sent to the
// model as written, so a list of placeholders in it would be a list of
// placeholders in the prompt.
func TestScaffold_AWordingsPlaceholdersAreNamedAboveItsKey(t *testing.T) {
	prose := commentProse(Scaffold(Config{}, false))
	for _, want := range []string{"{{target}}", "{{reason}}", "{{rounds}}", "{{finished}}", "{{item}}", "{{diff}}"} {
		if !strings.Contains(prose, want) {
			t.Errorf("the scaffold never names %s", want)
		}
	}
}

// commentProse is every sentence in the scaffold run back together, so a
// description that the file wrapped over three lines is one string again.
func commentProse(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			out = append(out, rest)
		}
	}
	return strings.Join(out, " ")
}

// The commented lines are the defaults, all of them. Uncommenting the whole
// file has to leave a config that behaves exactly as one with no file at
// all, or the scaffold is a set of changes wearing the word "default": a
// switch that reads `on` and writes `false` turns something off for the
// first person who uncomments its line.
func TestScaffold_UncommentingItAllChangesNothing(t *testing.T) {
	text, _ := uncomment(Scaffold(Config{}, false))
	var got Config
	if _, err := toml.Decode(text, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range settings {
		if strings.Contains(s.Key, RoleWildcard) {
			continue
		}
		var want Config
		if v := s.written(); v != "" {
			if err := Set(&want, s.Key, v); err != nil {
				t.Errorf("%s: its default %q is not a value the key takes: %v", s.Key, v, err)
				continue
			}
		}
		gotText, _ := Value(got, s.Key)
		wantText, _ := Value(want, s.Key)
		if gotText != wantText {
			t.Errorf("%s uncommented to %q, want its default %q", s.Key, gotText, wantText)
		}
	}
}
