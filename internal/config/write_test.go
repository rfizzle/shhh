package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file a person wrote: three sections, comments saying why, a trailing
// comment on a value, an indented key, a list over several lines.
const handWritten = `# my settings — the model is pinned because the default changed under me
[provider]
default = "anthropic"
model = "claude-sonnet-5"   # pinned on purpose

[appearance]
  mouse = false  # I select with the terminal

[behavior]
command_allowlist = [
  "go test",
  "go build",
]
# leave the rest at their defaults
`

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if text != "" {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWrite_ChangesOneLineAndNothingElse(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "provider.model", Value: "claude-opus-5"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(handWritten,
		`model = "claude-sonnet-5"   # pinned on purpose`,
		`model = "claude-opus-5"   # pinned on purpose`, 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("the write touched more than its line:\n--- want\n%s--- got\n%s", want, got)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "claude-opus-5" || cfg.Appearance.Mouse == nil || *cfg.Appearance.Mouse {
		t.Fatalf("loaded back wrong: %+v", cfg)
	}
}

func TestWrite_ZeroRemovesTheLine(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "appearance.mouse", Value: ""}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(handWritten, "  mouse = false  # I select with the terminal\n", "", 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.Mouse != nil {
		t.Fatalf("mouse should be unset, got %v", *cfg.Appearance.Mouse)
	}
}

func TestWrite_AddsUnderAnExistingTableAtItsIndent(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "appearance.notify", Value: "false"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(handWritten,
		"  mouse = false  # I select with the terminal\n",
		"  mouse = false  # I select with the terminal\n  notify = false\n", 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

func TestWrite_AddsATableAtTheEndWhenTheFileLacksIt(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "summary.model", Value: "claude-haiku-4-5-20251001"}); err != nil {
		t.Fatal(err)
	}
	want := handWritten + "\n[summary]\nmodel = \"claude-haiku-4-5-20251001\"\n"
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

func TestWrite_CreatesAMissingFileHoldingOnlyTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "shhh", "config.toml")
	if err := Write(path, Edit{Key: "appearance.mouse", Value: "false"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[appearance]\nmouse = false\n"; got != want {
		t.Fatalf("a new file should hold the key alone:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("a new config file is 0600, got %v", info.Mode().Perm())
	}
}

func TestWrite_ReplacesAMultiLineValue(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "behavior.command_allowlist", Value: "go test, make lint"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(handWritten,
		"command_allowlist = [\n  \"go test\",\n  \"go build\",\n]\n",
		"command_allowlist = [\"go test\", \"make lint\"]\n", 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Behavior.CommandAllowlist, "|") != "go test|make lint" {
		t.Fatalf("allowlist = %v", cfg.Behavior.CommandAllowlist)
	}
}

func TestWrite_ReplacesAMultiLineString(t *testing.T) {
	// The string ends in a quote of its own, so its closing delimiter is
	// four quotes long — the form TOML allows and a naive scan stops short of.
	text := "[behavior]\nsystem_prompt_extra = \"\"\"\nBe brief.\nEnd with \"done\"\"\"\"\nshell = \"zsh\"\n"
	path := writeTemp(t, text)
	if err := Write(path, Edit{Key: "behavior.system_prompt_extra", Value: "Be terse."}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[behavior]\nsystem_prompt_extra = \"Be terse.\"\nshell = \"zsh\"\n"; got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

// A key the file spells as a dotted path at the root, or under a shorter
// table, is the same key.
func TestWrite_FindsADottedKey(t *testing.T) {
	for _, text := range []string{
		"appearance.mouse = true\n",
		"[appearance]\nmouse = true\n",
		"[ appearance ]\n\"mouse\" = true\n",
	} {
		path := writeTemp(t, text)
		if err := Write(path, Edit{Key: "appearance.mouse", Value: "false"}); err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(text, "true", "false", 1)
		if got := readBack(t, path); got != want {
			t.Fatalf("%q: --- want\n%s--- got\n%s", text, want, got)
		}
	}
}

// The three keys `config set` spells one way and the file another.
func TestWrite_RoleModelIsAProfileTable(t *testing.T) {
	path := writeTemp(t, "")
	if err := Write(path, Edit{Key: "agents.researcher_model", Value: "claude-haiku-4-5-20251001"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[agents.profiles.researcher]\nmodel = \"claude-haiku-4-5-20251001\"\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.Profiles["researcher"].Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("profile not read back: %+v", cfg.Agents.Profiles)
	}
}

// A negative is a value — no bound — and stays; a zero is unset and goes.
func TestWrite_KeepsANegativeAndDropsAZero(t *testing.T) {
	path := writeTemp(t, "[behavior]\nmax_tool_rounds = 40\ncommand_timeout_seconds = 30\n")
	if err := Write(path,
		Edit{Key: "behavior.max_tool_rounds", Value: "-1"},
		Edit{Key: "behavior.command_timeout_seconds", Value: "0"},
		Edit{Key: "behavior.check_in_max_doublings", Value: "-1"},
	); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[behavior]\nmax_tool_rounds = -1\ncheck_in_max_doublings = -1\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
}

// A count whose unset is not its zero is written as the zero it was given.
// The general rule takes a zero out of the file; this key's zero is the
// answer "do not wait one out", and a file it vanished from would say the
// opposite of what the person asked for.
func TestWrite_KeepsAZeroWhereZeroIsTheAnswer(t *testing.T) {
	path := writeTemp(t, "[behavior]\nprovider_retries = 3\n")
	if err := Write(path, Edit{Key: "behavior.provider_retries", Value: "0"}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[behavior]\nprovider_retries = 0\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
	if err := Write(path, Edit{Key: "behavior.provider_retries", Value: ""}); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "[behavior]\n"; got != want {
		t.Fatalf("clearing the key should take the line out, got:\n%s", got)
	}
}

// The six slash commands that persist an answer, each through the writer
// they share, each read back as what was written.
func TestWrite_SlashWritersRoundTrip(t *testing.T) {
	cases := []struct {
		key, value string
		read       func(Config) string
	}{
		{"provider.model", "claude-opus-5", func(c Config) string { return c.Provider.Model }},
		{"agents.model", "claude-haiku-4-5-20251001", func(c Config) string { return c.Agents.Model }},
		{"provider.reasoning", "high", func(c Config) string { return c.Provider.Reasoning }},
		{"appearance.mouse", "false", func(c Config) string { return boolWord(c.Appearance.Mouse) }},
		{"appearance.notify", "false", func(c Config) string { return boolWord(c.Appearance.Notify) }},
		{"appearance.window_title", "true", func(c Config) string { return boolWord(c.Appearance.WindowTitle) }},
	}
	path := writeTemp(t, handWritten)
	for _, c := range cases {
		if err := Write(path, Edit{Key: c.key, Value: c.value}); err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		if got := c.read(cfg); got != c.value {
			t.Errorf("%s = %q after writing %q", c.key, got, c.value)
		}
	}
	got := readBack(t, path)
	for _, line := range []string{"# my settings", "# pinned on purpose", "# I select with the terminal", "# leave the rest"} {
		if !strings.Contains(got, line) {
			t.Errorf("comment %q lost:\n%s", line, got)
		}
	}
	if strings.Count(got, "mouse = ") != 1 {
		t.Errorf("mouse should be written once:\n%s", got)
	}
}

func boolWord(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}

func TestWrite_AStringWithEscapesRoundTrips(t *testing.T) {
	value := "say \"hi\"\tthen\\ stop\nnow \x01 é"
	path := writeTemp(t, "")
	if err := Write(path, Edit{Key: "behavior.system_prompt_extra", Value: value}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Behavior.SystemPromptExtra != value {
		t.Fatalf("got %q", cfg.Behavior.SystemPromptExtra)
	}
}

func TestWrite_RefusesAFileThatDoesNotParse(t *testing.T) {
	broken := "[provider\nmodel = \"x\"\n"
	path := writeTemp(t, broken)
	err := Write(path, Edit{Key: "provider.model", Value: "y"})
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("err = %v", err)
	}
	if got := readBack(t, path); got != broken {
		t.Fatalf("the file was touched:\n%s", got)
	}
}

func TestWrite_RefusesAnUnknownKey(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := Write(path, Edit{Key: "provider.modle", Value: "y"}); err == nil {
		t.Fatal("an unknown key was written")
	}
	if got := readBack(t, path); got != handWritten {
		t.Fatalf("the file was touched:\n%s", got)
	}
}

func TestWriteServer_ThenRemoveLeavesTheFileAsItWas(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"no mcp table", handWritten},
		{"mcp table last", handWritten + "\n[mcp]\ndisabled = false\n"},
		{"mcp table middle", "[mcp]\nstartup_timeout_seconds = 5\n# other servers are in mcp.json\n\n[provider]\ndefault = \"anthropic\"\n"},
		{"a sibling server", "[mcp.servers.docs]\ncommand = \"docs-mcp\"\n\n[mcp.servers.docs.env]\nA = \"1\"\n\n[provider]\ndefault = \"anthropic\"\n"},
		{"no trailing newline", "[provider]\ndefault = \"anthropic\""},
		{"a comment introduces the next section", "[mcp]\ndisabled = false\n\n# the provider I use, after a long argument\n[provider]\ndefault = \"anthropic\"\n"},
		{"no separators at all", "[mcp]\ndisabled = false\n[provider]\ndefault = \"anthropic\"\n"},
		{"windows line endings", "[mcp]\r\ndisabled = false\r\n\r\n[provider]\r\ndefault = \"anthropic\"\r\n"},
	} {
		text := tc.text
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, text)
			s := MCPServer{Command: "npx", Args: []string{"-y", "@x/y"}, Env: map[string]string{"TOKEN": "${TOKEN}"}, ReadOnly: true}
			if err := WriteServer(path, "my server", s); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatalf("after add: %v\n%s", err, readBack(t, path))
			}
			got := cfg.MCP.Servers["my server"]
			if got.Command != "npx" || strings.Join(got.Args, " ") != "-y @x/y" || got.Env["TOKEN"] != "${TOKEN}" || !got.ReadOnly || got.TimeoutSeconds != 0 {
				t.Fatalf("read back %+v\n%s", got, readBack(t, path))
			}
			if added := readBack(t, path); strings.Contains(added, "timeout_seconds = 0") || strings.Contains(added, "disabled = false\n[mcp.servers") {
				t.Fatalf("a zero was written:\n%s", added)
			}
			if err := RemoveServer(path, "my server"); err != nil {
				t.Fatal(err)
			}
			want := text
			if !strings.HasSuffix(want, "\n") {
				want += "\n"
			}
			if got := readBack(t, path); got != want {
				t.Fatalf("--- want\n%s--- got\n%s", want, got)
			}
		})
	}
}

// A comment under the last MCP key introduces the section after it, and a
// table added there goes above the comment, not between it and its section.
func TestWriteServer_DoesNotTakeTheNextSectionsComment(t *testing.T) {
	text := "[mcp]\ndisabled = false\n\n# the provider I use, after a long argument\n[provider]\ndefault = \"anthropic\"\n"
	path := writeTemp(t, text)
	if err := WriteServer(path, "docs", MCPServer{Command: "docs-mcp"}); err != nil {
		t.Fatal(err)
	}
	want := "[mcp]\ndisabled = false\n\n[mcp.servers.docs]\ncommand = \"docs-mcp\"\n\n# the provider I use, after a long argument\n[provider]\ndefault = \"anthropic\"\n"
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
	if err := RemoveServer(path, "docs"); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != text {
		t.Fatalf("--- want\n%s--- got\n%s", text, got)
	}
}

// A comment after a server's last key stays in the file when the server
// goes: it more often belongs to what follows, and a stray comment costs less
// than a destroyed one.
func TestRemoveServer_LeavesACommentBelowTheTable(t *testing.T) {
	text := "[mcp.servers.docs]\ncommand = \"x\"\n\n# why anthropic\n[provider]\ndefault = \"anthropic\"\n"
	path := writeTemp(t, text)
	if err := RemoveServer(path, "docs"); err != nil {
		t.Fatal(err)
	}
	if got, want := readBack(t, path), "# why anthropic\n[provider]\ndefault = \"anthropic\"\n"; got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

func TestWrite_FollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "shhh.toml")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := Write(link, Edit{Key: "provider.model", Value: "claude-opus-5"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file: %v %v", info, err)
	}
	if !strings.Contains(readBack(t, real), `model = "claude-opus-5"`) {
		t.Fatalf("the real file did not change:\n%s", readBack(t, real))
	}
}

func TestWrite_KeepsAByteOrderMark(t *testing.T) {
	text := "\uFEFF" + handWritten
	path := writeTemp(t, text)
	if err := Write(path, Edit{Key: "provider.model", Value: "claude-opus-5"}); err != nil {
		t.Fatal(err)
	}
	want := "\uFEFF" + strings.Replace(handWritten, `"claude-sonnet-5"`, `"claude-opus-5"`, 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%q--- got\n%q", want, got)
	}
}

// A section written as an inline table takes no line, so the write says so
// and names it rather than appending a header the file would then define
// twice, or quietly leaving the value in force.
func TestWrite_RefusesAKeyInsideAnInlineTable(t *testing.T) {
	text := "appearance = { mouse = true }\n"
	path := writeTemp(t, text)
	for _, value := range []string{"false", ""} {
		err := Write(path, Edit{Key: "appearance.notify", Value: value})
		if err == nil || !strings.Contains(err.Error(), "appearance is written as an inline table") {
			t.Fatalf("value %q: err = %v", value, err)
		}
	}
	if got := readBack(t, path); got != text {
		t.Fatalf("touched:\n%s", got)
	}
}

func TestWrite_AddsLinesInTheFilesOwnLineEnding(t *testing.T) {
	text := "[provider]\r\nmodel = \"a\"\r\n"
	path := writeTemp(t, text)
	if err := Write(path, Edit{Key: "provider.default", Value: "openai"}, Edit{Key: "appearance.mouse", Value: "false"}); err != nil {
		t.Fatal(err)
	}
	want := "[provider]\r\nmodel = \"a\"\r\ndefault = \"openai\"\r\n\r\n[appearance]\r\nmouse = false\r\n"
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%q--- got\n%q", want, got)
	}
}

