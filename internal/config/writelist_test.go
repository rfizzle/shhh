package config

import (
	"strings"
	"testing"
)

// An entry is added to the list the file itself holds, once, and the preview
// is exactly the text the write then leaves, with nothing written by the
// preview.
func TestListAdd_AddsToTheFilesOwnListAndThePreviewIsTheWrite(t *testing.T) {
	path := writeTemp(t, handWritten)
	e, err := ListAdd(path, "behavior.command_allowlist", "go vet")
	if err != nil || e.Value != "go test, go build, go vet" {
		t.Fatalf("edit = %+v (%v)", e, err)
	}
	before, after, err := Preview(path, e)
	if err != nil || before != handWritten || readBack(t, path) != handWritten {
		t.Fatalf("the preview wrote, or read the wrong text (%v)", err)
	}
	if !strings.Contains(after, `command_allowlist = ["go test", "go build", "go vet"]`) {
		t.Fatalf("after = %s", after)
	}
	if err := Write(path, e); err != nil || readBack(t, path) != after {
		t.Fatalf("the write is not the preview (%v):\n%s", err, readBack(t, path))
	}

	if e, err := ListAdd(path, "behavior.command_allowlist", "go test"); err != nil || e.Value != "go test, go build, go vet" {
		t.Fatalf("an entry already there: %+v (%v)", e, err)
	}
	if e, err := ListAdd(writeTemp(t, ""), "behavior.command_allowlist", "go test"); err != nil || e.Value != "go test" {
		t.Fatalf("a missing file: %+v (%v)", e, err)
	}
	for _, bad := range []struct{ key, entry string }{
		{"behavior.command_allowlist", "a, b"},
		{"behavior.command_allowlist", " "},
		{"provider.model", "x"},
	} {
		if _, err := ListAdd(path, bad.key, bad.entry); err == nil {
			t.Errorf("%s %q was accepted", bad.key, bad.entry)
		}
	}
}
