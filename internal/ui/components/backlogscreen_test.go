package components

// The backlog screen against its own rules: what each word narrows to,
// what a key does to the row under the pointer, and which keys are not live
// while a turn is working.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

func pressAll(b *BacklogScreen, text string) (done bool, result backlogResult) {
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
	for _, i := range b.filter.shown {
		out = append(out, b.rows()[i].Slug)
	}
	return strings.Join(out, " ")
}

// query types words into the filter row and closes it with enter, keeping
// them: the list's own keys are live again over the list they narrowed.
func query(b *BacklogScreen, words string) {
	b.Update(key("/"))
	pressAll(b, words)
	b.Update(key("enter"))
}

// The register is the eleven keys and the two every surface has: move,
// page, read, the tabs, the filter, edit, new, drop, status, the sprint and
// run, then ? and esc. The filter letters and the dependency jump are gone,
// so the list moves on j/k like every other.
func TestBacklog_TheRegisterIsElevenKeys(t *testing.T) {
	var got []string
	for _, b := range keys.Backlog.All() {
		got = append(got, keys.Shown(b))
	}
	want := []string{"↑↓/jk", "enter", "pgup/pgdn", "tab", "/", "e", "n", "d", "s", "space", "r", "?", "esc"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the register is %v, want %v", got, want)
	}
	if keys.Shown(keys.Sprint.Take) != "ctrl+s" {
		t.Errorf("the sprint card writes on %q, want ctrl+s", keys.Shown(keys.Sprint.Take))
	}
	for _, gone := range []string{"R", "S", "b", "o", "a", "g", "w", "p", "x"} {
		for _, b := range keys.Backlog.All() {
			if keys.Is(gone, b) {
				t.Errorf("%q still answers %q", gone, keys.Words(b))
			}
		}
	}
	b := goldenBacklogScreen()
	b.Update(key("j"))
	if got := b.current(); got == nil || got.Slug != "screen-over-items" {
		t.Fatalf("j left the pointer on %+v", got)
	}
	b.Update(key("k"))
	if got := b.current(); got == nil || got.Slug != "rail-todo-block" {
		t.Fatalf("k left the pointer on %+v", got)
	}
}

// The query takes words: the status words, a field by any prefix of its
// name and a word, and free text found in the slug or the title. Every word
// has to hold, and the header says the words.
func TestBacklog_TheQueryFiltersByWord(t *testing.T) {
	for _, tc := range []struct {
		query, want string
	}{
		{"ready", "prose-renderer drop-loses-the-file half-written"},
		{"blocked", "sprint-file half-written"},
		{"done", "half-written"},
		{"p:high", "rail-todo-block screen-over-items half-written"},
		{"kind:bug", "drop-loses-the-file half-written"},
		{"kind:story ready", "half-written"},
		{"ready p:low", "drop-loses-the-file half-written"},
		{"renderer", "prose-renderer"},
		{"Dropping", "drop-loses-the-file"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			b := goldenBacklogScreen()
			query(b, tc.query)
			if got := slugsShowing(b); got != tc.want {
				t.Errorf("%q left %q, want %q", tc.query, got, tc.want)
			}
			if got := b.filter.words(); got != "matching "+tc.query {
				t.Errorf("the header says %q", got)
			}
		})
	}
	// esc clears the query before it leaves: the first press is the whole
	// list back, and only the second is the way out.
	b := goldenBacklogScreen()
	query(b, "ready")
	if b.filter.filtering {
		t.Fatal("enter should close the query row and keep the words")
	}
	if done, _ := b.Update(key("esc")); done || b.filter.query != "" {
		t.Fatalf("the first esc should clear the query, done=%v query=%q", done, b.filter.query)
	}
	if got := slugsShowing(b); !strings.Contains(got, "rail-todo-block") {
		t.Errorf("the cleared query left %q", got)
	}
	if done, _ := b.Update(key("esc")); !done {
		t.Fatal("esc over no query should leave")
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
// and esc walks the row out a level at a time: it clears the query, then
// closes the row and hands them back.
func TestBacklogScreen_QueryRowKeepsTheLettersUntilItCloses(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("/"))
	pressAll(b, "sr")
	if b.filter.query != "sr" || b.picker != nil {
		t.Fatalf("letters typed into the query opened a picker: query=%q picker=%v", b.filter.query, b.picker != nil)
	}
	b.Update(key("esc"))
	if b.filter.query != "" || !b.filter.filtering {
		t.Fatalf("the first clear should empty the query and leave the row open, got %q filtering=%v", b.filter.query, b.filter.filtering)
	}
	b.Update(key("esc"))
	if b.filter.filtering {
		t.Fatal("esc over an empty query should close the row")
	}
	pressAll(b, "s")
	if b.picker == nil {
		t.Fatal("the letters should be live again once the row has closed")
	}
}

