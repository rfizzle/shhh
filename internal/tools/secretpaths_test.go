package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretMarker = "REFRESH-TOKEN-FROM-ANOTHER-PROCESS"

// secretWorkspace is a workspace that holds both a plain file and, beside it,
// the data directory with a sign-in record, so a search over the workspace
// passes over the record.
func secretWorkspace(t *testing.T) (ws, record string) {
	t.Helper()
	ws = t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(ws, "xdg"))
	record = filepath.Join(ws, "xdg", "shhh", "chatgpt", "credentials.json")
	must(t, os.MkdirAll(filepath.Dir(record), 0o700))
	must(t, os.WriteFile(record, []byte(`{"refresh_token":"`+secretMarker+`"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(ws, "work.json"), []byte(`{"refresh_token":"plain"}`), 0o644))
	return ws, record
}

func runTool(t *testing.T, name string, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	must(t, err)
	return Execute(context.Background(), name, raw)
}

func TestSecretPaths_ReadFileRefusesTheRecordHoweverItIsNamed(t *testing.T) {
	ws, record := secretWorkspace(t)
	link := filepath.Join(ws, "innocent.json")
	must(t, os.Symlink(record, link))
	dirLink := filepath.Join(ws, "dirlink")
	must(t, os.Symlink(filepath.Dir(record), dirLink))

	for name, path := range map[string]string{
		"direct":           record,
		"symlinked file":   link,
		"symlinked dir":    filepath.Join(dirLink, "credentials.json"),
		"dot-dot":          filepath.Join(ws, "xdg", "shhh", "chatgpt", "..", "chatgpt", "credentials.json"),
		"state sibling":    filepath.Join(ws, "xdg", "shhh", "shhh.db"),
		"missing in state": filepath.Join(ws, "xdg", "shhh", "chatgpt", "gone.json"),
	} {
		out, err := runTool(t, ReadFileName, map[string]any{"path": path})
		if err == nil || !strings.Contains(err.Error(), "never shown to the model") {
			t.Errorf("%s: read_file = %q, %v; want the refusal", name, out, err)
		}
		if strings.Contains(out, secretMarker) {
			t.Errorf("%s: the record reached the answer", name)
		}
	}

	if _, err := runTool(t, ReadFileName, map[string]any{"path": filepath.Join(ws, "work.json")}); err != nil {
		t.Errorf("a plain file is still readable: %v", err)
	}
}

func TestSecretPaths_OtherDirectReadersRefuseToo(t *testing.T) {
	_, record := secretWorkspace(t)
	dir := filepath.Dir(record)
	for name, call := range map[string]func() (string, error){
		"search file": func() (string, error) {
			return runTool(t, SearchName, map[string]any{"pattern": "refresh", "path": record})
		},
		"search dir": func() (string, error) {
			return runTool(t, SearchName, map[string]any{"pattern": "refresh", "path": dir})
		},
		"glob":  func() (string, error) { return runTool(t, GlobName, map[string]any{"pattern": "*", "path": dir}) },
		"list":  func() (string, error) { return runTool(t, ListDirectoryName, map[string]any{"path": dir}) },
		"query": func() (string, error) { return runTool(t, QueryName, map[string]any{"paths": []string{record}}) },
		"sqlite": func() (string, error) {
			return runTool(t, SqliteName, map[string]any{"path": record, "sql": "select 1"})
		},
	} {
		out, err := call()
		if err == nil || !strings.Contains(err.Error(), "never shown to the model") || strings.Contains(out, secretMarker) {
			t.Errorf("%s = %q, %v; want the refusal", name, out, err)
		}
	}
}

func TestSecretPaths_AWalkOverAParentSkipsTheRecordSilently(t *testing.T) {
	ws, record := secretWorkspace(t)
	must(t, os.Symlink(record, filepath.Join(ws, "innocent.json")))

	t.Run("search walker", func(t *testing.T) {
		forceWalker(t)
		out, err := runTool(t, SearchName, map[string]any{"pattern": "refresh_token", "path": ws})
		if err != nil || !strings.Contains(out, "work.json") || strings.Contains(out, secretMarker) || strings.Contains(out, "innocent") {
			t.Errorf("search = %q, %v; want only work.json", out, err)
		}
	})
	t.Run("search through ripgrep", func(t *testing.T) {
		fakeRg(t, "printf '"+record+"\\0001:"+secretMarker+"\\n"+filepath.Join(ws, "work.json")+"\\0001:plain\\n'")
		out, err := runTool(t, SearchName, map[string]any{"pattern": "refresh_token", "path": ws})
		if err != nil || !strings.Contains(out, "work.json") || strings.Contains(out, secretMarker) {
			t.Errorf("search = %q, %v; want only work.json", out, err)
		}
	})
	t.Run("glob", func(t *testing.T) {
		out, err := runTool(t, GlobName, map[string]any{"pattern": "**/*.json", "path": ws})
		if err != nil || !strings.Contains(out, "work.json") || strings.Contains(out, "credentials") || strings.Contains(out, "innocent") {
			t.Errorf("glob = %q, %v; want only work.json", out, err)
		}
	})
	t.Run("query glob", func(t *testing.T) {
		out, err := runTool(t, QueryName, map[string]any{"paths": []string{filepath.Join(ws, "**", "*.json")}})
		if err != nil || strings.Contains(out, secretMarker) {
			t.Errorf("query = %q, %v; want the record left out", out, err)
		}
	})
	t.Run("list", func(t *testing.T) {
		out, err := runTool(t, ListDirectoryName, map[string]any{"path": filepath.Join(ws, "xdg"), "depth": 3})
		if err != nil || strings.Contains(out, "credentials") {
			t.Errorf("list = %q, %v; want the state directory left out", out, err)
		}
	})
}
