package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/secret"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/quality"
)

// The executor chain a session builds: the reducer inside, this package's
// wrap outside. What the chain returns has always been clean; what the
// evidence store writes to disk is the copy that outlives the turn, and
// these are the two tools that fill it.
func evidenceChain(t *testing.T, v *secret.Vault, dir string, result string) (*Store, func(string) string) {
	t.Helper()
	store, err := Open(filepath.Join(dir, "evidence"), NewSessionID())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	red := NewReducer(store)
	red.SetScrub(v.Scrub)
	exec := v.WrapExecutor(red.WrapExecutor(func(context.Context, string, json.RawMessage) (string, error) {
		return result, nil
	}))
	return store, func(tool string) string {
		out, err := exec(context.Background(), tool, nil)
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
	v := secret.New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dotenv := "API_KEY=" + value + "\n" + strings.Repeat("PADDING=xxxxxxxxxxxxxxxxxxxx\n", 300)
	_, call := evidenceChain(t, v, root, dotenv)

	// read_file on the .env the key lives in, and a command that prints it:
	// one is exempt from reduction and one is exactly what reduction is for.
	for _, tool := range []string{"read_file", "execute_command"} {
		if out := call(tool); strings.Contains(out, value) {
			t.Fatalf("%s handed the value back", tool)
		}
	}

	stored := 0
	err := filepath.WalkDir(filepath.Join(root, "evidence"), func(path string, d fs.DirEntry, err error) error {
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
	// The command's stored original. A walk that
	// found no entries at all would pass for the wrong reason.
	if stored != 1 {
		t.Fatalf("walked %d stored entries, expected 1", stored)
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
	v := secret.New()
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
	store, err := Open(dir, NewSessionID())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Wired in the order a session wires it: the gate is built while the
	// toolset is, before the secrets are opened, so it takes the scrub
	// late — a copy taken here would be the nil one.
	gate := &quality.Runner{Workspace: ws, Evidence: store.Put}
	red := NewReducer(store)
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
		if !strings.Contains(text, secret.Placeholder("API_KEY")) {
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
	v := secret.New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}
	idRe := regexp.MustCompile(`ev-[0-9a-f]{16}`)

	page := func(scrubBeforeStore bool, result string) (header, body string) {
		t.Helper()
		store, err := Open(t.TempDir(), NewSessionID())
		if err != nil {
			t.Fatal(err)
		}
		red := NewReducer(store)
		if scrubBeforeStore {
			red.SetScrub(v.Scrub)
		}
		exec := v.WrapExecutor(red.WrapExecutor(func(context.Context, string, json.RawMessage) (string, error) {
			return result, nil
		}))
		out, err := exec(context.Background(), "execute_command", nil)
		if err != nil {
			t.Fatal(err)
		}
		id := idRe.FindString(out)
		if id == "" {
			t.Fatalf("no evidence id in %q", out)
		}
		read, err := exec(context.Background(), "evidence", json.RawMessage(`{"action":"read","id":"`+id+`","limit":16384}`))
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
	if strings.Contains(afterB, value) || !strings.Contains(afterB, secret.Placeholder("API_KEY")) {
		t.Fatalf("paged text = %q", afterB[:80])
	}
	// The header counts the file, and the file is now the scrubbed copy.
	if !strings.HasSuffix(afterH, fmt.Sprintf("of %d:", len(v.Scrub(leaky)))) {
		t.Fatalf("header must count the stored copy: %q", afterH)
	}
}

// The surfaces the chain has to cover, with nothing declared at all:
// what a tool hands back, what goes into a message on its way to a provider,
// the spool a long-running process leaves is covered beside the process.
func TestRedact_ReachesToolOutputAndAMessage(t *testing.T) {
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	v := secret.New()

	root := t.TempDir()
	config := "token = \"" + token + "\"\n" + strings.Repeat("padding = \"xxxxxxxxxxxxxxxxxxxx\"\n", 300)
	_, call := evidenceChain(t, v, root, config)
	for _, tool := range []string{"read_file", "execute_command"} {
		if out := call(tool); strings.Contains(out, token) {
			t.Fatalf("%s handed the token back", tool)
		}
	}

	msgs := v.ScrubMessages([]llm.Message{{Role: "user", Content: "here it is: " + token}})
	if strings.Contains(msgs[0].Content, token) || !strings.Contains(msgs[0].Content, secret.Redacted("github-token")) {
		t.Fatalf("message = %q", msgs[0].Content)
	}
}