// A file that cannot be read is a row rather than a gap, and it survives the
// field words because it has no fields to answer them with: the row is the
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
	query(b, "kind:chore")
	if !strings.Contains(slugsShowing(b), "half-written") {
		t.Errorf("a kind word hid the row that has no kind: %q", slugsShowing(b))
	}
}

// The dependency is drawn at both ends: the row says what it waits on, and
// the pane on the item it waits on says what waits on it.
func TestBacklogScreen_DependenciesAreDrawnAtBothEnds(t *testing.T) {
	b := goldenBacklogScreen()
	b.Update(key("down"))
	view := ansi.Strip(b.View(130))
	for _, want := range []string{"waits on rail-todo-block", "waits on rail-todo-block, prose-renderer"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen never says %q:\n%s", want, view)
		}
	}
	b.Update(key("up"))
	if !strings.Contains(ansi.Strip(b.View(130)), "1 item waits on this: screen-over-items") {
		t.Error("the item's own pane does not say what waits on it")
	}
}

// The drop asks first, and says what it loses.
func TestBacklogScreen_TheDropAsksFirst(t *testing.T) {
	b := goldenBacklogScreen()
	_, result := pressAll(b, "d")
	if result.Do != nil {
		t.Fatal("[d] acted without asking")
	}
	prompt := "Drop rail-todo-block? The file is deleted, not archived."
	if got := ansi.Strip(b.View(110)); !strings.Contains(got, prompt) {
		t.Fatalf("[d] asked %q, want it to name %q", got, prompt)
	}
	if _, declined := b.Update(key("n")); declined.Do != nil {
		t.Fatal("[d] acted on a no")
	}
	pressAll(b, "d")
	_, agreed := b.Update(key("y"))
	if agreed.Do == nil || agreed.Do.Act != BacklogDrop || agreed.Do.Slug != "rail-todo-block" {
		t.Fatalf("[d] resolved to %+v", agreed.Do)
	}
}

// [s] is a picker of where the item can stand — open, blocked, done — with
// the one it stands at now marked and inert, and esc a step back to the
// list rather than off the screen.
func TestBacklog_TheStatusPickerSetsWhereItStands(t *testing.T) {
	for _, tc := range []struct {
		press string
		want  backlogAct
	}{
		{"enter", BacklogReopen},
		{"2", BacklogBlock},
		{"3", BacklogArchive},
	} {
		b := goldenBacklogScreen()
		pressAll(b, "s")
		if b.picker == nil {
			t.Fatal("[s] opened no picker")
		}
		view := ansi.Strip(b.View(130))
		for _, want := range []string{"set the status of rail-todo-block", "open", "blocked", "done"} {
			if !strings.Contains(view, want) {
				t.Errorf("the picker never says %q:\n%s", want, view)
			}
		}
		_, r := b.Update(key(tc.press))
		if r.Do == nil || r.Do.Act != tc.want || r.Do.Slug != "rail-todo-block" {
			t.Errorf("[%s] on the status picker resolved to %+v", tc.press, r.Do)
		}
		if b.picker != nil {
			t.Error("taking a row left the picker up")
		}
	}
	// The status the item already has is marked, and taking it changes
	// nothing.
	b := goldenBacklogScreen()
	for range 3 {
		b.Update(key("down"))
	}
	pressAll(b, "s")
	if !strings.Contains(ansi.Strip(b.View(130)), "now") {
		t.Errorf("the blocked item's picker does not mark where it stands:\n%s", ansi.Strip(b.View(130)))
	}
	if _, r := b.Update(key("2")); r.Do != nil {
		t.Errorf("taking the status it has acted: %+v", r.Do)
	}
	pressAll(b, "s")
	if done, r := b.Update(key("esc")); done || r.Do != nil || b.picker != nil {
		t.Fatalf("esc on the picker should close it and stay: done=%v do=%+v", done, r.Do)
	}
}

// [r] is a picker of the two ways a turn is spent on an item: run it, or
// groom it. Neither starts until a row is taken.
func TestBacklog_TheRunPickerRunsOrGrooms(t *testing.T) {
	b := goldenBacklogScreen()
	if _, r := pressAll(b, "r"); r.Do != nil || b.picker == nil {
		t.Fatalf("[r] should open a picker and start nothing: %+v", r.Do)
	}
	if _, r := b.Update(key("enter")); r.Do == nil || r.Do.Act != BacklogRun {
		t.Fatalf("the run picker's first row resolved to %+v", r.Do)
	}
	pressAll(b, "r")
	b.Update(key("j"))
	if _, r := b.Update(key("enter")); r.Do == nil || r.Do.Act != BacklogGroom {
		t.Fatalf("the run picker's second row resolved to %+v", r.Do)
	}
}

