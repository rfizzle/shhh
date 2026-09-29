package provider

// Attachments: a user message can carry parts that are not
// conversation — a pasted screenshot, a file dragged in from the desktop, a
// PDF. They are held here as raw bytes with a sniffed media type rather than
// as provider-shaped blocks, because the same attachment has to be sendable
// to whichever provider the session is pointed at, and the session can switch
// providers mid-conversation (/model, /provider). Each provider's converter
// decides how to carry one; only the fallback text form is shared.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"strings"

	// The picture formats whose size Figure can read from a header. They are
	// registered for their side effect, which is what image.DecodeConfig
	// reads its sniffer from; WebP is not among them and is counted in bytes.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// AttachmentKind is how a provider should carry one part. Sniffing decides it
// once, where the bytes are read, so every converter agrees about what a
// given attachment is.
type AttachmentKind string

const (
	// AttachmentImage is a raster image the model can see.
	AttachmentImage AttachmentKind = "image"
	// AttachmentDocument is a document the model reads natively — a PDF.
	// Providers that cannot take one inline degrade to a visible note
	// rather than dropping it silently.
	AttachmentDocument AttachmentKind = "document"
	// AttachmentAudio is a recording the model listens to — a voice memo, a
	// clip off a call. It is the narrowest of the kinds: two of the four
	// dialects take one at all, and each of those takes a shorter list of
	// formats than it takes of pictures, so the converters decide format by
	// format and anything a dialect has no part for degrades to the note.
	// See docs/capabilities/chat.md#what-can-ride-with-a-message.
	AttachmentAudio AttachmentKind = "audio"
	// AttachmentText is text that belongs in the prompt rather than in the
	// sentence: a file's contents, wrapped so the model can tell the two
	// apart. Every provider carries it the same way.
	AttachmentText AttachmentKind = "text"
)

// Attachment is one non-conversational part riding along with a message.
type Attachment struct {
	Kind AttachmentKind
	// Name is what to call it in the prompt and on screen — a base name, not
	// a path, so the transcript does not leak the sender's directory layout.
	Name string
	// Handle is what the attachment is called when somebody points at it —
	// `Image#2`, `Paste#1`, `File#3` — numbered by kind across the
	// conversation. It sits beside the name rather than replacing it,
	// because the name is often nobody's choice (every pasted screenshot is
	// clipboard.png) and three chips with one name cannot be told apart by
	// the two verbs that take one. Empty on a message saved before handles
	// existed. Label is where a converter reads it: every part a user
	// message carries is led by a line naming it by its handle.
	// See docs/capabilities/chat.md#what-can-ride-with-a-message.
	Handle    string
	MediaType string
	Data      []byte
}

// Base64 is the attachment's bytes in the encoding every provider's inline
// form asks for.
func (a Attachment) Base64() string {
	return base64.StdEncoding.EncodeToString(a.Data)
}

// DataURL is the `data:` form the OpenAI-shaped APIs take an inline image in.
func (a Attachment) DataURL() string {
	return "data:" + a.MediaType + ";base64," + a.Base64()
}

// AsText is the attachment as prompt text: the wrapped contents for a text
// attachment, and a visible note for bytes this provider could not carry.
// The tag form is deliberate — it tells the model where the file starts and
// stops without pretending the bytes are part of the sentence.
func (a Attachment) AsText() string {
	if a.Kind == AttachmentText {
		body := strings.TrimRight(string(a.Data), "\n")
		return fmt.Sprintf("<attachment name=%q type=%q>\n%s\n</attachment>", a.Name, a.MediaType, body)
	}
	return fmt.Sprintf("[attachment %q (%s, %d bytes) could not be sent to this provider inline]",
		a.Name, a.MediaType, len(a.Data))
}

