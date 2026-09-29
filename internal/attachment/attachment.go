package attachment

// Acquiring attachments: reading the bytes off the clipboard or off
// disk, deciding what they are, and refusing what shhh cannot carry. The
// wire type lives in internal/provider — this package is only the door the
// bytes come in through, which is why it is named for the noun rather than
// for `attach`, the verb that already means "attach to a sub-agent" here.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/clipboard"
	"github.com/rfizzle/shhh/internal/provider"
)

const (
	// MaxBytes is the ceiling on one attachment. Providers reject inline
	// parts well before their context does, and a rejected request costs the
	// whole turn — so the refusal happens here, where it can name the file.
	MaxBytes = 5 << 20
	// MaxTotalBytes bounds everything staged for one message.
	MaxTotalBytes = 20 << 20
	// MaxTextBytes is the ceiling on a text attachment specifically: its
	// contents go into the prompt verbatim, so the limit that matters is the
	// context window, not the upload size.
	MaxTextBytes = 256 << 10
)

// The shape past which a paste stops being a sentence and becomes a file. Ten
// lines is more than the draft box shows at once, so a paste taller than that
// is already something the reader cannot see the whole of while they finish
// the sentence around it; a thousand columns is wider than any terminal draws,
// so a line that long is one nobody was going to read back either way.
//
// They are the point where inserting the text costs more than staging it —
// not a judgement about size, which is what MaxTextBytes is for.
const (
	DefaultPasteLines   = 10
	DefaultPasteColumns = 1000
)

// Clipboard is one read of the system clipboard, already classified: what
// can be attached, and the plain text that was on it either way. Text alone
// is not an attachment — it belongs in the draft — so the caller decides
// between the two rather than being told there was nothing.
type Clipboard struct {
	Attachments []provider.Attachment
	Text        string
	// Rejected is why something on the clipboard could not be attached —
	// a file too large, bytes of a kind shhh does not carry. Empty when
	// nothing was refused.
	Rejected error
}

// Read reads the clipboard and classifies it: a pasted image first, then the
// files the clipboard points at, and the text flavour alongside both.
func Read() (Clipboard, error) {
	p, err := clipboard.Read()
	if err != nil {
		return Clipboard{}, err
	}
	out := Clipboard{Text: p.Text}
	if len(p.Image) > 0 {
		a, err := FromBytes(defaultImageName(p.ImageType), p.Image)
		if err != nil {
			out.Rejected = err
			return out, nil
		}
		out.Attachments = []provider.Attachment{a}
		return out, nil
	}
	for _, path := range p.Files {
		a, err := FromFile(path)
		if err != nil {
			if out.Rejected == nil {
				out.Rejected = err
			}
			continue
		}
		out.Attachments = append(out.Attachments, a)
	}
	return out, nil
}

// FromFile reads one file off disk as an attachment.
func FromFile(path string) (provider.Attachment, error) {
	path = Expand(path)
	info, err := os.Stat(path)
	if err != nil {
		return provider.Attachment{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return provider.Attachment{}, fmt.Errorf("%s is a directory", filepath.Base(path))
	}
	if info.Size() > MaxBytes {
		return provider.Attachment{}, fmt.Errorf("%s is %s — the limit for one attachment is %s",
			filepath.Base(path), HumanSize(int(info.Size())), HumanSize(MaxBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return provider.Attachment{}, err
	}
	return FromBytes(filepath.Base(path), data)
}

// FromBytes classifies bytes that are already in hand — a pasted screenshot,
// a file already read.
func FromBytes(name string, data []byte) (provider.Attachment, error) {
	if len(data) == 0 {
		return provider.Attachment{}, fmt.Errorf("%s is empty", name)
	}
	if len(data) > MaxBytes {
		return provider.Attachment{}, fmt.Errorf("%s is %s — the limit for one attachment is %s",
			name, HumanSize(len(data)), HumanSize(MaxBytes))
	}
	kind, mediaType, err := Sniff(name, data)
	if err != nil {
		return provider.Attachment{}, err
	}
	if kind == provider.AttachmentText && len(data) > MaxTextBytes {
		return provider.Attachment{}, fmt.Errorf(
			"%s is %s of text — attach files under %s, or let the agent read it with a tool",
			name, HumanSize(len(data)), HumanSize(MaxTextBytes))
	}
	return provider.Attachment{Kind: kind, Name: name, MediaType: mediaType, Data: data}, nil
}

// Sniff decides what a set of bytes is. The extension is consulted only to
// sharpen a media type the content sniffer already agrees with — bytes win,
// so a .png that is really a JPEG is sent as one, and a recording renamed to
// .wav is refused rather than sent under a format it is not in.
func Sniff(name string, data []byte) (provider.AttachmentKind, string, error) {
	detected := http.DetectContentType(data)
	if i := strings.IndexByte(detected, ';'); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return provider.AttachmentImage, detected, nil
	case "application/pdf":
		return provider.AttachmentDocument, detected, nil
	}
	// A recording keeps the name the vendors' format lists give it, which is
	// not always the one the sniffing rules answer with.
	if mediaType, ok := provider.AudioMediaType(detected); ok {
		return provider.AttachmentAudio, mediaType, nil
	}
	// DetectContentType calls anything textish text/plain; source files are
	// the common case here, so the extension names them more usefully.
	if utf8.Valid(data) && !hasNUL(data) {
		return provider.AttachmentText, textMediaType(name), nil
	}
	return "", "", fmt.Errorf("%s is %s — shhh attaches images, PDFs, audio, and text files",
		name, detected)
}

func hasNUL(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func textMediaType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "text/xml"
	case ".yaml", ".yml":
		return "text/yaml"
	}
	return "text/plain"
}

func defaultImageName(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "clipboard.jpg"
	case "image/gif":
		return "clipboard.gif"
	case "image/webp":
		return "clipboard.webp"
	}
	return "clipboard.png"
}

