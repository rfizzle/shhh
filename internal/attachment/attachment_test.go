package attachment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// pngBytes is the smallest thing http.DetectContentType calls a PNG.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func TestSniff_ClassifiesByBytesNotExtension(t *testing.T) {
	// A PNG named .txt is still an image: the bytes decide.
	kind, mt, err := Sniff("notes.txt", pngBytes)
	if err != nil {
		t.Fatalf("sniff png: %v", err)
	}
	if kind != provider.AttachmentImage || mt != "image/png" {
		t.Fatalf("got %s/%s, want image/image-png", kind, mt)
	}

	kind, mt, err = Sniff("notes.md", []byte("# hello\n"))
	if err != nil {
		t.Fatalf("sniff text: %v", err)
	}
	if kind != provider.AttachmentText || mt != "text/markdown" {
		t.Fatalf("got %s/%s, want text/text-markdown", kind, mt)
	}

	kind, _, err = Sniff("doc.pdf", []byte("%PDF-1.7\n%âãÏÓ\n"))
	if err != nil {
		t.Fatalf("sniff pdf: %v", err)
	}
	if kind != provider.AttachmentDocument {
		t.Fatalf("got %s, want document", kind)
	}
}

func TestSniff_RefusesBytesItCannotCarry(t *testing.T) {
	// An ELF header: binary, not an image, not text.
	if _, _, err := Sniff("a.out", []byte("\x7fELF\x02\x01\x01\x00\x00\x00")); err == nil {
		t.Fatal("expected a refusal for an unsupported binary")
	}
}

func TestFromFile_RefusesWhatIsTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, make([]byte, MaxTextBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := FromFile(path)
	if err == nil {
		t.Fatal("expected a refusal for an oversized text attachment")
	}
	if !strings.Contains(err.Error(), "big.txt") {
		t.Fatalf("the refusal should name the file: %v", err)
	}
}

func TestFromFile_NamesTheBaseNameOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "shot.png" {
		t.Fatalf("name = %q, want the base name — the transcript must not leak the path", a.Name)
	}
	if a.Kind != provider.AttachmentImage {
		t.Fatalf("kind = %s, want image", a.Kind)
	}
}

func TestLooksLikeFile_OnlyOneExistingLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "with space.png")
	if err := os.WriteFile(path, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// A dragged path arrives quoted or backslash-escaped.
	for _, pasted := range []string{path, `"` + path + `"`, strings.ReplaceAll(path, " ", `\ `)} {
		if got, ok := LooksLikeFile(pasted); !ok || got != path {
			t.Fatalf("LooksLikeFile(%q) = %q, %v; want the resolved path", pasted, got, ok)
		}
	}
	// Prose, a missing path, and a directory are not attachments.
	for _, pasted := range []string{"look at " + path + "\nand this", filepath.Join(dir, "nope.png"), dir} {
		if _, ok := LooksLikeFile(pasted); ok {
			t.Fatalf("LooksLikeFile(%q) = true, want false", pasted)
		}
	}
}

func TestExpand_ResolvesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := Expand("~/shot.png"); got != filepath.Join(home, "shot.png") {
		t.Fatalf("Expand(~/shot.png) = %q", got)
	}
}

func TestSummarizeAndNames(t *testing.T) {
	atts := []provider.Attachment{
		{Name: "a.png", Data: make([]byte, 2048)},
		{Name: "b.png", Data: make([]byte, 2048)},
	}
	if got, want := Summarize(atts), "2 attachments · 4 KB"; got != want {
		t.Fatalf("Summarize = %q, want %q", got, want)
	}
	if got, want := Summarize(atts[:1]), "1 attachment · 2 KB"; got != want {
		t.Fatalf("Summarize = %q, want %q", got, want)
	}
	if Summarize(nil) != "" {
		t.Fatal("an empty set has nothing to say")
	}
	names := Names(atts)
	if len(names) != 2 || names[0] != "a.png (2 KB)" {
		t.Fatalf("Names = %v", names)
	}
}