// Label is the one line that leads an attachment's part in a request:
// `Image#1 (clipboard.png, 1440×900):`. It is what makes a handle written in
// the sentence resolve — "Image#1 shows the error" names the part the line
// stands over — and it carries the name as well, so a file named in the
// sentence by hand resolves too. The bytes still lead the message and the
// sentence still follows them: the label points, it moves nothing
// (docs/capabilities/chat.md#what-can-ride-with-a-message).
//
// An attachment saved before handles existed is labelled by its name alone.
func (a Attachment) Label() string {
	if a.Handle == "" {
		return fmt.Sprintf("%s (%s):", a.Name, a.Figure())
	}
	return fmt.Sprintf("%s (%s, %s):", a.Handle, a.Name, a.Figure())
}

// Figure is the one fact an attachment is counted by where it is pointed at —
// the fold in the draft, the fold row under a sent message, and Label: a
// picture's size in pixels, text's height in lines, anything else its size in
// bytes. One function, so the word in the sentence and the line over the
// bytes cannot describe the same attachment two ways.
//
// A picture whose header this package cannot read — WebP, or bytes that are
// not the picture they claim to be — is counted in bytes rather than not at
// all: the fold still needs a figure, and a guessed one would be wrong.
func (a Attachment) Figure() string {
	switch a.Kind {
	case AttachmentImage:
		cfg, _, err := image.DecodeConfig(bytes.NewReader(a.Data))
		if err == nil && cfg.Width > 0 && cfg.Height > 0 {
			return fmt.Sprintf("%d×%d", cfg.Width, cfg.Height)
		}
	case AttachmentText:
		if n := LineCount(a.Data); n != 1 {
			return fmt.Sprintf("%d lines", n)
		}
		return "1 line"
	}
	return HumanSize(len(a.Data))
}

// LineCount is how many lines text runs to, the way the chip, the fold and
// Label count them: a trailing newline does not open another line.
func LineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	body := bytes.TrimSuffix(data, []byte("\n"))
	return bytes.Count(body, []byte("\n")) + 1
}

// HumanSize renders a byte count the way the rails do — two significant
// figures at most, so it never widens a row unpredictably. It is here rather
// than beside the staging area because Label writes one into the request,
// and the chip and the line over the bytes must spell a size alike.
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

// audioMediaTypes maps what a byte sniffer calls a recording onto the media
// type a vendor's list of accepted formats knows it by. The two disagree, and
// silently: every content detector follows the same sniffing rules, and those
// rules answer `audio/wave` for a RIFF/WAVE file and `application/ogg` for an
// Ogg stream — neither of which appears on any of the lists, so a request
// carrying one is refused over the name rather than over the bytes.
//
// A format no detector recognises from its first bytes is left out rather
// than guessed at from the extension: FLAC, raw AAC and an MP3 saved without
// its ID3 tag have no signature in the rules, and a kind that came from a
// file's name would put bytes inline on the strength of a rename. Those are
// refused rather than degraded, which is the one place a reader will notice
// the line: the refusal names the file and what it sniffed as. MIDI has one and is still left out — it is a
// score rather than a recording, and no vendor lists it.
//
// An Ogg container can hold video, and this calls every Ogg audio. The rules
// cannot tell the two apart from a header, and the failure that costs is one
// refused request for a video nobody drags into a chat; refusing every voice
// recording to avoid it is the worse half of the trade.
var audioMediaTypes = map[string]string{
	"audio/aiff":      "audio/aiff",
	"audio/mpeg":      "audio/mpeg",
	"audio/wave":      "audio/wav",
	"application/ogg": "audio/ogg",
}

// AudioMediaType reports whether sniffed bytes are a recording shhh carries,
// and the media type to carry it under. It takes the detector's answer with
// any parameters already stripped, which is the form the sniffer holds.
func AudioMediaType(detected string) (string, bool) {
	mediaType, ok := audioMediaTypes[detected]
	return mediaType, ok
}

// AttachmentBytes is the total size of a set, for the size ceilings the
// front-end enforces and the count it shows.
func AttachmentBytes(atts []Attachment) int {
	var n int
	for _, a := range atts {
		n += len(a.Data)
	}
	return n
}
