package attachment

// Acquiring attachments (S-134): reading the bytes off the clipboard or off
// disk, deciding what they are, and refusing what shhh cannot carry. The
// wire type lives in internal/provider — this package is only the door the
// bytes come in through, which is why it is named for the noun rather than
// for `attach`, the verb that already means "attach to a sub-agent" here.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

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
// so a .png that is really a JPEG is sent as one.
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
	// DetectContentType calls anything textish text/plain; source files are
	// the common case here, so the extension names them more usefully.
	if utf8.Valid(data) && !hasNUL(data) {
		return provider.AttachmentText, textMediaType(name), nil
	}
	return "", "", fmt.Errorf("%s is %s — shhh attaches images, PDFs, and text files",
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

// HumanSize renders a byte count the way the rails do — two significant
// figures at most, so it never widens a row unpredictably.
func HumanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
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