// The keys that ask nothing resolve straight to the act, and the sprint key
// reads the row to decide which of its two halves it is.
func TestBacklogScreen_KeysThatAskNothing(t *testing.T) {
	b := goldenBacklogScreen()
	if _, r := pressAll(b, "e"); r.Do == nil || r.Do.Act != BacklogEdit {
		t.Fatalf("[e] resolved to %+v", r.Do)
	}
	if _, r := pressAll(b, "n"); r.Do == nil || r.Do.Act != BacklogNew {
		t.Fatalf("[n] resolved to %+v", r.Do)
	}
	// The first row is already in the sprint, so the key drops it; the third
	// is not, so the same key adds it.
	if _, r := b.Update(key("space")); r.Do == nil || r.Do.Act != BacklogSprintDrop {
		t.Fatalf("[space] on a row in the sprint resolved to %+v", r.Do)
	}
	b.Update(key("down"))
	b.Update(key("down"))
	if _, r := b.Update(key("space")); r.Do == nil || r.Do.Act != BacklogSprintAdd {
		t.Fatalf("[space] on a row outside the sprint resolved to %+v", r.Do)
	}
}

// Invariant 5 over a working turn: the keys that change a file do nothing,
// they are not offered live, and the footer says why.
func TestBacklogScreen_StateKeysAreInertWhileATurnWorks(t *testing.T) {
	b := goldenBacklogScreen()
	b.ReadOnly = true
	for _, press := range []string{"s", "d", "e", "r", "space", "n"} {
		if _, r := b.Update(key(press)); r.Do != nil {
			t.Errorf("[%s] acted while a turn was working: %+v", press, r.Do)
		}
		if b.confirm != nil || b.picker != nil {
			t.Errorf("[%s] armed a confirm or a picker while a turn was working", press)
		}
	}
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, b.foot().whyInert()) {
		t.Errorf("the footer never says why the keys are grey:\n%s", view)
	}
	for _, offer := range b.foot().offers(110) {
		if strings.Contains(offer.Key, "[d]") || strings.Contains(offer.Key, "[e]") {
			t.Error("a key that cannot act is still being offered")
		}
	}
	// Reading is untouched: the filter and the pointer change no file.
	if _, r := pressAll(b, "/"); r.Do != nil || !b.filter.filtering {
		t.Error("the filter should stay live while a turn works")
	}
}

