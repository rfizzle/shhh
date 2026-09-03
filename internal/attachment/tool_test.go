package attachment

import (
	"fmt"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func image(name string) provider.Attachment {
	return provider.Attachment{Kind: provider.AttachmentImage, Name: name, MediaType: "image/png", Data: []byte{1}}
}

func TestNoteAndTakeResult(t *testing.T) {
	NoteResult("a.png is an image", image("a.png"))
	got := TakeResult("a.png is an image")
	if len(got) != 1 || got[0].Name != "a.png" {
		t.Fatalf("expected the noted image back, got %+v", got)
	}
	if TakeResult("a.png is an image") != nil {
		t.Error("a collected result should not be collectable twice")
	}
}

func TestTakeResult_UnknownResultIsNothing(t *testing.T) {
	if got := TakeResult("some ordinary tool output"); got != nil {
		t.Errorf("expected nothing for a result nothing was noted for, got %+v", got)
	}
}

func TestNoteResult_OldestGoesWhenNobodyCollects(t *testing.T) {
	first := "0.png is an image"
	for i := range maxPendingResults + 1 {
		NoteResult(fmt.Sprintf("%d.png is an image", i), image("x.png"))
	}
	if TakeResult(first) != nil {
		t.Error("the oldest uncollected entry should have been evicted")
	}
	last := fmt.Sprintf("%d.png is an image", maxPendingResults)
	if TakeResult(last) == nil {
		t.Error("the newest entry should still be there")
	}
	// Leave nothing behind for the tests that follow.
	for i := range maxPendingResults + 1 {
		TakeResult(fmt.Sprintf("%d.png is an image", i))
	}
}

func TestNoteResult_IgnoresEmptyInput(t *testing.T) {
	NoteResult("", image("a.png"))
	NoteResult("a result with nothing on it")
	if TakeResult("") != nil || TakeResult("a result with nothing on it") != nil {
		t.Error("nothing should have been recorded")
	}
}

func TestTakeResult_SurvivesAWrapperAddingToTheText(t *testing.T) {
	notice := "logo.png is an image"
	NoteResult(notice, image("logo.png"))
	got := TakeResult("[repeat: you have run this before]\n" + notice)
	if len(got) != 1 {
		t.Fatalf("a wrapper's notice above the result must not lose the image, got %+v", got)
	}
}

func TestTakeResult_UnrelatedResultLeavesThePartsWhereTheyAre(t *testing.T) {
	notice := "diagram.png is an image"
	NoteResult(notice, image("diagram.png"))
	if got := TakeResult("some other tool's output"); got != nil {
		t.Fatalf("expected nothing, got %+v", got)
	}
	if got := TakeResult(notice); len(got) != 1 {
		t.Fatalf("the parts should still be collectable, got %+v", got)
	}
}
