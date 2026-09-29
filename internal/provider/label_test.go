package provider

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

// pictureOf is a real PNG of the given size, so Figure has a header to read.
func pictureOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// labelled is one attachment a sentence can point at, and the line the
// request must lead its part with.
type labelled struct {
	name  string
	att   Attachment
	label string
}

func labelledCases(t *testing.T) []labelled {
	return []labelled{
		{
			name: "a picture is counted in pixels",
			att: Attachment{Kind: AttachmentImage, Name: "clipboard.png", Handle: "Image#1",
				MediaType: "image/png", Data: pictureOf(t, 14, 9)},
			label: "Image#1 (clipboard.png, 14×9):",
		},
		{
			name: "a paste is counted in lines",
			att: Attachment{Kind: AttachmentText, Name: "paste-1.txt", Handle: "Paste#1",
				MediaType: "text/plain", Data: []byte(strings.Repeat("round 26 reached\n", 214))},
			label: "Paste#1 (paste-1.txt, 214 lines):",
		},
		{
			name: "a document is counted in bytes",
			att: Attachment{Kind: AttachmentDocument, Name: "spec.pdf", Handle: "File#1",
				MediaType: "application/pdf", Data: bytes.Repeat([]byte("%PDF"), 1024)},
			label: "File#1 (spec.pdf, 4 KB):",
		},
	}
}

func TestAttachment_LabelNamesTheHandleTheNameAndTheFigure(t *testing.T) {
	for _, tc := range labelledCases(t) {
		if got := tc.att.Label(); got != tc.label {
			t.Errorf("%s: Label = %q, want %q", tc.name, got, tc.label)
		}
	}
	// A picture whose header cannot be read is counted in bytes, and a row
	// saved before handles existed is named by its name alone.
	old := Attachment{Kind: AttachmentImage, Name: "shot.webp", MediaType: "image/webp", Data: []byte("RIFF")}
	if got, want := old.Label(), "shot.webp (4 B):"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
	one := Attachment{Kind: AttachmentText, Name: "paste-2.txt", Handle: "Paste#2", Data: []byte("x\n")}
	if got, want := one.Figure(), "1 line"; got != want {
		t.Errorf("Figure = %q, want %q", got, want)
	}
}

// Each dialect leads an attachment's part with its label, then the bytes,
// then the sentence last: the label points, it moves nothing.

func TestToAnthropicMessages_LabelLeadsEachAttachment(t *testing.T) {
	for _, tc := range labelledCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			_, out := toAnthropicMessages([]Message{{
				Role: RoleUser, Content: tc.att.Handle + " shows it", Attachments: []Attachment{tc.att},
			}})
			blocks := out[0].Content
			if len(blocks) != 3 {
				t.Fatalf("got %d blocks, want label, bytes, sentence", len(blocks))
			}
			if blocks[0].OfText == nil || blocks[0].OfText.Text != tc.label {
				t.Fatalf("first block = %#v, want the label %q", blocks[0].OfText, tc.label)
			}
			switch tc.att.Kind {
			case AttachmentImage:
				if blocks[1].OfImage == nil {
					t.Fatal("the picture should follow its label")
				}
			case AttachmentDocument:
				if blocks[1].OfDocument == nil {
					t.Fatal("the document should follow its label")
				}
			default:
				if blocks[1].OfText == nil || !strings.Contains(blocks[1].OfText.Text, `<attachment name="`+tc.att.Name+`"`) {
					t.Fatalf("the text attachment should keep its wrapper: %#v", blocks[1].OfText)
				}
			}
			if blocks[2].OfText == nil || blocks[2].OfText.Text != tc.att.Handle+" shows it" {
				t.Fatal("the sentence should come last")
			}
		})
	}
}

func TestToGeminiContents_LabelLeadsEachAttachment(t *testing.T) {
	for _, tc := range labelledCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			contents, _ := toGeminiContents([]Message{{
				Role: RoleUser, Content: tc.att.Handle + " shows it", Attachments: []Attachment{tc.att},
			}})
			parts := contents[0].Parts
			if len(parts) != 3 {
				t.Fatalf("got %d parts, want label, bytes, sentence", len(parts))
			}
			if parts[0].Text != tc.label || parts[0].InlineData != nil {
				t.Fatalf("first part = %#v, want the label %q", parts[0], tc.label)
			}
			if tc.att.Kind == AttachmentText {
				if !strings.Contains(parts[1].Text, `<attachment name="`+tc.att.Name+`"`) {
					t.Fatalf("the text attachment should keep its wrapper: %q", parts[1].Text)
				}
			} else if parts[1].InlineData == nil {
				t.Fatal("the bytes should follow their label")
			}
			if parts[2].Text != tc.att.Handle+" shows it" {
				t.Fatal("the sentence should come last")
			}
		})
	}
}

func TestToResponseItems_LabelLeadsEachAttachment(t *testing.T) {
	bytesType := map[AttachmentKind]string{
		AttachmentImage: "input_image", AttachmentDocument: "input_file", AttachmentText: "input_text",
	}
	for _, tc := range labelledCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			items, _ := toResponseItems([]Message{{
				Role: RoleUser, Content: tc.att.Handle + " shows it", Attachments: []Attachment{tc.att},
			}}, false)
			content := items[0].Content
			if len(content) != 3 {
				t.Fatalf("got %d parts, want label, bytes, sentence", len(content))
			}
			if content[0].Type != "input_text" || content[0].Text != tc.label {
				t.Fatalf("first part = %#v, want the label %q", content[0], tc.label)
			}
			if content[1].Type != bytesType[tc.att.Kind] {
				t.Fatalf("second part = %q, want %q", content[1].Type, bytesType[tc.att.Kind])
			}
			if content[2].Text != tc.att.Handle+" shows it" {
				t.Fatal("the sentence should come last")
			}
		})
	}
}

func TestToOpenAIMessages_LabelLeadsEachAttachment(t *testing.T) {
	for _, tc := range labelledCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			parts := toOpenAIMessages([]Message{{
				Role: RoleUser, Content: tc.att.Handle + " shows it", Attachments: []Attachment{tc.att},
			}})[0].MultiContent
			if len(parts) != 3 {
				t.Fatalf("got %d parts, want label, bytes, sentence", len(parts))
			}
			if parts[0].Text != tc.label || parts[0].ImageURL != nil {
				t.Fatalf("first part = %#v, want the label %q", parts[0], tc.label)
			}
			if tc.att.Kind == AttachmentImage {
				if parts[1].ImageURL == nil {
					t.Fatal("the picture should follow its label")
				}
			} else if parts[1].Text != tc.att.AsText() {
				t.Fatalf("second part = %q, want the attachment's text form", parts[1].Text)
			}
			if parts[2].Text != tc.att.Handle+" shows it" {
				t.Fatal("the sentence should come last")
			}
		})
	}
}
