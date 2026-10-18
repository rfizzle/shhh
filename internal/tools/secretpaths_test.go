package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
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

func TestSecretPaths_AQueryGlobOverTheRecordsDirectoryIsRefused(t *testing.T) {
	_, record := secretWorkspace(t)
	out, err := runTool(t, QueryName, map[string]any{"paths": []string{filepath.Join(filepath.Dir(record), "*.json")}})
	if err == nil || !strings.Contains(err.Error(), "never shown to the model") || strings.Contains(out, secretMarker) {
		t.Errorf("query = %q, %v; want the refusal", out, err)
	}
}

func TestSecretPaths_TheConfigFileIsRefusedAndItsNeighboursAreNot(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(ws, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(ws, "data"))
	cfg := filepath.Join(ws, "cfg", "shhh", "config.toml")
	skill := filepath.Join(ws, "cfg", "shhh", "skills", "x", "SKILL.md")
	must(t, os.MkdirAll(filepath.Dir(skill), 0o755))
	must(t, os.WriteFile(cfg, []byte("api_key = \""+secretMarker+"\"\n"), 0o600))
	must(t, os.WriteFile(skill, []byte("a skill api_key"), 0o644))

	for name, call := range map[string]func() (string, error){
		"read_file": func() (string, error) { return runTool(t, ReadFileName, map[string]any{"path": cfg}) },
		"search": func() (string, error) {
			return runTool(t, SearchName, map[string]any{"pattern": "api_key", "path": cfg})
		},
		"glob": func() (string, error) { return runTool(t, GlobName, map[string]any{"pattern": "*", "path": cfg}) },
	} {
		out, err := call()
		if err == nil || strings.Contains(out, secretMarker) {
			t.Errorf("%s of the config file = %q, %v; want the refusal", name, out, err)
		}
	}
	// A walk over the directory leaves the file out and finds the skill.
	forceWalker(t)
	out, err := runTool(t, SearchName, map[string]any{"pattern": "api_key", "path": filepath.Join(ws, "cfg")})
	if err != nil || strings.Contains(out, secretMarker) || !strings.Contains(out, "SKILL.md") {
		t.Errorf("search over the config directory = %q, %v; want the skill only", out, err)
	}
	if out, err := runTool(t, ReadFileName, map[string]any{"path": skill}); err != nil || !strings.Contains(out, "a skill") {
		t.Errorf("the skill beside the config file is unreadable: %q, %v", out, err)
	}
}

func TestSecretPaths_AHardLinkIsTheSameFile(t *testing.T) {
	ws, record := secretWorkspace(t)
	db := filepath.Join(ws, "xdg", "shhh", "shhh.db")
	must(t, os.WriteFile(db, []byte("SQLite format 3\x00"+secretMarker), 0o600))
	linkRecord, linkDB := filepath.Join(ws, "a.json"), filepath.Join(ws, "b.db")
	must(t, os.Link(record, linkRecord))
	must(t, os.Link(db, linkDB))

	out, err := runTool(t, ReadFileName, map[string]any{"path": linkRecord})
	if err == nil || strings.Contains(out, secretMarker) {
		t.Errorf("read_file of a hard link = %q, %v; want the refusal", out, err)
	}
	out, err = runTool(t, SqliteName, map[string]any{"path": linkDB, "sql": "select 1"})
	if err == nil || !strings.Contains(err.Error(), "never shown to the model") {
		t.Errorf("sqlite over a hard link = %q, %v; want the refusal", out, err)
	}
	// Walks leave the link out as they leave the file.
	forceWalker(t)
	out, err = runTool(t, SearchName, map[string]any{"pattern": "refresh_token", "path": ws})
	if err != nil || strings.Contains(out, secretMarker) || strings.Contains(out, "a.json") {
		t.Errorf("search = %q, %v; want the hard link left out", out, err)
	}
}

func TestSecretPaths_AFileNotYetThereUnderALinkedDirectoryIsJudgedByWhereItLands(t *testing.T) {
	ws, record := secretWorkspace(t)
	link := filepath.Join(ws, "dirlink")
	must(t, os.Symlink(filepath.Dir(record), link))
	args := map[string]any{"path": filepath.Join(link, "new.json"), "content": "x"}
	if _, err := ExecuteMutating(context.Background(), WriteFileName, mustRaw(t, args)); err == nil || !strings.Contains(err.Error(), "never shown to the model") {
		t.Errorf("write_file through a linked directory = %v; want the refusal", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(record), "new.json")); err == nil {
		t.Error("the file was written")
	}
}

func TestSecretPaths_TheMutatingToolsRefuseShhhsFilesToo(t *testing.T) {
	ws, record := secretWorkspace(t)
	plain := filepath.Join(ws, "work.json")
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
	}{
		"write":     {WriteFileName, map[string]any{"path": record, "content": "{}"}},
		"edit":      {EditFileName, map[string]any{"path": record, "edits": []map[string]any{{"old_text": "refresh_token", "new_text": "x"}}}},
		"edit many": {EditFileName, map[string]any{"paths": []string{plain, record}, "edits": []map[string]any{{"old_text": "refresh_token", "new_text": "x"}}}},
	} {
		out, err := ExecuteMutating(context.Background(), tc.tool, mustRaw(t, tc.args))
		if err == nil || !strings.Contains(err.Error(), "never shown to the model") || strings.Contains(out, secretMarker) {
			t.Errorf("%s = %q, %v; want the refusal", name, out, err)
		}
		if _, err := PreviewMutation(tc.tool, mustRaw(t, tc.args)); err == nil {
			t.Errorf("%s: the approval card would have been drawn", name)
		}
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), secretMarker) {
		t.Error("the record was changed")
	}
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return b
}

func TestSecretPaths_TheConfigListIsTheConfigPackages(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got, want := configFilePaths(), config.Paths(); !slices.Equal(got, want) {
		t.Errorf("configFilePaths = %v, config.Paths = %v", got, want)
	}
}
