package clipboard

// Reading the clipboard. Copy has one job on every platform — push
// text out — but reading it back is where the platforms stop agreeing: a
// screenshot on the pasteboard is not text, and a file copied in Finder or a
// file manager is neither. So Read reports what it found rather than a
// string, and the caller decides whether that is an attachment or something
// to type into the draft.

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// Payload is whatever the clipboard was holding. All three fields can be
// empty (nothing useful on it) and more than one can be set — a copied image
// often carries a text fallback too — so callers read them in preference
// order: image, then files, then text.
type Payload struct {
	// Image is raw image bytes with ImageType naming them.
	Image     []byte
	ImageType string
	// Files are absolute paths to files the clipboard is pointing at.
	Files []string
	// Text is the plain-text flavour.
	Text string
}

// Empty reports whether nothing usable was on the clipboard.
func (p Payload) Empty() bool {
	return len(p.Image) == 0 && len(p.Files) == 0 && strings.TrimSpace(p.Text) == ""
}

// output runs a clipboard tool and returns its stdout. It is a variable so
// tests can drive Read without a real pasteboard.
var output = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

var lookPath = exec.LookPath

// Read reports what the system clipboard is holding.
func Read() (Payload, error) {
	if runtime.GOOS == "darwin" {
		return readDarwin(), nil
	}
	if runtime.GOOS == "windows" {
		return readWindows(), nil
	}
	if _, err := lookPath("wl-paste"); err == nil {
		return readWayland(), nil
	}
	if _, err := lookPath("xclip"); err == nil {
		return readXclip(), nil
	}
	if _, err := lookPath("xsel"); err == nil {
		out, err := output("xsel", "--clipboard", "--output")
		if err != nil {
			return Payload{}, nil
		}
		return Payload{Text: string(out)}, nil
	}
	return Payload{}, fmt.Errorf("no clipboard tool found — install wl-clipboard, xclip, or xsel")
}

// readWindows asks PowerShell, which is the only reader Windows ships — there
// is a clip.exe for writing and nothing for reading.
//
// Text only. An image on the clipboard would have to be round-tripped through
// a temporary file by a script long enough to be its own program, and a paste
// that silently produced a broken image would be worse than one that reports
// nothing. -Raw keeps the newlines a multi-line paste is made of, which the
// default would fold.
func readWindows() Payload {
	out, err := output("powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	if err != nil {
		return Payload{}
	}
	// PowerShell writes CRLF; every reader downstream of this expects the
	// newline it would have got from any other platform's tool.
	return Payload{Text: strings.ReplaceAll(string(out), "\r\n", "\n")}
}

// readDarwin asks the pasteboard through AppleScript, which is always there:
// pngpaste is faster for an image but is not installed by default, so it is
// tried first and osascript is the fallback rather than the requirement.
func readDarwin() Payload {
	var p Payload
	if _, err := lookPath("pngpaste"); err == nil {
		if out, err := output("pngpaste", "-"); err == nil && len(out) > 0 {
			p.Image, p.ImageType = out, "image/png"
		}
	}
	if len(p.Image) == 0 {
		if out, err := output("osascript", "-e", "the clipboard as «class PNGf»"); err == nil {
			if data := parseAppleData(string(out)); len(data) > 0 {
				p.Image, p.ImageType = data, "image/png"
			}
		}
	}
	// A file copied in Finder arrives as a file URL rather than as its
	// contents; `as list` keeps a multi-file copy whole.
	if out, err := output("osascript", "-e",
		"set ps to \"\"\nrepeat with f in (the clipboard as «class furl» as list)\nset ps to ps & POSIX path of f & linefeed\nend repeat\nreturn ps"); err == nil {
		p.Files = splitPaths(string(out))
	}
	if out, err := output("pbpaste"); err == nil {
		p.Text = string(out)
	}
	return p
}

// readWayland reads through wl-paste, which reports the flavours it has.
func readWayland() Payload {
	var p Payload
	types := strings.Fields(runString("wl-paste", "--list-types"))
	if mt := pickImageType(types); mt != "" {
		if out, err := output("wl-paste", "--no-newline", "--type", mt); err == nil {
			p.Image, p.ImageType = out, mt
		}
	}
	if hasType(types, "text/uri-list") {
		p.Files = parseURIList(runString("wl-paste", "--no-newline", "--type", "text/uri-list"))
	}
	p.Text = runString("wl-paste", "--no-newline")
	return p
}

// readXclip reads through xclip, asking TARGETS what the selection offers.
func readXclip() Payload {
	var p Payload
	types := strings.Fields(runString("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"))
	if mt := pickImageType(types); mt != "" {
		if out, err := output("xclip", "-selection", "clipboard", "-t", mt, "-o"); err == nil {
			p.Image, p.ImageType = out, mt
		}
	}
	if hasType(types, "text/uri-list") {
		p.Files = parseURIList(runString("xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o"))
	}
	p.Text = runString("xclip", "-selection", "clipboard", "-o")
	return p
}

func runString(name string, args ...string) string {
	out, err := output(name, args...)
	if err != nil {
		return ""
	}
	return string(out)
}

// pickImageType chooses the best image flavour on offer; PNG first because it
// is lossless and every provider takes it.
func pickImageType(types []string) string {
	for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if hasType(types, want) {
			return want
		}
	}
	return ""
}

func hasType(types []string, want string) bool {
	for _, t := range types {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

// parseAppleData pulls the bytes out of AppleScript's raw-data literal, which
// prints as «data PNGf89504E47…» — a four-character type tag followed by hex.
func parseAppleData(s string) []byte {
	start := strings.Index(s, "«data ")
	if start < 0 {
		return nil
	}
	rest := s[start+len("«data "):]
	end := strings.Index(rest, "»")
	if end < 0 {
		return nil
	}
	body := strings.TrimSpace(rest[:end])
	if len(body) <= 4 {
		return nil
	}
	data, err := hex.DecodeString(body[4:])
	if err != nil {
		return nil
	}
	return data
}

// parseURIList reads the file:// URLs off a text/uri-list flavour, dropping
// the comment lines the format allows and any URL that is not a local file.
func parseURIList(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" {
			continue
		}
		if path, err := url.PathUnescape(u.Path); err == nil && path != "" {
			out = append(out, path)
		}
	}
	return out
}

func splitPaths(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
