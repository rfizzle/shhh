package components

// The backlog screen against its own rules: what each filter narrows to,
// what a key does to the row under the pointer, and which keys are not live
// while a turn is working.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func pressAll(b *BacklogScreen, text string) (done bool, result BacklogResult) {
	for _, r := range text {
		done, result = b.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return done, result
}

// slugsShowing is the list as it stands, which is what every filter case
// below is asserted against.
func slugsShowing(b *BacklogScreen) string {
	b.sync()
	var out []string
	for _, i := range b.shown {
		out = append(out, b.rows()[i].Slug)
	}
	return strings.Join(out, " ")
}

// Each filter narrows the fixture to exactly what it says it does, and the
// header says which filter did it — a list shorter than the backlog with
// nothing on screen explaining why is the failure this states against.
func TestBacklogScreen_FiltersNarrowAsStated(t *testing.T) {
	for _, tc := range []struct {
		name  string
		press string
		want  string
		says  string
	}{
		{"status", "s", "screen-over-items prose-renderer drop-loses-the-file half-written", "open"},
		{"priority", "p", "rail-todo-block screen-over-items half-written", "high priority"},
		{"kind", "k", "rail-todo-block screen-over-items sprint-file half-written", "kind story"},
		{"ready", "r", "prose-renderer drop-loses-the-file half-written", "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := goldenBacklogScreen()
			pressAll(b, tc.press)
			if got := slugsShowing(b); got != tc.want {
				t.Errorf("[%s] left %q, want %q", tc.press, got, tc.want)
			}
			if got := b.filterWords(); got != tc.says {
				t.Errorf("the header says %q, want %q", got, tc.says)
			}
		})
	}
}

// The text filter matches the slug and the title, and the header states both
// the words and what is left of the list.
func TestBacklogScreen_TextFilterStatesItsCount(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("/"))
	pressAll(b, "renderer")
	if got := slugsShowing(b); got != "prose-renderer" {
		t.Fatalf("the filter left %q", got)
	}
	view := ansi.Strip(b.View(110))
	for _, want := range []string{"matching renderer", "1 of 6 items", "5 hidden"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen never says %q:\n%s", want, view)
		}
	}
}

// The selectors' rule: while the query row is open every letter is a letter,
// and clearing an already empty query closes the row and hands them back.
func TestBacklogScreen_QueryRowKeepsTheLettersUntilItCloses(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("/"))
	pressAll(b, "sr")
	if b.query != "sr" || b.status != 0 || b.ready {
		t.Fatalf("letters typed into the query cycled a filter: query=%q status=%d ready=%v", b.query, b.status, b.ready)
	}
	b.Update(key("ctrl+u"))
	if b.query != "" || !b.filtering {
		t.Fatalf("the first clear should empty the query and leave the row open, got %q filtering=%v", b.query, b.filtering)
	}
	b.Update(key("ctrl+u"))
	if b.filtering {
		t.Fatal("clearing an empty query should close the row")
	}
	pressAll(b, "s")
	if b.status == 0 {
		t.Fatal("the letters should be live again once the row has closed")
	}
}

// A file that cannot be read is a row rather than a gap, and it survives the
// field filters because it has no fields to answer them with: the row is the
// only thing on screen saying the file is there.
func TestBacklogScreen_UnreadableRowSurvivesAndCarriesTheReason(t *testing.T) {
	b := goldenBacklogScreen()
	for range 5 {
		b.Update(key("down"))
	}
	view := ansi.Strip(b.View(110))
	for _, want := range []string{"⚠ half-written", "will not load", "no title in the header", "is still on disk"} {
		if !strings.Contains(view, want) {
			t.Errorf("the broken row never says %q:\n%s", want, view)
		}
	}
	pressAll(b, "kkk")
	if !strings.Contains(slugsShowing(b), "half-written") {
		t.Errorf("a kind filter hid the row that has no kind: %q", slugsShowing(b))
	}
}