// Expand resolves the forms a path arrives in when it was dragged into a
// terminal or copied from a file manager: surrounding quotes, backslash
// escapes, and a leading ~.
func Expand(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 2 {
		if (path[0] == '\'' && path[len(path)-1] == '\'') || (path[0] == '"' && path[len(path)-1] == '"') {
			path = path[1 : len(path)-1]
		}
	}
	path = strings.ReplaceAll(path, `\ `, " ")
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// LooksLikeFile reports whether a pasted string is a single path to a file
// that exists. A paste that is a path to a screenshot is almost always a
// drag-and-drop; a paste of several lines is prose that happens to start with
// a slash, so only one line qualifies.
func LooksLikeFile(s string) (string, bool) {
	if strings.ContainsAny(s, "\n\r") {
		return "", false
	}
	path := Expand(s)
	if path == "" {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// PeekKind classifies a file from its first bytes without reading it whole.
// It exists for the one caller that has to decide on the event loop — a
// bracketed paste, which is a keystroke — where reading a five-megabyte file
// to find out it is a PNG would stall the frame. The read that follows
// happens in a command.
func PeekKind(path string) (provider.AttachmentKind, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 512 is what http.DetectContentType reads; more would be discarded.
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return "", err
	}
	kind, _, err := Sniff(filepath.Base(path), head[:n])
	return kind, err
}

// PasteOverflows reports whether pasted text is too big to leave in the
// draft: taller than maxLines, or holding a line wider than maxColumns.
//
// This is the one place the thresholds are read, and so the one place the
// rule about them lives: a non-positive threshold turns its own half of the
// test off, which is how a person who wants every paste typed says so.
//
// A line is measured only once its bytes say it could be wide enough. A
// column costs at least a byte, so a line shorter than the threshold in bytes
// cannot reach it in columns, and the common paste — which arrives on the
// event loop, because a paste is a keystroke — is never segmented at all.
//
// It counts \n and nothing else, so give it NormalizeNewlines' output: a
// terminal that ends a pasted line with a bare \r would otherwise hand a
// fifty-line stack trace over as one line.
func PasteOverflows(text string, maxLines, maxColumns int) bool {
	if text == "" {
		return false
	}
	if maxLines > 0 && strings.Count(strings.TrimSuffix(text, "\n"), "\n")+1 > maxLines {
		return true
	}
	if maxColumns <= 0 {
		return false
	}
	for line := range strings.SplitSeq(text, "\n") {
		if len(line) > maxColumns && ansi.StringWidth(line) > maxColumns {
			return true
		}
	}
	return false
}

// NormalizeNewlines puts a paste's line endings in the one form everything
// downstream counts: \r\n and a bare \r both become \n.
//
// The draft never had to care, because the textarea rewrites both on the way
// in (charm.land/bubbles' runeutil). Staging is a second door onto the same
// bytes and there is nothing between it and the terminal, so it does the same
// thing here — otherwise a CR-delimited paste is one line to every count that
// follows: the threshold that decides whether to stage it, the chip that says
// how tall it is, and the preview that draws it.
func NormalizeNewlines(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// LineCount is how many lines of text an attachment carries, as the chip and
// the preview report it. A trailing newline opens no line: a file that ends
// the way files end is not one line longer than what is in it. It is the
// provider's count, because the line leading a text attachment in a request
// states the same figure (provider.Attachment.Label).
func LineCount(data []byte) int {
	return provider.LineCount(data)
}

// PasteName is what the paste handed `Paste#n` is called. A paste has no
// name of its own — nobody chose it and there is no file behind it — so it
// is given one that follows its number, and the name and the handle can
// never tell a reader two different things about which paste it is.
func PasteName(n int) string {
	return fmt.Sprintf("paste-%d.txt", n)
}

// HumanSize renders a byte count the way the rails do — two significant
// figures at most, so it never widens a row unpredictably. It is the
// provider's spelling, for LineCount's reason.
func HumanSize(n int) string {
	return provider.HumanSize(n)
}

// Summarize is the one-line description of a staged set, for the notice rail
// and the transcript row: how many, and how much.
func Summarize(atts []provider.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	noun := "attachments"
	if len(atts) == 1 {
		noun = "attachment"
	}
	return fmt.Sprintf("%d %s · %s", len(atts), noun, HumanSize(provider.AttachmentBytes(atts)))
}

// Names lists the staged attachments the way the transcript shows them: the
// name and its size, never the bytes.
func Names(atts []provider.Attachment) []string {
	out := make([]string, 0, len(atts))
	for _, a := range atts {
		out = append(out, fmt.Sprintf("%s (%s)", a.Name, HumanSize(len(a.Data))))
	}
	return out
}

// The three words a handle is spelled with
// (docs/capabilities/chat.md#what-can-ride-with-a-message). A picture has a
// word of its own because it is the one kind a reader refers to by what is
// in it — "the second screenshot" — and a paste has one because it is the
// one attachment with no file behind it, so its name is only ever the
// number. Everything else — a PDF, a text file, a recording — is a file.
const (
	HandleImage = "Image"
	HandlePaste = "Paste"
	HandleFile  = "File"
)

// HandleWord is the word an attachment of this kind is numbered under when
// nothing asked for another. A paste is never decided here: it is text like
// a text file, and only the door that staged it knows it had no file.
func HandleWord(kind provider.AttachmentKind) string {
	if kind == provider.AttachmentImage {
		return HandleImage
	}
	return HandleFile
}

// SplitHandle reads a handle back into its word and number. It answers false
// for anything that is not one of the three words, a `#` and a positive
// number, spelled exactly — the handles this package writes, and nothing a
// reader typed that merely resembles one.
func SplitHandle(h string) (word string, n int, ok bool) {
	word, digits, found := strings.Cut(h, "#")
	if !found || !knownHandleWord(word) {
		return "", 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || strconv.Itoa(n) != digits {
		return "", 0, false
	}
	return word, n, true
}

func knownHandleWord(word string) bool {
	return word == HandleImage || word == HandlePaste || word == HandleFile
}

// Handles is the conversation's count of each word: the highest number
// handed out so far. It is a value with no map in it on purpose — a session
// model is copied on every update, and a count shared between the copies
// would be advanced by a staging the caller then threw away.
type Handles struct {
	image, paste, file int
}

// Next hands out the next handle of a word and counts it. A word that is not
// one of the three is numbered as a file, which is what anything unnamed is.
func (h *Handles) Next(word string) string {
	if !knownHandleWord(word) {
		word = HandleFile
	}
	c := h.count(word)
	*c++
	return word + "#" + strconv.Itoa(*c)
}

// Saw counts a handle that was handed out somewhere else — on a saved
// message being read back, or on a chip still staged across a boundary — so
// the next one of its word is past it. Anything that is not a handle is
// ignored: a message saved before handles existed carries none.
func (h *Handles) Saw(handle string) {
	word, n, ok := SplitHandle(handle)
	if !ok {
		return
	}
	if c := h.count(word); n > *c {
		*c = n
	}
}

func (h *Handles) count(word string) *int {
	switch word {
	case HandleImage:
		return &h.image
	case HandlePaste:
		return &h.paste
	}
	return &h.file
}
