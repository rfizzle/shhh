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