// The foot is one row at every width, and `[?]` is where the rest are: the
// row carries the keys pressed every time, the header keeps the way out, and
// the register still lists every key the screen answers.
func TestBacklogScreen_FootIsOneRow(t *testing.T) {
	for _, width := range append([]int{40}, goldenWidths...) {
		b := goldenBacklogScreen()
		b.sync()
		rows := b.foot().rows(width)
		if len(rows) != 1 {
			t.Fatalf("at %d columns the foot is %d rows:\n%s", width, len(rows), strings.Join(rows, "\n"))
		}
		foot := ansi.Strip(rows[0])
		for _, want := range []string{"[↑↓/jk] move", "[enter] read"} {
			if !strings.Contains(foot, want) {
				t.Errorf("at %d columns the foot sheds %q: %s", width, want, foot)
			}
		}
		if width >= 80 {
			for _, want := range []string{"[/] filter", "[e] edit", "[n] new", "[esc] back"} {
				if !strings.Contains(foot, want) {
					t.Errorf("at %d columns the foot sheds %q: %s", width, want, foot)
				}
			}
		}
	}

	// The archive's row carries its own verb in the editor's place, and at
	// every width: its words shorten before anything but the way out and the
	// new item gives ground.
	for _, width := range append([]int{40}, goldenWidths...) {
		b := goldenBacklogScreen()
		b.Update(key("tab"))
		rows := b.foot().rows(width)
		if len(rows) != 1 {
			t.Fatalf("at %d columns the archive's foot is %d rows:\n%s", width, len(rows), strings.Join(rows, "\n"))
		}
		foot := ansi.Strip(rows[0])
		want := "[s] put it back"
		if width >= 80 {
			want = "[s] put it back in the backlog"
		}
		if width > 40 && !strings.Contains(foot, want) {
			t.Errorf("at %d columns the archive's foot never offers %q: %s", width, want, foot)
		}
		if strings.Contains(foot, "[e]") {
			t.Errorf("at %d columns the archive's foot offers the editor over its own verb: %s", width, foot)
		}
	}

	// The query row's foot is three keys, esc walking the row out a level at
	// a time, and never offers fewer than them, down to the narrowest width a
	// surface is drawn at. It moves on the arrows alone: j is a letter there.
	for _, width := range goldenWidths {
		b := goldenBacklogScreen()
		pressAll(b, "/")
		rows := b.foot().rows(width)
		if len(rows) != 1 {
			t.Fatalf("at %d columns the filter's foot is %d rows:\n%s", width, len(rows), strings.Join(rows, "\n"))
		}
		foot := ansi.Strip(rows[0])
		for _, want := range []string{"[↑↓] move", "[enter] keep it", "[esc]"} {
			if !strings.Contains(foot, want) {
				t.Errorf("at %d columns the filter's foot sheds %q: %s", width, want, foot)
			}
		}
		if width >= 80 && !strings.Contains(foot, "[esc] clear the filter, then close it") {
			t.Errorf("at %d columns the filter's foot gives up esc's words with room for them: %s", width, foot)
		}
	}

	// With words narrowing the list the way out says it clears them first.
	held := goldenBacklogScreen()
	query(held, "ready")
	if foot := ansi.Strip(strings.Join(held.foot().rows(110), "\n")); !strings.Contains(foot, "[esc] clear the filter") {
		t.Errorf("a held query's foot never says esc clears it: %s", foot)
	}

	// `?` lists the keys in the register's own words.
	b := goldenBacklogScreen()
	pressAll(b, "?")
	register := ansi.Strip(strings.Join(b.foot().rows(110), "\n"))
	for _, want := range []string{
		"[↑↓/jk] move", "[enter] open", "[tab] the backlog, the sprint, or what shipped",
		"[e] edit", "[n] new item", "[d] delete", "[s] set its status", "[space] toggle it in the sprint",
		"[r] run it", "[esc] back",
	} {
		if !strings.Contains(register, want) {
			t.Errorf("[?] never lists %q:\n%s", want, register)
		}
	}
}

