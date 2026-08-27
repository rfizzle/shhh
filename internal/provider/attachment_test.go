package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

var testPNG = Attachment{
	Kind:      AttachmentImage,
	Name:      "shot.png",
	MediaType: "image/png",
	Data:      []byte{0x89, 'P', 'N', 'G'},
}

var testNotes = Attachment{
	Kind:      AttachmentText,
	Name:      "notes.md",
	MediaType: "text/markdown",
	Data:      []byte("# heading\n"),
}

func TestAttachment_DataURL(t *testing.T) {
	if got, want := testPNG.DataURL(), "data:image/png;base64,iVBORw=="; got != want {
		t.Fatalf("DataURL = %q, want %q", got, want)
	}
}

func TestAttachment_AsTextWrapsTheContents(t *testing.T) {
	got := testNotes.AsText()
	if !strings.Contains(got, `name="notes.md"`) || !strings.Contains(got, "# heading") {
		t.Fatalf("AsText = %q", got)
	}
	// Bytes no provider can carry say so rather than arriving as mojibake.
	note := testPNG.AsText()
	if !strings.Contains(note, "could not be sent") || !strings.Contains(note, "shot.png") {
		t.Fatalf("fallback note = %q", note)
	}
}

func TestToAnthropicMessages_ImageLeadsTheMessage(t *testing.T) {
	_, out := toAnthropicMessages([]Message{{
		Role:        RoleUser,
		Content:     "what is wrong here?",
		Attachments: []Attachment{testPNG, testNotes},
	}})
	if len(out) != 1 {
		t.Fatalf("got %d messages, want 1", len(out))
	}
	blocks := out[0].Content
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want image + text attachment + sentence", len(blocks))
	}
	if blocks[0].OfImage == nil {
		t.Fatal("the image should lead the message")
	}
	if blocks[0].OfImage.Source.OfBase64.Data != testPNG.Base64() {
		t.Fatal("image block does not carry the bytes")
	}
	if blocks[2].OfText == nil || blocks[2].OfText.Text != "what is wrong here?" {
		t.Fatal("the sentence should come last")
	}
}

func TestToAnthropicMessages_EmptySentenceStillSendsOneBlock(t *testing.T) {
	// Attaching and pressing enter with nothing typed must not produce a
	// message with no content at all, which the API rejects.
	_, out := toAnthropicMessages([]Message{{Role: RoleUser, Attachments: []Attachment{testPNG}}})
	if len(out) != 1 || len(out[0].Content) != 1 || out[0].Content[0].OfImage == nil {
		t.Fatalf("got %#v", out)
	}
	// And a message with no attachments keeps its single text block.
	_, plain := toAnthropicMessages([]Message{{Role: RoleUser, Content: "hi"}})
	if len(plain[0].Content) != 1 || plain[0].Content[0].OfText == nil {
		t.Fatal("a plain message should still be one text block")
	}
}

func TestToOpenAIMessages_MixedContentClearsTheStringField(t *testing.T) {
	out := toOpenAIMessages([]Message{{
		Role:        RoleUser,
		Content:     "look",
		Attachments: []Attachment{testPNG},
	}})
	msg := out[0]
	if msg.Content != "" {
		t.Fatal("Content and MultiContent are mutually exclusive in the SDK")
	}
	if len(msg.MultiContent) != 2 {
		t.Fatalf("got %d parts, want image + sentence", len(msg.MultiContent))
	}
	if msg.MultiContent[0].ImageURL == nil || msg.MultiContent[0].ImageURL.URL != testPNG.DataURL() {
		t.Fatal("the image part does not carry the data URL")
	}
	if msg.MultiContent[1].Text != "look" {
		t.Fatal("the sentence should come last")
	}
	// Messages without attachments stay on the plain string form.
	plain := toOpenAIMessages([]Message{{Role: RoleUser, Content: "hi"}})
	if plain[0].Content != "hi" || plain[0].MultiContent != nil {
		t.Fatalf("plain message = %#v", plain[0])
	}
}

func TestToResponseItems_ImageAndDocumentParts(t *testing.T) {
	pdf := Attachment{Kind: AttachmentDocument, Name: "spec.pdf", MediaType: "application/pdf", Data: []byte("%PDF")}
	items, _ := toResponseItems([]Message{{
		Role:        RoleUser,
		Content:     "read this",
		Attachments: []Attachment{testPNG, pdf},
	}})
	if len(items) != 1 || len(items[0].Content) != 3 {
		t.Fatalf("got %#v", items)
	}
	if items[0].Content[0].Type != "input_image" || items[0].Content[0].ImageURL != testPNG.DataURL() {
		t.Fatalf("image part = %#v", items[0].Content[0])
	}
	if items[0].Content[1].Type != "input_file" || items[0].Content[1].Filename != "spec.pdf" {
		t.Fatalf("file part = %#v", items[0].Content[1])
	}
	if items[0].Content[2].Type != "input_text" || items[0].Content[2].Text != "read this" {
		t.Fatalf("text part = %#v", items[0].Content[2])
	}
	// The added fields must stay out of an ordinary text part's JSON.
	b, err := json.Marshal(items[0].Content[2])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "image_url") || strings.Contains(string(b), "file_data") {
		t.Fatalf("text part carries empty attachment fields: %s", b)
	}
}

func TestToGeminiContents_InlineBlobLeads(t *testing.T) {
	contents, _ := toGeminiContents([]Message{{
		Role:        RoleUser,
		Content:     "look",
		Attachments: []Attachment{testPNG},
	}})
	if len(contents) != 1 || len(contents[0].Parts) != 2 {
		t.Fatalf("got %#v", contents)
	}
	blob := contents[0].Parts[0].InlineData
	if blob == nil || blob.MIMEType != "image/png" || string(blob.Data) != string(testPNG.Data) {
		t.Fatalf("inline blob = %#v", blob)
	}
	if contents[0].Parts[1].Text != "look" {
		t.Fatal("the sentence should come last")
	}
}

func TestAttachmentBytes(t *testing.T) {
	if got := AttachmentBytes([]Attachment{testPNG, testNotes}); got != len(testPNG.Data)+len(testNotes.Data) {
		t.Fatalf("AttachmentBytes = %d", got)
	}
}
