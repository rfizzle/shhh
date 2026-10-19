package secret

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/attachment"
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
	exec := v.WrapExecutor(func(ctx context.Context, name string, args json.RawMessage) (string, error) {
		if name == "fail" {
			return "", errors.New("bad hunter2hunter2")
		}
		return "token=hunter2hunter2", nil
	})
	out, err := exec(context.Background(), "ok", nil)
	if err != nil || out != "token=[secret:TOKEN]" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := exec(context.Background(), "fail", nil); err == nil || err.Error() != "bad [secret:TOKEN]" {
		t.Fatalf("err=%v", err)
	}
}

func TestScrubMessages_CopiesAndScrubsEveryText(t *testing.T) {
	v := New()
	_ = v.Add("TOKEN", "hunter2hunter2")
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "key hunter2hunter2", Attachments: []attachment.Attachment{
			{Kind: attachment.Text, Data: []byte("hunter2hunter2")},
		}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Arguments: `{"c":"echo hunter2hunter2"}`}}},
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