// The dependency is drawn at both ends and walkable: the row says what it
// waits on, the pane says what waits on it, and the key lands on it.
func TestBacklogScreen_DependenciesAreDrawnAndWalked(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("down"))
	view := ansi.Strip(b.View(130))
	for _, want := range []string{"waits on rail-todo-block", "waits on rail-todo-block, prose-renderer"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen never says %q:\n%s", want, view)
		}
	}
	pressAll(b, "w")
	if got := b.current(); got == nil || got.Slug != "rail-todo-block" {
		t.Fatalf("[w] landed on %+v, want rail-todo-block", got)
	}
	// And the pane on that item states the other end of the same edge.
	if !strings.Contains(ansi.Strip(b.View(130)), "1 item waits on this: screen-over-items") {
		t.Error("the item's own pane does not say what waits on it")
	}
}

// A jump into a row the filters are hiding takes the filters off rather than
// landing on nothing.
func TestBacklogScreen_JumpClearsAFilterHidingItsTarget(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("down"))
	pressAll(b, "s")
	if strings.Contains(slugsShowing(b), "rail-todo-block") {
		t.Fatal("the fixture no longer hides the dependency; the case is not being tested")
	}
	pressAll(b, "w")
	if got := b.current(); got == nil || got.Slug != "rail-todo-block" {
		t.Fatalf("[w] landed on %+v with a filter up", got)
	}
	if b.filterWords() != "" {
		t.Errorf("the filter should have come off, header says %q", b.filterWords())
	}
}

// The three keys that change a file ask first, and the one that loses
// information says what it loses.
func TestBacklogScreen_DestructiveKeysAskFirst(t *testing.T) {
	for _, tc := range []struct {
		press  string
		prompt string
		act    BacklogAct
	}{
		{"b", "Block rail-todo-block?", BacklogBlock},
		{"d", "Archive rail-todo-block?", BacklogArchive},
		{"x", "Drop rail-todo-block? The file is deleted, not archived.", BacklogDrop},
	} {
		b := goldenBacklogScreen()
		_, result := pressAll(b, tc.press)
		if result.Do != nil {
			t.Fatalf("[%s] acted without asking", tc.press)
		}
		if got := ansi.Strip(b.View(110)); !strings.Contains(got, tc.prompt) {
			t.Fatalf("[%s] asked %q, want it to name %q", tc.press, got, tc.prompt)
		}
		_, declined := b.Update(key("n"))
		if declined.Do != nil {
			t.Fatalf("[%s] acted on a no", tc.press)
		}
		pressAll(b, tc.press)
		_, agreed := b.Update(key("y"))
		if agreed.Do == nil || agreed.Do.Act != tc.act || agreed.Do.Slug != "rail-todo-block" {
			t.Fatalf("[%s] resolved to %+v", tc.press, agreed.Do)
		}
	}
}

// The keys that ask nothing resolve straight to the act, and the sprint key
// reads the row to decide which of its two halves it is.
func TestBacklogScreen_KeysThatAskNothing(t *testing.T) {
	b := goldenBacklogScreen()
	if _, r := pressAll(b, "e"); r.Do == nil || r.Do.Act != BacklogEdit {
		t.Fatalf("[e] resolved to %+v", r.Do)
	}
	if _, r := pressAll(b, "R"); r.Do == nil || r.Do.Act != BacklogRun {
		t.Fatalf("[R] resolved to %+v", r.Do)
	}
	if _, r := pressAll(b, "n"); r.Do == nil || r.Do.Act != BacklogNew {
		t.Fatalf("[n] resolved to %+v", r.Do)
	}
	// The first row is already in the sprint, so the key drops it; the third
	// is not, so the same key adds it.
	if _, r := pressAll(b, "S"); r.Do == nil || r.Do.Act != BacklogSprintDrop {
		t.Fatalf("[S] on a row in the sprint resolved to %+v", r.Do)
	}
	b.Update(key("down"))
	b.Update(key("down"))
	if _, r := pressAll(b, "S"); r.Do == nil || r.Do.Act != BacklogSprintAdd {
		t.Fatalf("[S] on a row outside the sprint resolved to %+v", r.Do)
	}
}

// Invariant 5 over a working turn: the keys that change a file do nothing,
// they are not offered live, and the footer says why.
func TestBacklogScreen_StateKeysAreInertWhileATurnWorks(t *testing.T) {
	b := goldenBacklogScreen()
	b.ReadOnly = true
	for _, press := range []string{"b", "d", "x", "e", "R", "S", "o", "n"} {
		if _, r := pressAll(b, press); r.Do != nil {
			t.Errorf("[%s] acted while a turn was working: %+v", press, r.Do)
		}
		if b.confirm != nil {
			t.Errorf("[%s] armed a confirm while a turn was working", press)
		}
	}
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, b.whyInert()) {
		t.Errorf("the footer never says why the keys are grey:\n%s", view)
	}
	for _, offer := range b.offers() {
		if strings.Contains(offer.Key, "[x]") {
			t.Error("a key that cannot act is still being offered")
		}
	}
	// Reading is untouched: the filters and the pointer change no file.
	if _, r := pressAll(b, "/"); r.Do != nil || !b.filtering {
		t.Error("the filter should stay live while a turn works")
	}
}

