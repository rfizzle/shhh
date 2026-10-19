package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/agentcore/reading"
	"github.com/rfizzle/agentcore/repeat"
	"github.com/rfizzle/agentcore/secret"
	"github.com/rfizzle/agentcore/skill"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/memory"
)

// wordingsMaster is the master commit the hashes below were captured from.
const wordingsMaster = "d281b19e5"

// wordingHash is the sha256 of a model-facing text, hex encoded.
func wordingHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// jsonOf renders a value as the JSON the provider is sent, so a tool
// definition is pinned by every byte the model reads of it.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wordingsFixtureSkills is a fixed catalog, and one skill read from a
// SKILL.md written into a temporary directory (no Dir, so no path leaks into
// the rendered text).
func wordingsFixtureSkills(t *testing.T) (*skill.Catalog, skill.Skill) {
	t.Helper()
	loc := filepath.Join(t.TempDir(), "SKILL.md")
	body := "---\nname: tidy\ndescription: Tidy a file.\n---\n# Tidy\n\nRead the file, then fix its spacing.\n"
	if err := os.WriteFile(loc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	listed := skill.Skill{Name: "tidy", Description: "Tidy a file & <report>.", Location: "/skills/tidy/SKILL.md", Compatibility: "any shell"}
	disk := listed
	disk.Location = loc
	other := skill.Skill{Name: "audit", Description: "Audit a change.", Location: "/skills/audit/SKILL.md"}
	return &skill.Catalog{Skills: []skill.Skill{listed, other}}, disk
}

// TestModelFacingWordings pins, by sha256, every text the model reads from the
// packages that are to move. A failure names the text and prints the got hash,
// so a deliberate rewording can be recaptured.
func TestModelFacingWordings(t *testing.T) {
	// The session-steer cases read the wording the app installs at startup.
	handSessionSteerWording()
	t.Cleanup(func() { agent.SetSessionSteerWording(nil) })

	cat, diskSkill := wordingsFixtureSkills(t)
	content, err := skill.Content(diskSkill)
	if err != nil {
		t.Fatal(err)
	}

	vault := secret.New()
	if err := vault.Add("API_TOKEN", "s3cr3t-value-123"); err != nil {
		t.Fatal(err)
	}

	// The mask is what makes the asks flag change the block.
	vault.SetEnvMask(true)

	mem := []memory.Entry{
		{ID: 1, Scope: "project", Kind: memory.KindPreference, Text: "Prefer table tests."},
		{ID: 2, Scope: "user", Kind: memory.KindLesson, Text: "Run the gate before committing."},
	}

	// The repeat notices, from fixed calls: a repeated call that succeeded,
	// one that failed, a repeated question (twice over), and a sweep.
	twice := func(call func(d *repeat.RepeatDetector) string) string {
		d := repeat.NewRepeatDetector(repeatShapes())
		call(d)
		return call(d)
	}
	shellArgs := json.RawMessage(`{"command":"go test ./..."}`)
	askArgs := json.RawMessage(`{"question":"which one?"}`)
	repeatOK := twice(func(d *repeat.RepeatDetector) string { return d.Notice("shell", shellArgs, "ok") })
	repeatFailed := twice(func(d *repeat.RepeatDetector) string { return d.Notice("shell", shellArgs, "error: exit 1") })
	repeatAsked := twice(func(d *repeat.RepeatDetector) string { return d.AskedBefore(ask.ToolName, askArgs) })
	repeatAskCall := twice(func(d *repeat.RepeatDetector) string { return d.Notice(ask.ToolName, askArgs, "") })
	sweepD := repeat.NewRepeatDetector(repeatShapes())
	var sweep string
	for i := 1; i <= 12; i++ {
		args := json.RawMessage(fmt.Sprintf(`{"pattern":"needle%d","path":"internal/ui/chat"}`, i))
		sweep = sweepD.Notice("search", args, fmt.Sprintf("a.go:%d: needle%d", i, i))
	}
	for _, n := range []string{repeatOK, repeatFailed} {
		if !repeat.IsRepeatNotice(n) {
			t.Fatalf("a repeat fixture is not a repeat notice: %q", n)
		}
	}

	// The trimmed-result placeholder that carries an evidence id, produced
	// through the exported ElideResult with a store that takes the result.
	archived := agent.ElideResult("shell", string(make([]byte, 2048)), func(tool, content string) (string, bool) {
		return "ev-0001", true
	}, evidence.ElidedWording)

	cases := []struct {
		name string
		text string
		want string
	}{
		{"agent.CheckInWording", agent.CheckInWording(), "f5e14bcfc4bae4a171d0805c9c213db318d5e37bbbd48fbc43c6601a912997b6"},
		{"agent.SteerWording", agent.SteerWording(), "4689cecfb766b2807b6c62c2a60791c577971518853aee0e8811ddde689fd995"},
		{"agent.ProgressPrompt", agent.ProgressPrompt, "40e549509831ca1a4657b922c9228e354e25f167372fa38f84c8485b7a9a39f3"},
		{"agent.CompactInstruction", agent.CompactInstruction, "2739423a65da1800adac33e59879560ab34cbb6aa42297b85f4968c85e1e2820"},
		{"agent.CompactSummaryMessage", agent.CompactSummaryMessage("x"), "9c6a6f3f020d129ead172a73bd2cc32ed150fad9041f5157a000275292860ae5"},
		{"agent.ContinueAfterCeiling", agent.ContinueAfterCeiling, "d0e1e23e8a2ba369979025b64d5660b7bb50b6ec3d711d9d7de3ad9b9c8f928e"},
		{"agent.TruncatedRoundNotice", agent.TruncatedRoundNotice, "0d8ca0396d5c90c48246bb7dcf1bfd1bac2a4375ee7b0c0a39f1c2cc2515d95e"},
		{"agent.CancelledResult", agent.CancelledResult, "8ffebbe19d5c5147f4ad8a672b1230dcda261b29c9ea6bea515b922a7ffd9bb1"},
		{"agent.ElidedResult", agent.ElidedResult, "935a7923fa62de73d977ff01935a4769d5a60acd31c3c59b721e13a3fd73a099"},
		{"agent.ElideResult with evidence (2048 bytes, ev-0001)", archived, "44014644c46f57a018f7ca6bfe4ca2cfd69bddd6dbe5f1496a40de729c849fb4"},
		{"agent.RepeatDetector repeat notice", repeatOK, "772b96b9a0b84c02f26019b4b2af429d4830502af6de6191bfe55716e9811c8e"},
		{"agent.RepeatDetector failed repeat notice", repeatFailed, "83df9bf5336d3aaa6e7d73e55271b7a994a1c58beacc0ce3362b75ddb32b736c"},
		{"agent.RepeatDetector repeated question (AskedBefore)", repeatAsked, "370547092aaa904cf4b2acae241f4811289395e0b47124d4e67dd4607f172498"},
		{"agent.RepeatDetector repeated question (Notice)", repeatAskCall, "040abb4fcd566fb3d2d52f5da2a9bcd910067984d97c269d456852e62e629b5b"},
		{"agent.RepeatDetector sweep notice", sweep, "cd15278b31e655d07b8f0dd21621d171ba5772abe370bcba2ceb09a25950f84f"},
		{"agent.SessionSteerWording", agent.SessionSteerWording(), "7b00096f5f1d70d8a5780d3a79d380d965da5d475df56acedca42d8bd30a0a47"},
		{"agent.Steering.SessionSteer", agent.Steering{}.SessionSteer("slot-1", "the line"), "79038b492c9b2db1ce13e6aed3fdfd22a9c377ca7eca2189e59d11495ef0236a"},
		{"sidejobs.SummaryWording", reading.SummaryWording(), "5492cddecfcb194ec748fb25dc05711693f26e3235290b67e4d7b21b19c043a9"},
		{"skill.PromptBlock", skill.PromptBlock(cat), "05daee882fd95e4e9a980205b08da417285b94cfcd92a847a9320455c5e46491"},
		{"skill.Content", content, "1854ff9780c0e542d11d30ec4e5efcae82f193d69471478bc5e7b4a1c1fa4a97"},
		{"skill.ToolDefinition", jsonOf(t, skill.ToolDefinition(cat)), "87d9c75b6e0e5795def41acba2e25e3c3676922fa70cb6d52a18c9e2dd6060f1"},
		{"secret.PromptBlock(asks)", secretPromptBlock(vault, true), "59dcd9549ef7f72c72b93036ad80c85a1fae55a6f44350e2b7179ac6ae8ec8d2"},
		{"secret.PromptBlock(no asks)", secretPromptBlock(vault, false), "7116ff7e60f5b779a1f31724b95b41587e8941ef083a30a561b89593ec64142b"},
		{"secret.Placeholder", secret.Placeholder("API_TOKEN"), "de263f36f5feb65f6e366be9b98fcbd7ada137c1be9648919c906d4039b82c94"},
		{"secret.Redacted", secret.Redacted("aws-key"), "b90af787c7e43284e037e8111b657e4e1e5ca67e53890684239b9ff4b79993f5"},
		{"memory.PromptBlock", memory.PromptBlock(mem), "2353318efe35b8bff22d1f5ba81e953c8f4573c0bef96acfe5ea0a99730cc4e2"},
		{"memory.ToolDefinition", jsonOf(t, memory.ToolDefinition()), "f0035230ffe203ab5e1299e54e3ef475f24ccf9331d46e19a0a013f83b42d629"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.text == "" {
				t.Fatalf("%s is empty", c.name)
			}
			if got := wordingHash(c.text); got != c.want {
				t.Errorf("%s changed (captured from %s):\n got  %s\n want %s\ntext:\n%s", c.name, wordingsMaster, got, c.want, c.text)
			}
		})
	}
}
