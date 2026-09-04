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

// The recordings the sniffer can produce, one per format the dialects
// disagree about: an MP3 both OpenAI dialects name, a WAV they name under a
// media type the sniffing rules do not use, and an Ogg only Gemini takes.
var (
	testMP3 = Attachment{
		Kind:      AttachmentAudio,
		Name:      "memo.mp3",
		MediaType: "audio/mpeg",
		Data:      []byte("ID3\x04"),
	}
	testWAV = Attachment{
		Kind:      AttachmentAudio,
		Name:      "clip.wav",
		MediaType: "audio/wav",
		Data:      []byte("RIFF\x00\x00\x00\x00WAVE"),
	}
	testOGG = Attachment{
		Kind:      AttachmentAudio,
		Name:      "call.ogg",
		MediaType: "audio/ogg",
		Data:      []byte("OggS\x00"),
	}
)

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
	}}, false)
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

func TestAudioMediaType_RenamesWhatTheSnifferAnswers(t *testing.T) {
	// The two the sniffing rules and the vendors' lists disagree about.
	if got, ok := AudioMediaType("audio/wave"); !ok || got != "audio/wav" {
		t.Fatalf("audio/wave = %q, %v", got, ok)
	}
	if got, ok := AudioMediaType("application/ogg"); !ok || got != "audio/ogg" {
		t.Fatalf("application/ogg = %q, %v", got, ok)
	}
	// And the two they agree about.
	for _, mt := range []string{"audio/mpeg", "audio/aiff"} {
		if got, ok := AudioMediaType(mt); !ok || got != mt {
			t.Fatalf("%s = %q, %v", mt, got, ok)
		}
	}
	// A score is not a recording, and neither is a picture.
	for _, mt := range []string{"audio/midi", "image/png", "application/octet-stream"} {
		if got, ok := AudioMediaType(mt); ok {
			t.Fatalf("%s carried as %q", mt, got)
		}
	}
}

func TestToGeminiContents_AudioRidesAsAnInlineBlob(t *testing.T) {
	contents, _ := toGeminiContents([]Message{{
		Role:        RoleUser,
		Content:     "what is said here?",
		Attachments: []Attachment{testMP3, testOGG},
	}})
	if len(contents) != 1 || len(contents[0].Parts) != 3 {
		t.Fatalf("got %#v", contents)
	}
	// Gemini's own list spells MP3 differently from every other surface.
	mp3 := contents[0].Parts[0].InlineData
	if mp3 == nil || mp3.MIMEType != "audio/mp3" || string(mp3.Data) != string(testMP3.Data) {
		t.Fatalf("mp3 blob = %#v", mp3)
	}
	ogg := contents[0].Parts[1].InlineData
	if ogg == nil || ogg.MIMEType != "audio/ogg" {
		t.Fatalf("ogg blob = %#v", ogg)
	}
	if contents[0].Parts[2].Text != "what is said here?" {
		t.Fatal("the sentence should come last")
	}
}

func TestToGeminiContents_AFormatItsListOmitsGoesAsText(t *testing.T) {
	flac := Attachment{Kind: AttachmentAudio, Name: "take.flac", MediaType: "audio/flac", Data: []byte("fLaC")}
	contents, _ := toGeminiContents([]Message{{Role: RoleUser, Attachments: []Attachment{flac}}})
	part := contents[0].Parts[0]
	if part.InlineData != nil {
		t.Fatalf("a format the list omits should not go inline: %#v", part.InlineData)
	}
	if !strings.Contains(part.Text, "could not be sent") || !strings.Contains(part.Text, "take.flac") {
		t.Fatalf("fallback part = %q", part.Text)
	}
}

func TestToResponseItems_AudioIsBareBase64AndAFormatToken(t *testing.T) {
	items, _ := toResponseItems([]Message{{
		Role:        RoleUser,
		Content:     "transcribe this",
		Attachments: []Attachment{testMP3, testWAV},
	}}, false)
	if len(items) != 1 || len(items[0].Content) != 3 {
		t.Fatalf("got %#v", items)
	}
	mp3 := items[0].Content[0]
	if mp3.Type != "input_audio" || mp3.InputAudio == nil {
		t.Fatalf("mp3 part = %#v", mp3)
	}
	// Bare base64, not the data URL an image rides on.
	if mp3.InputAudio.Data != testMP3.Base64() || mp3.InputAudio.Format != "mp3" {
		t.Fatalf("mp3 audio = %#v", mp3.InputAudio)
	}
	if strings.HasPrefix(mp3.InputAudio.Data, "data:") {
		t.Fatal("the audio part takes base64 without a data URL prefix")
	}
	if wav := items[0].Content[1]; wav.InputAudio == nil || wav.InputAudio.Format != "wav" {
		t.Fatalf("wav part = %#v", wav)
	}
	if items[0].Content[2].Type != "input_text" {
		t.Fatalf("text part = %#v", items[0].Content[2])
	}
	// The nested object must stay out of every other part's JSON.
	b, err := json.Marshal(items[0].Content[2])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "input_audio") {
		t.Fatalf("text part carries an empty audio object: %s", b)
	}
}

func TestToResponseItems_AFormatTheAudioPartOmitsGoesAsText(t *testing.T) {
	items, _ := toResponseItems([]Message{{Role: RoleUser, Attachments: []Attachment{testOGG}}}, false)
	part := items[0].Content[0]
	if part.Type != "input_text" || part.InputAudio != nil {
		t.Fatalf("ogg part = %#v", part)
	}
	if !strings.Contains(part.Text, "could not be sent") || !strings.Contains(part.Text, "call.ogg") {
		t.Fatalf("fallback part = %q", part.Text)
	}
}

func TestAudioDegradesOnTheDialectsWithNoPartForIt(t *testing.T) {
	// Chat completions: the endpoint takes a recording and the client this
	// dialect goes through has no field for one, so the note is what rides.
	parts := toOpenAIMessages([]Message{{
		Role:        RoleUser,
		Content:     "listen",
		Attachments: []Attachment{testMP3},
	}})[0].MultiContent
	if len(parts) != 2 || parts[0].ImageURL != nil {
		t.Fatalf("got %#v", parts)
	}
	if !strings.Contains(parts[0].Text, "could not be sent") || !strings.Contains(parts[0].Text, "memo.mp3") {
		t.Fatalf("fallback part = %q", parts[0].Text)
	}
	// The Messages API takes no recording at all.
	_, out := toAnthropicMessages([]Message{{Role: RoleUser, Attachments: []Attachment{testMP3}}})
	block := out[0].Content[0]
	if block.OfText == nil || !strings.Contains(block.OfText.Text, "could not be sent") {
		t.Fatalf("anthropic block = %#v", block)
	}
}
