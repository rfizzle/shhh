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

func TestSniff_AudioKeepsTheNameItsFormatIsListedUnder(t *testing.T) {
	// The sniffing rules name a RIFF/WAVE file `audio/wave` and an Ogg
	// stream `application/ogg`; no vendor's list of accepted formats has
	// either on it, so what is carried is the name the lists do use.
	for _, tc := range []struct {
		name  string
		data  []byte
		media string
	}{
		{"clip.wav", []byte("RIFF\x00\x00\x00\x00WAVE"), "audio/wav"},
		{"call.ogg", []byte("OggS\x00"), "audio/ogg"},
		{"memo.mp3", []byte("ID3\x04\x00\x00"), "audio/mpeg"},
		{"take.aiff", []byte("FORM\x00\x00\x00\x00AIFF"), "audio/aiff"},
	} {
		kind, mt, err := Sniff(tc.name, tc.data)
		if err != nil {
			t.Fatalf("sniff %s: %v", tc.name, err)
		}
		if kind != provider.AttachmentAudio || mt != tc.media {
			t.Fatalf("%s got %s/%s, want audio/%s", tc.name, kind, mt, tc.media)
		}
	}
	// A name is not evidence: renaming a binary does not make it a recording.
	if _, _, err := Sniff("memo.mp3", []byte("\x7fELF\x02\x01\x01\x00\x00\x00")); err == nil {
		t.Fatal("an extension alone should not carry bytes inline")
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

// The threshold is two questions asked of the same text, and either one
// answering yes is enough: a log is tall, and a minified bundle is one line
// nobody can read the end of.
func TestPasteOverflows_HeightOrWidth(t *testing.T) {
	for _, c := range []struct {
		name    string
		text    string
		lines   int
		columns int
		want    bool
	}{
		{"empty", "", DefaultPasteLines, DefaultPasteColumns, false},
		{"a sentence", "fix the round limit", DefaultPasteLines, DefaultPasteColumns, false},
		{"exactly the line limit", strings.Repeat("x\n", 9) + "x",
			DefaultPasteLines, DefaultPasteColumns, false},
		{"a trailing newline opens no line", strings.Repeat("x\n", 10),
			DefaultPasteLines, DefaultPasteColumns, false},
		{"one line past it", strings.Repeat("x\n", 10) + "x",
			DefaultPasteLines, DefaultPasteColumns, true},
		{"exactly the column limit", strings.Repeat("x", 1000),
			DefaultPasteLines, DefaultPasteColumns, false},
		{"one column past it", strings.Repeat("x", 1001),
			DefaultPasteLines, DefaultPasteColumns, true},
		{"wide characters are counted in columns", strings.Repeat("世", 501),
			DefaultPasteLines, DefaultPasteColumns, true},
		{"height turned off", strings.Repeat("x\n", 40), -1, DefaultPasteColumns, false},
		{"width turned off", strings.Repeat("x", 4000), DefaultPasteLines, -1, false},
		{"both turned off", strings.Repeat("x\n", 40), -1, -1, false},
	} {
		if got := PasteOverflows(c.text, c.lines, c.columns); got != c.want {
			t.Errorf("%s: PasteOverflows = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLineCount_ATrailingNewlineOpensNoLine(t *testing.T) {
	for _, c := range []struct {
		data []byte
		want int
	}{
		{nil, 0},
		{[]byte("a"), 1},
		{[]byte("a\n"), 1},
		{[]byte("a\nb"), 2},
		{[]byte("a\nb\n"), 2},
		{[]byte("\n"), 1},
	} {
		if got := LineCount(c.data); got != c.want {
			t.Errorf("LineCount(%q) = %d, want %d", c.data, got, c.want)
		}
	}
}

func TestPasteName_IsTypeable(t *testing.T) {
	if got, want := PasteName(1), "paste-1.txt"; got != want {
		t.Fatalf("PasteName(1) = %q, want %q", got, want)
	}
	// The name has to sniff as text, because that is what decides how it
	// rides out and what the preview draws it as.
	a, err := FromBytes(PasteName(2), []byte("goroutine 1 [running]:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != provider.AttachmentText || a.MediaType != "text/plain" {
		t.Fatalf("a staged paste is %q/%q, want text/text.plain", a.Kind, a.MediaType)
	}
}

// The order matters: \r\n has to go before a bare \r, or every Windows line
// ending opens two lines instead of one.
func TestNormalizeNewlines_EveryEndingBecomesOne(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a\nb", "a\nb"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\r\n\rb", "a\n\nb"},
		{"a\n\rb\r\n", "a\n\nb\n"},
		{"nothing to do", "nothing to do"},
		{"", ""},
	} {
		if got := NormalizeNewlines(c.in); got != c.want {
			t.Errorf("NormalizeNewlines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// And the counts downstream agree once it has run: eleven CRLF lines are
	// eleven lines, not one.
	crlf := NormalizeNewlines(strings.Repeat("goroutine 1 [running]:\r\n", 11))
	if got := LineCount([]byte(crlf)); got != 11 {
		t.Fatalf("LineCount after normalizing = %d, want 11", got)
	}
	if !PasteOverflows(crlf, DefaultPasteLines, DefaultPasteColumns) {
		t.Fatal("eleven normalized lines should overflow the draft")
	}
}