// A number that is not one is refused, not read as zero: under the rule
// that a zero takes the line out, `abc` would have deleted the setting.
func TestWrite_RefusesANonNumber(t *testing.T) {
	text := "[behavior]\nmax_tool_rounds = 40\n"
	path := writeTemp(t, text)
	err := Write(path, Edit{Key: "behavior.max_tool_rounds", Value: "abc"})
	if err == nil || !strings.Contains(err.Error(), "is not a whole number") {
		t.Fatalf("err = %v", err)
	}
	if got := readBack(t, path); got != text {
		t.Fatalf("touched:\n%s", got)
	}
}

func TestWriteServer_LandsBesideTheOtherMCPTables(t *testing.T) {
	text := "[mcp]\ndisabled = false\n\n[mcp.servers.docs]\ncommand = \"docs-mcp\"\n\n[provider]\ndefault = \"anthropic\"\n"
	path := writeTemp(t, text)
	if err := WriteServer(path, "fs", MCPServer{Command: "fs-mcp"}); err != nil {
		t.Fatal(err)
	}
	want := "[mcp]\ndisabled = false\n\n[mcp.servers.docs]\ncommand = \"docs-mcp\"\n\n[mcp.servers.fs]\ncommand = \"fs-mcp\"\n\n[provider]\ndefault = \"anthropic\"\n"
	if got := readBack(t, path); got != want {
		t.Fatalf("--- want\n%s--- got\n%s", want, got)
	}
}