// The archive is the second tab: its bodies are the reports, its keys are
// the two that mean anything there, and the status and ready filters do not
// come with it.
func TestBacklogScreen_ArchiveTab(t *testing.T) {
	b := goldenBacklogScreen()
	pressAll(b, "s")
	b.Update(key("tab"))
	if !b.archived() || b.status != 0 {
		t.Fatalf("the tab should carry no status filter, archive=%v status=%d", b.archived(), b.status)
	}
	view := ansi.Strip(b.View(110))
	for _, want := range []string{"backlog · done", "2 items", "the one place a key is written down", "[o] put it back in the backlog"} {
		if !strings.Contains(view, want) {
			t.Errorf("the archive never says %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[R] run it") || strings.Contains(view, "[x] drop it") {
		t.Errorf("the archive offers a key it cannot answer:\n%s", view)
	}
	if _, r := pressAll(b, "o"); r.Do == nil || r.Do.Act != BacklogReopen {
		t.Fatalf("[o] in the archive resolved to %+v", r.Do)
	}
}

// Under the pane width the body folds under the list and is never beside it.
func TestBacklogScreen_BodyFoldsUnderTheList(t *testing.T) {
	b := goldenBacklogScreen()
	if !strings.Contains(ansi.Strip(b.View(backlogStackWidth)), "│") {
		t.Error("at the threshold the two panes should sit side by side")
	}
	narrow := ansi.Strip(b.View(backlogStackWidth - 1))
	if strings.Contains(narrow, "│") {
		t.Errorf("under the threshold nothing should sit beside the list:\n%s", narrow)
	}
	if !strings.Contains(narrow, "rail-todo-block") {
		t.Errorf("the folded layout lost the item:\n%s", narrow)
	}
}

// [enter] hands the body the keys and the pager counts what is off the ends;
// the way back is a step rather than an exit.
func TestBacklogScreen_ReadingTheBody(t *testing.T) {
	b := goldenBacklogScreen()
	b.MaxLines = 12
	b.Update(key("enter"))
	if !b.reading {
		t.Fatal("[enter] should hand the body the keys")
	}
	b.Update(key("down"))
	b.Update(key("down"))
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, "rows above") {
		t.Errorf("a scrolled body should say what is off the top:\n%s", view)
	}
	done, _ := b.Update(key("esc"))
	if done || b.reading {
		t.Fatalf("the way back from the body is the list, not the prompt (done=%v reading=%v)", done, b.reading)
	}
	if done, _ := b.Update(key("esc")); !done {
		t.Fatal("the second press should leave the screen")
	}
}

// The pointer is per tab: coming back to the backlog lands where the reader
// left it rather than at the top.
func TestBacklogScreen_EachTabKeepsItsPointer(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("down"))
	b.Update(key("down"))
	b.Update(key("tab"))
	b.Update(key("down"))
	if got := b.current(); got == nil || got.Slug != "windowed-list" {
		t.Fatalf("the archive's pointer is on %+v", got)
	}
	b.Update(key("tab"))
	if got := b.current(); got == nil || got.Slug != "prose-renderer" {
		t.Fatalf("the backlog's pointer moved to %+v", got)
	}
	b.Update(key("tab"))
	if got := b.current(); got == nil || got.Slug != "windowed-list" {
		t.Fatalf("the archive's pointer moved to %+v", got)
	}
}

// The query row's own corners: `q` is a letter there, the arrows still move
// the pointer, and esc closes the row rather than the screen.
func TestBacklogScreen_QueryRowCorners(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("/"))
	pressAll(b, "q")
	if !b.filtering || b.query != "q" {
		t.Fatalf("q closed the query row instead of typing into it: filtering=%v query=%q", b.filtering, b.query)
	}
	b.Update(key("ctrl+u"))
	b.Update(key("down"))
	if got := b.current(); got == nil || got.Slug != "screen-over-items" {
		t.Fatalf("the arrows should still move the pointer under an open query, landed on %+v", got)
	}
	done, _ := b.Update(key("esc"))
	if done || b.filtering {
		t.Fatalf("esc should close the row and not the screen (done=%v filtering=%v)", done, b.filtering)
	}
}

