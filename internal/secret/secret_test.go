package secret

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
)

func TestAdd_ValidatesNameAndValue(t *testing.T) {
	v := New()
	cases := []struct {
		name, value string
		wantErr     bool
	}{
		{"API_KEY", "x", false},
		{"lower_ok", "x", false},
		{"1BAD", "x", true},
		{"WITH-DASH", "x", true},
		{"PATH", "x", true},
		{"EMPTY", "", true},
	}
	for _, tc := range cases {
		err := v.Add(tc.name, tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("Add(%q, %q) err=%v, wantErr=%v", tc.name, tc.value, err, tc.wantErr)
		}
	}
	if got := v.Names(); strings.Join(got, ",") != "API_KEY,lower_ok" {
		t.Fatalf("Names = %v", got)
	}
}

func TestAdd_ReplacesSameName(t *testing.T) {
	v := New()
	_ = v.Add("K", "first-value")
	_ = v.Add("K", "second-value")
	if v.Len() != 1 {
		t.Fatalf("Len = %d, want 1", v.Len())
	}
	if got := v.Scrub("first-value second-value"); got != "first-value [secret:K]" {
		t.Fatalf("Scrub = %q", got)
	}
	if !v.Remove("K") || v.Remove("K") {
		t.Fatal("Remove should succeed once")
	}
}