// A definition of the same name already in the file — as a table, as a
// dotted key, with sub-tables — is replaced whole, not merged.
func TestWriteServer_ReplacesAnExistingDefinition(t *testing.T) {
	text := "[mcp.servers]\nfs.command = \"old\"\n\n[mcp.servers.fs.env]\nA = \"1\"\n\n[provider]\ndefault = \"anthropic\"\n"
	path := writeTemp(t, text)
	if err := WriteServer(path, "fs", MCPServer{URL: "https://x.test/mcp"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("%v\n%s", err, readBack(t, path))
	}
	got := cfg.MCP.Servers["fs"]
	if got.URL != "https://x.test/mcp" || got.Command != "" || len(got.Env) != 0 {
		t.Fatalf("read back %+v\n%s", got, readBack(t, path))
	}
	if !strings.Contains(readBack(t, path), "[provider]\ndefault = \"anthropic\"\n") {
		t.Fatalf("the neighbour was touched:\n%s", readBack(t, path))
	}
}

func TestRemoveServer_OnAMissingNameChangesNothing(t *testing.T) {
	path := writeTemp(t, handWritten)
	if err := RemoveServer(path, "nothing"); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != handWritten {
		t.Fatalf("touched:\n%s", got)
	}
}

func TestSet_EmptyUnsetsATriStateKey(t *testing.T) {
	var c Config
	if err := Set(&c, "appearance.mouse", "false"); err != nil {
		t.Fatal(err)
	}
	if c.Appearance.Mouse == nil || *c.Appearance.Mouse {
		t.Fatal("false did not take")
	}
	if err := Set(&c, "appearance.mouse", ""); err != nil {
		t.Fatal(err)
	}
	if c.Appearance.Mouse != nil {
		t.Fatal("an empty value should unset the key, not write false")
	}
}

// A boolean the parser cannot read leaves the file alone. Under the rule
// that a zero takes the line out, `yes` would have deleted the setting the
// person had — and reported success doing it.
func TestWrite_RefusesABooleanThatIsNotOne(t *testing.T) {
	text := "[appearance]\nmouse = false\n"
	path := writeTemp(t, text)
	err := Write(path, Edit{Key: "appearance.mouse", Value: "yes"})
	if err == nil || !strings.Contains(err.Error(), "is not true or false") {
		t.Fatalf("err = %v", err)
	}
	if got := readBack(t, path); got != text {
		t.Fatalf("touched:\n%s", got)
	}
}

// A refusal on the second of two edits leaves the file holding neither: a
// write is one act, and half of it applied is a file nobody asked for.
func TestWrite_ARefusedEditLeavesTheEarlierOnesUnwritten(t *testing.T) {
	text := "[provider]\ndefault = \"anthropic\"\n"
	path := writeTemp(t, text)
	err := Write(path,
		Edit{Key: "provider.model", Value: "claude-opus-5"},
		Edit{Key: "history.retention_days", Value: "abc"})
	if err == nil {
		t.Fatal("the bad value is refused")
	}
	if got := readBack(t, path); got != text {
		t.Fatalf("the first edit landed anyway:\n%s", got)
	}
}

// The reviewer's model goes to its own profile table, the way the other two
// roles' do.
func TestWrite_ReviewerModelIsAProfileTable(t *testing.T) {
	path := writeTemp(t, "")
	if err := Write(path, Edit{Key: "agents.reviewer_model", Value: "claude-haiku-4-5-20251001"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.Profiles["reviewer"].Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("profile not read back: %+v", cfg.Agents.Profiles)
	}
}