// The one act that is about the backlog rather than about a row answers on
// an empty list, which is the list it is most needed on — and the empty
// state offers it, so the offer and the handler cannot disagree.
func TestBacklogScreen_StartingAnItemWorksOnAnEmptyList(t *testing.T) {
	b := &BacklogScreen{MaxLines: 20}
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, "no items yet") {
		t.Fatalf("an empty backlog should say so:\n%s", view)
	}
	_, r := pressAll(b, "n")
	if r.Do == nil || r.Do.Act != BacklogNew {
		t.Fatalf("[n] on an empty list resolved to %+v", r.Do)
	}
	if !strings.Contains(view, keyOffers([]KeyOffer{keyOffer(keys.Backlog.New)})) {
		t.Errorf("the empty list does not offer the key it answers:\n%s", view)
	}
}

// A body that fits spends no row saying what it folded. The row is a fold's
// own account of itself, and a blank one costs a line of the item.
func TestBacklogScreen_AnItemThatFitsSpendsNoFoldRow(t *testing.T) {
	b := goldenBacklogScreen()
	b.MaxLines = 30
	b.Update(key("enter"))
	tall := strings.Split(ansi.Strip(b.View(110)), "\n")
	for _, row := range tall {
		if strings.Contains(row, "rows above") || strings.Contains(row, "more rows below") {
			t.Fatalf("an item that fits drew a fold marker:\n%s", strings.Join(tall, "\n"))
		}
	}
	b.MaxLines = 12
	short := ansi.Strip(b.View(110))
	if !strings.Contains(short, "more rows below") {
		t.Errorf("an item that does not fit should say how much it folded:\n%s", short)
	}
}

// researchFields is a second vocabulary, as a project that keeps a reading
// list would hand it over: questions and readings, graded by how deep the
// answer has to go. The screen draws its letters and narrows on its words
// without holding one of them.
func researchFields() (BacklogField, []BacklogField) {
	priority, _ := goldenBacklogFields()
	return priority, []BacklogField{
		{Name: "kind", Values: []BacklogValue{
			{Word: "question", Glyph: "Q"}, {Word: "reading", Glyph: "R"}}},
		{Name: "depth", Values: []BacklogValue{
			{Word: "quick", Glyph: "Q"}, {Word: "deep", Glyph: "D"}}},
	}
}

// A second vocabulary draws its own letters on the rows and narrows on its
// own words, and the footer names the field a filter stopped on — "reading"
// on its own would not say what was narrowed.
func TestBacklogScreen_DrawsASecondVocabulary(t *testing.T) {
	b := &BacklogScreen{MaxLines: 24, Rows: []BacklogRow{
		{Slug: "why-tabs", Title: "Why tabs", Priority: "high", Status: "open",
			Values: map[string]string{"kind": "reading", "depth": "deep"}, State: BacklogReady},
		{Slug: "who-reads-it", Title: "Who reads it", Priority: "low", Status: "open",
			Values: map[string]string{"kind": "question"}, State: BacklogReady},
	}}
	b.Priority, b.Fields = researchFields()
	view := ansi.Strip(b.View(110))
	// The first row is graded and the second is not, which is the hyphen.
	for _, want := range []string{"why-tabs  HRD", "who-reads-it  LQ-"} {
		if !strings.Contains(view, want) {
			t.Errorf("the rows never draw %q:\n%s", want, view)
		}
	}
	pressAll(b, "kk")
	if got := slugsShowing(b); got != "why-tabs" {
		t.Errorf("[k] left %q, want why-tabs", got)
	}
	if got := b.filterWords(); got != "kind reading" {
		t.Errorf("the header says %q, want %q", got, "kind reading")
	}
	// The cycle runs on into the second field rather than stopping at the
	// first, which is what one key over a list of fields has to do.
	pressAll(b, "k")
	if got := b.filterWords(); got != "depth quick" {
		t.Errorf("after three presses the header says %q", got)
	}
}