func TestScrub_WholeValueAndEncodings(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	v := New()
	if err := v.Add("STRIPE", key); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		key,
		base64.StdEncoding.EncodeToString([]byte(key)),
		base64.RawURLEncoding.EncodeToString([]byte(key)),
		hex.EncodeToString([]byte(key)),
		strings.ToUpper(hex.EncodeToString([]byte(key))),
	}
	for _, in := range cases {
		got := v.Scrub("out: " + in + " done")
		if got != "out: [secret:STRIPE] done" {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
}

func TestScrub_Fragments(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	v := New()
	_ = v.Add("STRIPE", key)
	cases := map[string]string{
		"prefix " + key[:12] + " end":          "prefix [secret:STRIPE] end",
		"tail " + key[len(key)-10:]:            "tail [secret:STRIPE]",
		key[:14] + "\n" + key[14:]:             "[secret:STRIPE]\n[secret:STRIPE]",
		"short " + key[:7] + " stays":          "short " + key[:7] + " stays",
		"nothing to see":                       "nothing to see",
		"twice " + key[:9] + " and " + key[3:]: "twice [secret:STRIPE] and [secret:STRIPE]",
	}
	for in, want := range cases {
		if got := v.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

// Five secrets are one walk of the text, not five, so the case worth a test
// is the one the per-entry version got for free: fragments of different
// secrets interleaved, each keeping its own name.
func TestScrub_ManySecretsInOnePass(t *testing.T) {
	v := New()
	values := map[string]string{
		"ALPHA": "alpha-0123456789abcdef",
		"BRAVO": "bravo-fedcba9876543210",
		"DELTA": "delta-zyxwvutsrqponmlk",
	}
	for name, value := range values {
		if err := v.Add(name, value); err != nil {
			t.Fatal(err)
		}
	}
	in := "a " + values["BRAVO"][:11] + " b " + values["DELTA"][4:] + " c " + values["ALPHA"] + " d"
	want := "a [secret:BRAVO] b [secret:DELTA] c [secret:ALPHA] d"
	if got := v.Scrub(in); got != want {
		t.Fatalf("Scrub = %q, want %q", got, want)
	}
}

// Remove rebuilds the shared map, and the failure it can have is silent in
// the direction that matters: a window left behind scrubs a value nobody
// declared any more, and one dropped by mistake stops scrubbing a value
// that is still declared.
func TestScrub_RemoveRebuildsTheSharedWindows(t *testing.T) {
	v := New()
	_ = v.Add("GONE", "gone-0123456789abcdef")
	_ = v.Add("KEPT", "kept-fedcba9876543210")
	if !v.Remove("GONE") {
		t.Fatal("Remove should have found GONE")
	}
	in := "gone-0123456789 and kept-fedcba98"
	if got := v.Scrub(in); got != "gone-0123456789 and [secret:KEPT]" {
		t.Fatalf("Scrub = %q", got)
	}
	if v.Remove("KEPT"); v.Scrub(in) != in {
		t.Fatalf("an empty vault still scrubbed %q", in)
	}
}

// The scrub reads a snapshot of the index without holding the lock, which
// is only sound because Add and Remove build a new one over a copy of the
// entries rather than editing the one a scrub is walking. Under -race this
// is what says so.
func TestScrub_AddsWhileScrubbing(t *testing.T) {
	v := New()
	_ = v.Add("FIRST", "first-0123456789abcdef")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			if err := v.Add(fmt.Sprintf("S%d", i), fmt.Sprintf("value-%03d-abcdefghij", i)); err != nil {
				t.Error(err)
				return
			}
			v.Remove(fmt.Sprintf("S%d", i-1))
		}
	}()
	for range 200 {
		if got := v.Scrub("log first-0123456789 line"); got != "log [secret:FIRST] line" {
			t.Fatalf("Scrub = %q", got)
		}
	}
	<-done
}

// The prefilter that makes text with no secret in it cheap is also the way
// the fragment scrub gets silently disabled: a window whose gate bit is
// clear is never looked up, and nothing on screen would show it. This is
// the same failure the shape markers have, and it is asserted the same way.
func TestScrubIndex_TheGateNeverTurnsAwayAWindow(t *testing.T) {
	v := New()
	for name, value := range map[string]string{
		"A": "sk-live-0123456789abcdef",
		"B": "a\x00b\xff\xfe\xfd\xfc\xfb\xfa\xf9",
		"C": strings.Repeat("z", minFragment),
	} {
		if err := v.Add(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for w := range v.idx.windows {
		k := gateBit(w[0], w[1])
		if v.idx.gate[k>>6]&(1<<(k&63)) == 0 {
			t.Errorf("%q is in the map and the gate turns it away", w)
		}
	}
}

func TestScrub_ShortSecretsMatchWhole(t *testing.T) {
	v := New()
	_ = v.Add("PIN", "1234")
	if got := v.Scrub("pin is 1234, not 123"); got != "pin is [secret:PIN], not 123" {
		t.Fatalf("Scrub = %q", got)
	}
}

func TestScrub_NilVaultIsIdentity(t *testing.T) {
	var v *Vault
	if got := v.Scrub("anything"); got != "anything" {
		t.Fatal("nil vault must not change text")
	}
	if v.Len() != 0 || v.Names() != nil || v.Environ() != nil {
		t.Fatal("nil vault holds nothing")
	}
}

func TestEnviron(t *testing.T) {
	v := New()
	_ = v.Add("B", "2")
	_ = v.Add("A", "1")
	if got := strings.Join(v.Environ(), " "); got != "B=2 A=1" {
		t.Fatalf("Environ = %q", got)
	}
}

func TestWrapExecutor_ScrubsResultsAndErrors(t *testing.T) {
	v := New()
	_ = v.Add("TOKEN", "hunter2hunter2")
	exec := v.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		if name == "fail" {
			return "", errors.New("bad hunter2hunter2")
		}
		return "token=hunter2hunter2", nil
	})
	out, err := exec("ok", nil)
	if err != nil || out != "token=[secret:TOKEN]" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := exec("fail", nil); err == nil || err.Error() != "bad [secret:TOKEN]" {
		t.Fatalf("err=%v", err)
	}
}

func TestScrubMessages_CopiesAndScrubsEveryText(t *testing.T) {
	v := New()
	_ = v.Add("TOKEN", "hunter2hunter2")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "key hunter2hunter2", Attachments: []provider.Attachment{
			{Kind: provider.AttachmentText, Data: []byte("hunter2hunter2")},
		}},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Arguments: `{"c":"echo hunter2hunter2"}`}}},
	}
	got := v.ScrubMessages(msgs)
	if got[0].Content != "key [secret:TOKEN]" || string(got[0].Attachments[0].Data) != "[secret:TOKEN]" {
		t.Fatalf("user message not scrubbed: %+v", got[0])
	}
	if got[1].ToolCalls[0].Arguments != `{"c":"echo [secret:TOKEN]"}` {
		t.Fatalf("tool call not scrubbed: %+v", got[1])
	}
	if msgs[0].Content != "key hunter2hunter2" || msgs[1].ToolCalls[0].Arguments != `{"c":"echo hunter2hunter2"}` {
		t.Fatal("original messages must be untouched")
	}
}

