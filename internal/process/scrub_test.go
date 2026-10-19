package process

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/agentcore/secret"
	"github.com/rfizzle/shhh/internal/evidence"
)

// A spooled process writes into the evidence store, whose files sit at 0600
// for a week: the scrub has to run before the spool is stored, not only on
// the way to the model.
func TestScrub_ASpooledProcessStoresNoValue(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	const value = "sk-live-0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	v := secret.New()
	if err := v.Add("API_KEY", value); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := evidence.Open(filepath.Join(root, "evidence"), evidence.NewSessionID())
	if err != nil {
		t.Fatalf("evidence.Open: %v", err)
	}

	// A spooled process: it is given the key as an environment variable, so
	// printing it is a command doing what it was told.
	sup, err := New(root, store.Put)
	if err != nil {
		t.Fatalf("New: %v", err)
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
	// One spool per stream. A walk that found no entries at all would pass
	// for the wrong reason.
	if stored != 2 {
		t.Fatalf("walked %d stored entries, expected 2", stored)
	}
}

// The three surfaces the chain has to cover, with nothing declared at all,
// include the spool a long-running process leaves in the evidence store:
// the copy that outlives the session by a week and that nothing on screen
// would report.
func TestRedact_ReachesASpooledProcess(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	v := secret.New()
	root := t.TempDir()
	store, err := evidence.Open(filepath.Join(root, "evidence"), evidence.NewSessionID())
	if err != nil {
		t.Fatalf("evidence.Open: %v", err)
	}
	sup, err := New(root, store.Put)
	if err != nil {
		t.Fatalf("New: %v", err)
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