// The archive is the second tab: its bodies are the reports, its keys are
// the two that mean anything there, and the words asking where an active
// item stands do not come with it.
func TestBacklogScreen_ArchiveTab(t *testing.T) {
	b := goldenBacklogScreen()
	query(b, "ready chore")
	b.Update(key("tab"))
	if !b.archived() || b.filter.query != "chore" {
		t.Fatalf("the tab should drop the status word and keep the rest, archive=%v query=%q", b.archived(), b.filter.query)
	}
	b.Update(key("esc"))
	view := ansi.Strip(b.View(110))
	for _, want := range []string{"backlog · done", "2 items", "the one place a key is written down"} {
		if !strings.Contains(view, want) {
			t.Errorf("the archive never says %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[r] run it") || strings.Contains(view, "[d] delete") {
		t.Errorf("the archive offers a key it cannot answer:\n%s", view)
	}
	if _, r := pressAll(b, "r"); r.Do != nil || b.picker != nil {
		t.Fatalf("[r] in the archive did something: %+v", r.Do)
	}
	// Reopening is the archive's own verb: its row offers it, and a turn
	// greys out the same words.
	if got := keyOffers(b.foot().stateOffers()); !strings.Contains(ansi.Strip(got), "[s] put it back in the backlog") {
		t.Errorf("the archive's verbs lost the reopen: %s", ansi.Strip(got))
	}
	pressAll(b, "s")
	if b.picker == nil || len(b.picker.card.Options) != 1 {
		t.Fatalf("the archive's status picker should be the one move back")
	}
	if _, r := b.Update(key("enter")); r.Do == nil || r.Do.Act != BacklogReopen {
		t.Fatalf("[s] in the archive resolved to %+v", r.Do)
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
	b.maxLines = 12
	b.Update(key("enter"))
	if !b.reader.reading {
		t.Fatal("[enter] should hand the body the keys")
	}
	b.Update(key("down"))
	b.Update(key("down"))
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, "rows above") {
		t.Errorf("a scrolled body should say what is off the top:\n%s", view)
	}
	done, _ := b.Update(key("esc"))
	if done || b.reader.reading {
		t.Fatalf("the way back from the body is the list, not the prompt (done=%v reading=%v)", done, b.reader.reading)
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
	if !b.filter.filtering || b.filter.query != "q" {
		t.Fatalf("q closed the query row instead of typing into it: filtering=%v query=%q", b.filter.filtering, b.filter.query)
	}
	b.Update(key("esc"))
	b.Update(key("down"))
	if got := b.current(); got == nil || got.Slug != "screen-over-items" {
		t.Fatalf("the arrows should still move the pointer under an open query, landed on %+v", got)
	}
	done, _ := b.Update(key("esc"))
	if done || b.filter.filtering {
		t.Fatalf("esc should close the row and not the screen (done=%v filtering=%v)", done, b.filter.filtering)
	}
}

// The one act that is about the backlog rather than about a row answers on
// an empty list, which is the list it is most needed on — and the empty
// state offers it, so the offer and the handler cannot disagree.
func TestBacklogScreen_StartingAnItemWorksOnAnEmptyList(t *testing.T) {
	b := &BacklogScreen{maxLines: 20}
	view := ansi.Strip(b.View(110))
	if !strings.Contains(view, "no items yet") {
		t.Fatalf("an empty backlog should say so:\n%s", view)
	}
	_, r := pressAll(b, "n")
	if r.Do == nil || r.Do.Act != BacklogNew {
		t.Fatalf("[n] on an empty list resolved to %+v", r.Do)
	}
	if !strings.Contains(view, ansi.Strip(keyOffers(b.foot().fileOffers()))) {
		t.Errorf("the empty list does not offer the key it answers:\n%s", view)
	}
}

// A body that fits spends no row saying what it folded. The row is a fold's
// own account of itself, and a blank one costs a line of the item.
func TestBacklogScreen_AnItemThatFitsSpendsNoFoldRow(t *testing.T) {
	b := goldenBacklogScreen()
	b.maxLines = 30
	b.Update(key("enter"))
	tall := strings.Split(ansi.Strip(b.View(110)), "\n")
	for _, row := range tall {
		if strings.Contains(row, "rows above") || strings.Contains(row, "more rows below") {
			t.Fatalf("an item that fits drew a fold marker:\n%s", strings.Join(tall, "\n"))
		}
	}
	b.maxLines = 12
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

// The tally counts in the word the project uses for one item. "12 items" on
// a list of questions names the backlog by a word that appears nowhere in it.
func TestBacklogScreen_TheTallyCountsInTheProjectsOwnNoun(t *testing.T) {
	rows := []BacklogRow{
		{Slug: "why-tabs", Title: "Why tabs", Priority: "high", Status: "open", State: BacklogReady},
		{Slug: "who-reads-it", Title: "Who reads it", Priority: "low", Status: "open", State: BacklogReady},
	}
	b := &BacklogScreen{maxLines: 24, Rows: rows, Noun: "question"}
	b.Priority, b.Fields = researchFields()
	b.sync()
	if got := b.count(); got != "2 questions" {
		t.Errorf("the tally says %q", got)
	}
	// A host that names none gets the word every backlog started with, so
	// nothing has to state the ordinary answer.
	plain := &BacklogScreen{maxLines: 24, Rows: rows}
	plain.sync()
	if got := plain.count(); got != "2 items" {
		t.Errorf("the tally with no noun says %q", got)
	}
}

// A second vocabulary draws its own letters on the rows and narrows on its
// own words, each field named by its own name or any prefix of it.
func TestBacklogScreen_DrawsASecondVocabulary(t *testing.T) {
	b := &BacklogScreen{maxLines: 24, Rows: []BacklogRow{
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
	query(b, "kind:reading")
	if got := slugsShowing(b); got != "why-tabs" {
		t.Errorf("kind:reading left %q, want why-tabs", got)
	}
	b.Update(key("esc"))
	query(b, "d:quick")
	if got := slugsShowing(b); got != "" {
		t.Errorf("d:quick left %q; neither row is quick", got)
	}
	b.Update(key("esc"))
	query(b, "kind:q")
	if got := slugsShowing(b); got != "who-reads-it" {
		t.Errorf("kind:q left %q, want who-reads-it", got)
	}
}

// The `?` reveal says what a row's letters stand for, and names the fields
// the query takes words for, in the words of whichever vocabulary the
// screen was handed: nothing about them is written for one profile.
func TestBacklogScreen_TheKeysSayWhatASecondVocabularysLettersMean(t *testing.T) {
	b := &BacklogScreen{maxLines: 60, Rows: []BacklogRow{
		{Slug: "why-tabs", Title: "Why tabs", Priority: "high", Status: "open", State: BacklogReady},
	}}
	b.Priority, b.Fields = researchFields()
	pressAll(b, "?")
	view := ansi.Strip(b.View(130))
	for _, want := range []string{
		"letters  priority H high · M medium · L low — kind Q question · R reading — depth Q quick · D deep — - unset",
		"ready, blocked, done, priority:high, kind:question, depth:quick, or a title",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the reveal never says %q:\n%s", want, view)
		}
	}
}
