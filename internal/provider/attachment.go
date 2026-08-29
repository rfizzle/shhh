package provider

// Attachments: a user message can carry parts that are not
// conversation — a pasted screenshot, a file dragged in from the desktop, a
// PDF. They are held here as raw bytes with a sniffed media type rather than
// as provider-shaped blocks, because the same attachment has to be sendable
// to whichever provider the session is pointed at, and the session can switch
// providers mid-conversation (/model, /provider). Each provider's converter
// decides how to carry one; only the fallback text form is shared.

import (
	"encoding/base64"
	"fmt"
	"strings"
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
	Name      string
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

// AttachmentBytes is the total size of a set, for the size ceilings the
// front-end enforces and the count it shows.
func AttachmentBytes(atts []Attachment) int {
	var n int
	for _, a := range atts {
		n += len(a.Data)
	}
	return n
}
