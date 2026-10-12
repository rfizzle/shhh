package agent

import (
	"encoding/json"
	"testing"
)

// A grant over one directory answers an edit across files only where every
// file of the list is under it: the first path inside and the second outside
// is a card, not a yes.
func TestModePolicy_AnEditAcrossFilesIsGrantedOnlyWhereEveryFileIs(t *testing.T) {
	p := ModePolicy{Mode: ModeManual, EditDirs: []string{"/w/internal/agent"}}
	for _, tc := range []struct {
		name  string
		paths []string
		want  Decision
	}{
		{"every file under the grant", []string{"/w/internal/agent/a.go", "/w/internal/agent/b.go"}, Allow},
		{"one file outside it", []string{"/w/internal/agent/a.go", "/w/internal/tools/b.go"}, Ask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Action{Kind: ActionEdit, Path: tc.paths[0], Paths: tc.paths}
			if got, _ := p.Decide(a); got != tc.want {
				t.Errorf("Decide = %v, want %v", got, tc.want)
			}
		})
	}
}

// The classification of an edit across files carries every file it names, so
// the policy above is asked about each of them.
func TestClassify_AnEditAcrossFilesCarriesEveryPath(t *testing.T) {
	c, err := ClassifyCall("edit_file", json.RawMessage(`{"paths":["a.go","b.go"],"old_text":"x","new_text":"y"}`), Answers{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Action.Path != "a.go" || len(c.Action.Paths) != 2 || c.Action.Paths[1] != "b.go" {
		t.Errorf("Action = %+v", c.Action)
	}
}