func TestPromptBlock(t *testing.T) {
	if PromptBlock(nil) != "" || PromptBlock(New()) != "" {
		t.Fatal("no secrets, no block")
	}
	v := New()
	_ = v.Add("API_KEY", "value-value-value")
	block := PromptBlock(v)
	for _, want := range []string{"$API_KEY", "[secret:API_KEY]", "## Secrets"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "value-value-value") {
		t.Fatal("the prompt block must never carry a value")
	}
}

// The executor chain a session builds: the reducer inside, this package's
// wrap outside. What the chain returns has always been clean; what the
// evidence store writes to disk is the copy that outlives the turn, and
// these are the two tools that fill it.
func evidenceChain(t *testing.T, v *Vault, dir string, result string) (*evidence.Store, func(string) string) {
	t.Helper()
	store, err := evidence.Open(filepath.Join(dir, "evidence"), evidence.NewSessionID())
	if err != nil {
		t.Fatalf("evidence.Open: %v", err)
	}
	red := evidence.NewReducer(store)
	red.SetScrub(v.Scrub)
	exec := v.WrapExecutor(red.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return result, nil
	}))
	return store, func(tool string) string {
		out, err := exec(tool, nil)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}
}

// The store's files sit at 0600 for a week. A scrub that runs only on the
// way to the model leaves the value in every one of them, which is the
// leak that lasts longest and the one nothing on screen reports.
func TestScrub_NothingUnderTheEvidenceDirectoryHoldsAValue(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	const value = "sk-live-0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	v := New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dotenv := "API_KEY=" + value + "\n" + strings.Repeat("PADDING=xxxxxxxxxxxxxxxxxxxx\n", 300)
	store, call := evidenceChain(t, v, root, dotenv)

	// read_file on the .env the key lives in, and a command that prints it:
	// one is exempt from reduction and one is exactly what reduction is for.
	for _, tool := range []string{"read_file", "execute_command"} {
		if out := call(tool); strings.Contains(out, value) {
			t.Fatalf("%s handed the value back", tool)
		}
	}

	// A spooled process: it is given the key as an environment variable, so
	// printing it is a command doing what it was told.
	sup, err := process.New(root, store.Put)
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}
	t.Cleanup(sup.Close)
	sup.SetEnv([]string{"API_KEY=" + value})
	sup.SetScrub(v.Scrub)
	if _, err := sup.Execute(json.RawMessage(`{"action":"start","name":"printer","command":"printf 'key=%s' \"$API_KEY\"; printf 'key=%s' \"$API_KEY\" 1>&2"}`)); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := sup.Execute(json.RawMessage(`{"action":"status","name":"printer"}`))
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if strings.Contains(out, "full log: evidence ev-") {
			if strings.Contains(out, value) {
				t.Fatal("the status block handed the value back")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the spool to be stored: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stored := 0
	err = filepath.WalkDir(filepath.Join(root, "evidence"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".dat" {
			stored++
		}
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("%s holds the value", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The command's stored original and one spool per stream. A walk that
	// found no entries at all would pass for the wrong reason.
	if stored != 3 {
		t.Fatalf("walked %d stored entries, expected 3", stored)
	}
}

// The quality gate is the third writer into the evidence store and the one
// nothing scrubs on the way past: a check's whole output is stored under an
// id of its own, and the excerpt of it that /gate result prints never goes
// through the executor chain. Checks run with shhh's own environment, which
// is where the values were loaded from, so a check that echoes what it was
// configured with prints one.
func TestScrub_AQualityCheckStoresNoValue(t *testing.T) {
	const value = "sk-live-0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	t.Setenv("API_KEY", value)
	v := New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".shhh"), 0o755); err != nil {
		t.Fatal(err)
	}
	suite := `{"suites": {"default": {"checks": [{"name": "leaky", "exe": "sh",
		"args": ["-c", "printf 'configured with %s\\n' \"$API_KEY\"; exit 1"]}]}}}`
	if err := os.WriteFile(filepath.Join(ws, ".shhh", "quality.json"), []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "evidence")
	store, err := evidence.Open(dir, evidence.NewSessionID())
	if err != nil {
		t.Fatalf("evidence.Open: %v", err)
	}
	// Wired in the order a session wires it: the gate is built while the
	// toolset is, before the secrets are opened, so it takes the scrub
	// late — a copy taken here would be the nil one.
	gate := &quality.Runner{Workspace: ws, Evidence: store.Put}
	red := evidence.NewReducer(store)
	gate.SetScrub(red.Scrub)
	red.SetScrub(v.Scrub)

	res, err := gate.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("gate run: %v", err)
	}
	if res.Verdict != quality.VerdictFail {
		t.Fatalf("verdict = %s (%s)", res.Verdict, res.Reason)
	}
	for what, text := range map[string]string{"the result": res.Format(res.Fingerprint), "/gate result": gate.Status()} {
		if strings.Contains(text, value) {
			t.Fatalf("%s handed the value back", what)
		}
		if !strings.Contains(text, Placeholder("API_KEY")) {
			t.Fatalf("%s must name the secret it held:\n%s", what, text)
		}
	}

	stored := 0
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".dat" {
			stored++
		}
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("%s holds the value", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The check's capture, and only it. A walk that found nothing at all
	// would pass for the wrong reason.
	if stored != 1 {
		t.Fatalf("walked %d stored entries, expected 1", stored)
	}
}

// The on-read scrub stays, so the change is invisible to the model: what it
// pages back is the same text, produced now by the file rather than by the
// wrap around the read.
func TestScrub_EvidenceToolPagesTheSameTextEitherWay(t *testing.T) {
	const value = "sk-live-0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	v := New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}
	idRe := regexp.MustCompile(`ev-[0-9a-f]{16}`)

	page := func(scrubBeforeStore bool, result string) (header, body string) {
		t.Helper()
		store, err := evidence.Open(t.TempDir(), evidence.NewSessionID())
		if err != nil {
			t.Fatal(err)
		}
		red := evidence.NewReducer(store)
		if scrubBeforeStore {
			red.SetScrub(v.Scrub)
		}
		exec := v.WrapExecutor(red.WrapExecutor(func(string, json.RawMessage) (string, error) {
			return result, nil
		}))
		out, err := exec("execute_command", nil)
		if err != nil {
			t.Fatal(err)
		}
		id := idRe.FindString(out)
		if id == "" {
			t.Fatalf("no evidence id in %q", out)
		}
		read, err := exec("evidence", json.RawMessage(`{"action":"read","id":"`+id+`","limit":16384}`))
		if err != nil {
			t.Fatal(err)
		}
		h, b, _ := strings.Cut(read, "\n")
		return idRe.ReplaceAllString(h, "ev-x"), b
	}

	clean := strings.Repeat("nothing secret on this line at all\n", 200)
	beforeH, beforeB := page(false, clean)
	afterH, afterB := page(true, clean)
	if beforeH != afterH || beforeB != afterB {
		t.Fatalf("output with no secret in it must not change:\n%s\n%s", beforeH, afterH)
	}

	leaky := "API_KEY=" + value + "\n" + clean
	_, beforeB = page(false, leaky)
	afterH, afterB = page(true, leaky)
	if beforeB != afterB {
		t.Fatal("the paged text must be what the on-read scrub produced before")
	}
	if strings.Contains(afterB, value) || !strings.Contains(afterB, Placeholder("API_KEY")) {
		t.Fatalf("paged text = %q", afterB[:80])
	}
	// The header counts the file, and the file is now the scrubbed copy.
	if !strings.HasSuffix(afterH, fmt.Sprintf("of %d:", len(v.Scrub(leaky)))) {
		t.Fatalf("header must count the stored copy: %q", afterH)
	}
}

// fixtures are one well-shaped example per entry in the table, with the kind
// each must come back as. The values are structurally real and belong to
// nobody: AWS's own documentation example key, and tokens built to the
// published prefixes and lengths.
var fixtures = []struct{ kind, text string }{
	{"aws-access-key", "AKIAIOSFODNN7EXAMPLE"},
	{"aws-access-key", "ASIAY34FZKBOKMUTVV7A"},
	{"github-token", "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"},
	{"github-token", "gho_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"},
	{"github-token", "github_pat_11ABCDEFG0abcdefghijkl_" + strings.Repeat("Z", 59)},
	{"slack-token", "xoxb-263594206564-2343594206574-FGqmpXTNWtEjIvJdrHFMnzYN"},
	{"slack-token", "xapp-1-A02UBRENZ8L-2412342342342-fd4d9a1b0c8e7f6a5b4c3d2e1f0a9b8c"},
	{"gitlab-token", "glpat-tAHOV29gnuBIPW3ahovC"},
	{"anthropic-key", "sk-ant-api03-ahovCJQX4bipwDKRY5cjqxELSZ6dkryFMT07elszGNU18fmtAHOV29gnuBIPW3ahovCJQX4bipwDKRY5cjqxELSZ6dkryAA"},
	{"openai-key", "sk-proj-dkryFMT07elszGNU18fmtAHOV29gnuBIPW3ahovC"},
	{"openai-key", "sk-fmtAHOV29gnuBIPW3ahovCJQX4bipwDKRY5cjqxELSZ6dkry"},
	{"google-api-key", "AIzalszGNU18fmtAHOV29gnuBIPW3ahovCJQX4b"},
	{"stripe-key", "sk_live_nuBIPW3ahovCJQX4bipwDKRY"},
	{"npm-token", "npm_ryFMT07elszGNU18fmtAHOV29gnuBIPW3aho"},
	{"sendgrid-key", "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"},
	// The header word is part of the match, because this is the one row with
	// no marker of the issuer's own: what makes the run a credential is that
	// it is being presented as one.
	{"bearer-token", "Bearer FMT07elszGNU18fmtAHOV29gnuBIPW3ahovCJQX4"},
	{"jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
	{"private-key", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAKj34GkxFhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\nKUpRKfFLfRYC9AIKjbJTWit+CqvjWYzvQwECAwEAAQ==\n-----END RSA PRIVATE KEY-----"},
}

// Every shape in the table is recognised on its own, with nothing declared:
// the vault's own pass answers "hide this value" and needs somebody to have
// named it, and the whole point of this one is the credential nobody did.
func TestRedact_EveryShapeInTheTable(t *testing.T) {
	for _, f := range fixtures {
		got := New().Scrub("before " + f.text + " after")
		if want := "before " + Redacted(f.kind) + " after"; got != want {
			t.Errorf("%s:\n got %q\nwant %q", f.kind, got, want)
		}
	}
}

// The prefilter that keeps this pass cheap is also the way a new shape gets
// silently disabled: a marker no match can contain is an optimisation, and a
// marker that is merely wrong is a pattern that never runs. Every shape has
// to have a fixture, and every fixture has to carry the shape's markers.
func TestRedact_EveryShapeIsReachableThroughItsMarkers(t *testing.T) {
	covered := map[string]bool{}
	for _, f := range fixtures {
		covered[f.kind] = true
		for _, sh := range shapes {
			if sh.kind != f.kind {
				continue
			}
			if !sh.matches(f.text) {
				t.Errorf("%s: the markers turn away a value the pattern matches", sh.kind)
			}
		}
	}
	for _, sh := range shapes {
		if !covered[sh.kind] {
			t.Errorf("%s has no fixture, so nothing would notice if it stopped matching", sh.kind)
		}
	}
}

// The failure this table can actually have, and the reason every entry is a
// marker the issuer put there rather than a guess at entropy. A JWT is three
// base64 runs separated by dots, which is also an import path, a dotted
// identifier and a version number; without the `eyJ` header prefix this pass
// would take the module line out of a build error and leave the model
// debugging text it cannot read.
func TestRedact_LeavesOrdinaryTextAlone(t *testing.T) {
	ordinary := []string{
		"github.com/rfizzle/shhh/internal/secret",
		"gopkg.in/yaml.v3 v3.0.1",
		"require github.com/spf13/cobra v1.10.1 // indirect",
		"go1.24.3 linux/amd64",
		"cmd.Env.Contains.Something",
		"THISISNOTANACCESSKEYATALL",
		"ghp_short",
		"xoxb-1",
		"xoxo-hugs-and-kisses-everyone",
		// `sk-` is three characters and the start of any kebab-case name, so
		// every guard on the OpenAI row is one of these: a slug is hyphenated
		// where the bare key is not, and neither reaches the length floor by
		// being long.
		"sk-lint-rules-for-the-whole-repository",
		"github.com/anthropics/anthropic-sdk-go v1.13.0",
		"sk-ant-api03",
		"AIzaSyShortEnoughToBeAnExample",
		// The environment npm itself puts in the process, and the header a
		// script writes before the shell expands anything into it.
		"npm_config_registry=https://registry.npmjs.org/",
		"npm_lifecycle_event",
		"glpat-short",
		"SG.1",
		"sk_live_short",
		"xapp-1",
		"authorization: Bearer $GITHUB_TOKEN",
		"the bearer of this message is authorised",
	}
	for _, text := range ordinary {
		if got := New().Scrub(text); got != text {
			t.Errorf("%q was rewritten to %q", text, got)
		}
	}
}

// The failure a fixed-length pattern has: a key with no delimiter after it
// matches its own length and stops, and what is left sits beside the
// placeholder reading as though it had been redacted. Both length-driven rows
// consume the whole run instead, which over-redacts a few characters that
// were never the key and never leaves part of one behind.
func TestRedact_ALengthDrivenShapeTakesTheWholeRun(t *testing.T) {
	for _, key := range []string{
		"AIzalszGNU18fmtAHOV29gnuBIPW3ahovCJQX4b",
		"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",
	} {
		got := New().Scrub(key + "TRAILINGRUNWITHNODELIMITER")
		if strings.Contains(got, "TRAILING") || strings.Contains(got, "RUNWITH") {
			t.Errorf("%.12s…: the tail was left beside the placeholder: %q", key, got)
		}
	}
}

// The bearer row is the one that matches by presentation rather than by an
// issuer's marker, so it is the one that can take a name away: a token that
// arrived in a header still belongs to a family, and `[redacted:jwt]` tells
// the reader which of their integrations was talking. It runs last for
// exactly this, and nothing in the table's order says so out loud.
func TestRedact_ATokenInAHeaderKeepsItsOwnFamilyName(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	got := New().Scrub("authorization: Bearer " + jwt)
	if want := "authorization: Bearer " + Redacted("jwt"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The order of the two passes, stated as the thing a reader would notice: a
// declared token comes back named, so the reader can tell which of their
// secrets was used. Redacting it by kind first would throw that away for
// every credential somebody bothered to declare.
func TestScrub_ADeclaredValueIsReplacedByNameAndNotByKind(t *testing.T) {
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	v := New()
	if err := v.Add("GITHUB_TOKEN", token); err != nil {
		t.Fatal(err)
	}
	got := v.Scrub("authorization: bearer " + token)
	if want := "authorization: bearer " + Placeholder("GITHUB_TOKEN"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, Redacted("github-token")) {
		t.Fatal("a declared value must keep its name")
	}
}

// The three surfaces the chain has to cover, with nothing declared at all:
// what a tool hands back, what goes into a message on its way to a provider,
// and the spool a long-running process leaves in the evidence store — the
// copy that outlives the session by a week and that nothing on screen would
// report.
func TestRedact_ReachesToolOutputAMessageAndASpool(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	v := New()

	root := t.TempDir()
	config := "token = \"" + token + "\"\n" + strings.Repeat("padding = \"xxxxxxxxxxxxxxxxxxxx\"\n", 300)
	store, call := evidenceChain(t, v, root, config)
	for _, tool := range []string{"read_file", "execute_command"} {
		if out := call(tool); strings.Contains(out, token) {
			t.Fatalf("%s handed the token back", tool)
		}
	}

	msgs := v.ScrubMessages([]provider.Message{{Role: "user", Content: "here it is: " + token}})
	if strings.Contains(msgs[0].Content, token) || !strings.Contains(msgs[0].Content, Redacted("github-token")) {
		t.Fatalf("message = %q", msgs[0].Content)
	}

	sup, err := process.New(root, store.Put)
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}
	t.Cleanup(sup.Close)
	sup.SetScrub(v.Scrub)
	start := fmt.Sprintf(`{"action":"start","name":"printer","command":"printf 'token=%%s' '%s'"}`, token)
	if _, err := sup.Execute(json.RawMessage(start)); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := sup.Execute(json.RawMessage(`{"action":"status","name":"printer"}`))
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if strings.Contains(out, "full log: evidence ev-") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the spool to be stored: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	err = filepath.WalkDir(filepath.Join(root, "evidence"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(token)) {
			t.Errorf("%s holds the token", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The mask reads a name and nothing else, because the credentials it is for
// are the ones no value was ever declared for.
func TestMaskedEnvName(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "STRIPE_SECRET", "npm_token"} {
		if !MaskedEnvName(name) {
			t.Errorf("%s should be masked", name)
		}
	}
	for _, name := range []string{"PATH", "HOME", "SHELL", "KEYBOARD", "TOKENIZER", "SECRETS_DIR"} {
		if MaskedEnvName(name) {
			t.Errorf("%s should not be masked", name)
		}
	}
}

// The mask's symptom is a variable that is simply unset, which is exactly
// what a machine nobody configured looks like — so the block says the mask
// is there even when no secret was declared, and says what the other
// placeholder means whatever else it says.
func TestPromptBlock_SaysTheMaskAndTheRedactionExist(t *testing.T) {
	masked := New()
	masked.SetEnvMask(true)
	block := PromptBlock(masked)
	for _, want := range []string{"## Secrets", "_TOKEN", Redacted("kind")} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}

	v := New()
	v.SetEnvMask(true)
	if err := v.Add("API_KEY", "value-value-value"); err != nil {
		t.Fatal(err)
	}
	block = PromptBlock(v)
	for _, want := range []string{"$API_KEY", Placeholder("API_KEY"), "_SECRET", Redacted("kind")} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
}

// logCorpus is credential-free text of the shape the scrub actually runs
// over: a dev server's stdout, streaming past at whatever rate it writes.
// Nothing in it matches a shape or a secret, because that is the case worth
// measuring — a hit is rare and bounded, and the cost that matters is the
// one paid on every byte that is not one.
func logCorpus(size int) string {
	var b strings.Builder
	b.Grow(size + 128)
	paths := []string{"/api/v1/users", "/api/v1/orders/4821", "/static/app.js", "/healthz", "/api/v1/search?q=widget"}
	levels := []string{"INFO", "DEBUG", "WARN"}
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "2026-09-08T14:%02d:%02d.%03dZ %s server: %s %s 200 %dms upstream=api-%d.internal pid=%d\n",
			i%60, (i*7)%60, i%1000, levels[i%len(levels)], []string{"GET", "POST"}[i%2],
			paths[i%len(paths)], 3+i%180, i%8, 40000+i%900)
	}
	return b.String()
}

var scrubSink string

// BenchmarkScrub is the acceptance measurement for the scrub's cost: the
// same log text through a vault with nothing declared and through one with
// five secrets. The first is the shape prefilter alone and the second is
// what a session with secrets pays on top of it; the ratio between them is
// what the output goroutine spends on a stream nobody declared anything for.
func BenchmarkScrub(b *testing.B) {
	text := logCorpus(2_700_000)
	values := []string{
		"sk-ant-api03-9Qw3RtY6uIoP0aSdFgHjKlZxCvBnM4eR7tY2uI9oP1aSdFgH",
		"ghp_ZxCvBnM4eR7tY2uI9oP1aSdFgHjKlQw3RtY6",
		"AKIAQW3RTY6UIOP0ASDF",
		"glpat-M4eR7tY2uI9oP1aSdFgH",
		"xoxb-263594206564-2343594206574-QwErTyUiOpAsDfGhJkLzXcVb",
	}
	for _, n := range []int{0, 5} {
		b.Run(fmt.Sprintf("secrets=%d", n), func(b *testing.B) {
			v := New()
			for i := range n {
				if err := v.Add(fmt.Sprintf("SECRET_%d", i), values[i]); err != nil {
					b.Fatal(err)
				}
			}
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for b.Loop() {
				scrubSink = v.Scrub(text)
			}
		})
	}
}
