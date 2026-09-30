package chat

// The toolchain draft. A checkout with no `.shhh/toolchain.toml` leaves the
// person to learn the file's grammar and its pin rules before a sandboxed
// session can build and test it, and one that has a declaration leaves them
// to notice when it has fallen behind the checks. So a session offers to read
// the checkout and draft the file — or review the one there — and puts the
// result on a card (docs/capabilities/containment.md#a-declaration-can-be-drafted-for-you).
//
// It is the scaffold card's shape and for the scaffold card's reason: a
// suggestion that wrote a file the moment it was chosen would be the one row
// on the start screen worth more than it says
// (docs/interface/surfaces.md#the-start-screen). The draft is read beside the
// conversation and never into it, the way a backlog reading is, and nothing
// is written until the card's yes.

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// toolchainCommandName is the command the offer stands for, named once so
// the start screen's row and the register cannot drift apart.
const toolchainCommandName = "/toolchain"

// ToolchainDraft is one drafting's answer, as the host hands it back: a
// declaration the loader has already read, or why there is none.
type ToolchainDraft struct {
	// Content is the file as the card's yes would write it. The host puts
	// it through the loader before it is handed back, so a draft that would
	// not load never reaches the card.
	Content []byte
	// Previous is the file as it stands, nil where there is none: what the
	// card's diff is taken against.
	Previous []byte
	// Provides names, for each install line, the binaries it puts on PATH,
	// as the model said. A line it said nothing about names nothing.
	Provides map[string][]string
	// Changes are a review's proposals, each with its reason.
	Changes []ToolchainChange
	// Unchanged is a review that found nothing to change.
	Unchanged bool
	// Err is a drafting that produced no declaration that loads.
	Err string
}

// ToolchainChange is one change a review proposes and the reason for it.
type ToolchainChange struct {
	Change string
	Reason string
}

// toolchainDrafting is one drafting in flight or waiting on its card. It
// lives on the session's toolchain state rather than on the Model, and the
// result message carries the pointer it was started under, so an answer
// that arrives after the session boundary dropped it is dropped too.
type toolchainDrafting struct {
	review bool
	busy   bool
	cancel context.CancelFunc
	// draft is the answer, once there is one to put on the card.
	draft *ToolchainDraft
}

// toolchainDraftMsg is a drafting's answer.
type toolchainDraftMsg struct {
	flow  *toolchainDrafting
	draft ToolchainDraft
}

// toolchainDraftWired reports a session that can draft the declaration and
// has somebody to put the card to — which is what /toolchain exists in.
func (m Model) toolchainDraftWired() bool {
	return m.toolchain().Draft != nil && m.toolchain().WriteDraft != nil
}

// toolchainDraftOffered reports whether the start screen spends its
// read-only offer on the draft: a wired session, in a checkout that is a
// repository or holds a build file the survey recognised. Anywhere else there
// are no checks to read a toolchain out of.
func (m Model) toolchainDraftOffered() bool {
	if !m.toolchainDraftWired() || m.start == nil {
		return false
	}
	p := m.start.Project
	return p.Repo || p.Language != ""
}

// toolchainDraftTitle is the offer's words: a draft where there is no file,
// a review where there is one.
func toolchainDraftTitle(review bool) string {
	if review {
		return "review this checkout's toolchain declaration"
	}
	return "draft this checkout's toolchain declaration"
}

// toolchainCommand is /toolchain: the card when a draft is waiting on it,
// the drafting when none is, or why neither can happen here.
func (m Model) toolchainCommand() (tea.Model, tea.Cmd) {
	if !m.toolchainDraftWired() {
		return m.systemNotice("this session cannot draft a toolchain declaration: it has no checkout of its own to read")
	}
	if f := m.toolchain().drafting; f != nil {
		if f.busy {
			return m.systemNotice("still reading the checkout — the card opens when the draft is ready")
		}
		if f.draft != nil {
			return m.openToolchainDraft()
		}
	}
	review := m.toolchain().Exists
	ctx, cancel := context.WithCancel(context.Background())
	f := &toolchainDrafting{review: review, busy: true, cancel: cancel}
	m.containment.Toolchain.drafting = f
	draft := m.toolchain().Draft
	what := "drafting " + project.ToolchainFile
	if review {
		what = "reviewing " + project.ToolchainFile
	}
	next, note := m.systemNotice(what + " from the checkout — it reads only, and the card opens when it is done")
	return next, tea.Batch(note, func() tea.Msg {
		defer cancel()
		return toolchainDraftMsg{flow: f, draft: draft(ctx, review)}
	})
}

// finishToolchainDraft takes a drafting's answer. The card opens only where
// the screen is free for it; anywhere else the answer waits for /toolchain,
// because a card that took the keyboard from a surface the reader was using
// would answer a key meant for that surface.
func (m Model) finishToolchainDraft(msg toolchainDraftMsg) (tea.Model, tea.Cmd) {
	f := m.toolchain().drafting
	if f == nil || f != msg.flow {
		return m, nil
	}
	f.busy = false
	switch {
	case msg.draft.Err != "":
		m.containment.Toolchain.drafting = nil
		return m.systemNotice("could not draft " + project.ToolchainFile + ": " + msg.draft.Err)
	case msg.draft.Unchanged:
		m.containment.Toolchain.drafting = nil
		return m.systemNotice(project.ToolchainFile + " already names what the checkout's checks need: nothing to change")
	}
	d := msg.draft
	f.draft = &d
	if !m.screenIsFree() || m.interruptShowing() {
		return m.systemNotice("the " + project.ToolchainFile + " draft is ready — " + toolchainCommandName + " opens it")
	}
	return m.openToolchainDraft()
}

// openToolchainDraft puts the card up. It is a takeover: the reader asked
// for it, so it holds the keyboard the way every summoned surface does.
func (m Model) openToolchainDraft() (tea.Model, tea.Cmd) {
	m.enterSurface(stateToolchainDraft)
	m.syncViewport()
	return m, nil
}

// dropToolchainDraft retires a drafting in flight or waiting: the session
// boundary calls it, since the draft was asked for by the session being left.
func (m *Model) dropToolchainDraft() {
	f := m.containment.Toolchain.drafting
	if f == nil {
		return
	}
	if f.cancel != nil {
		f.cancel()
	}
	m.containment.Toolchain.drafting = nil
	if m.state == stateToolchainDraft {
		m.leaveSurface()
	}
}

// toolchainDraftCard is the approval card for the write: the file as a diff
// against what is there, each install line with what it provides, the
// registries the lines reach, and — where the checkout is not trusted — that
// the file will not be read until it is.
func (m Model) toolchainDraftCard() *components.ApprovalCard {
	tc := m.toolchain()
	f := tc.drafting
	if f == nil || f.draft == nil {
		return &components.ApprovalCard{Variant: components.ApprovalGeneric, Title: "Approve toolchain declaration"}
	}
	d := f.draft
	// The content was read by the loader before it was handed over, and
	// again after an edit, so a refusal here would be a fault of this
	// session's own; the card then shows the file with no rows under it
	// rather than rows it cannot vouch for.
	read, _ := project.ParseToolchain(d.Content)
	act := "write " + project.ToolchainFile
	if d.Previous != nil {
		act = "rewrite " + project.ToolchainFile
	}
	card := &components.ApprovalCard{
		// The edit variant, because the body is a file's diff: a new file
		// is all additions, and a review is the change against the file as
		// it stands.
		Variant:  components.ApprovalEdit,
		Title:    "Approve toolchain declaration",
		ActGlyph: "✎",
		Act:      act,
		Hunks:    diff.Compute(string(d.Previous), string(d.Content)),
		Syntax:   diffSyntax(project.ToolchainFile),
		Answer:   "write it",
		Decline:  "nothing written",
		Return:   "leave — nothing written, and the draft waits",
		ExtraHints: []components.KeyOffer{
			{Key: keys.Bracket(keys.Decision.Revise), Label: "edit first"},
		},
		MaxLines: m.planPanelBound(),
		KeyList:  true,
	}
	// Each line with what it provides, on a row of its own under it: a
	// detail beside a line this long is the first thing a narrow terminal
	// drops, and the pairing is the reason the rows are here at all.
	for _, line := range read.Install {
		card.Fields = append(card.Fields, components.CardField{Label: "install", Value: line})
		if names := d.Provides[line]; len(names) > 0 {
			card.Fields = append(card.Fields, components.CardField{Value: "provides " + strings.Join(names, ", "), Tone: components.ToneChrome})
		}
	}
	hosts := components.CardField{Label: "reaches", Value: "the profile's network", Detail: "the declaration names no registry"}
	if len(read.Hosts) > 0 {
		hosts.Value, hosts.Detail = strings.Join(read.Hosts, ", "), "the only hosts an install on this machine may reach"
	}
	card.Fields = append(card.Fields, hosts)
	if len(read.Check) > 0 {
		card.Fields = append(card.Fields, components.CardField{Label: "checks", Value: strings.Join(read.Check, " · "), Detail: "looked for on PATH when a session starts"})
	}
	// Each change on a row and its reason on the row under it, for the
	// reason the provides rows are split: the reason is what the review is
	// for, and a detail is dropped first.
	for _, c := range d.Changes {
		card.Fields = append(card.Fields, components.CardField{Label: "change", Value: c.Change})
		if c.Reason != "" {
			card.Fields = append(card.Fields, components.CardField{Label: "because", Value: c.Reason, Tone: components.ToneChrome})
		}
	}
	if tc.Untrusted {
		// The draft is written either way; the loader will not read it
		// until the checkout is trusted, and a card that let the reader find
		// that out from the next session's silence would have hidden the
		// one thing standing between the file and its use.
		card.Fields = append(card.Fields, components.CardField{Label: "loads", Value: "once trusted: shhh trust",
			Detail: "this checkout is not trusted, so no session reads the file until then", Tone: components.ToneOpen})
	}
	return card
}

// answerToolchainDraft routes the card's keys the way the scaffold card's
// are routed (scaffold.go): esc leaves with the draft waiting, [n] drops it,
// [e] hands it to the editor, and only the yes writes anything.
func (m *Model) answerToolchainDraft(msg tea.KeyPressMsg) (bool, overlayAction) {
	f := m.toolchain().drafting
	switch {
	case keys.Match(msg, keys.Select.Cancel):
		return true, overlayAction{close: true, note: "nothing written; " + toolchainCommandName + " opens the draft again"}
	case keys.Match(msg, keys.Decision.Refuse):
		m.containment.Toolchain.drafting = nil
		return true, overlayAction{close: true, note: "nothing written; " + toolchainCommandName + " drafts it again"}
	case keys.Match(msg, keys.Decision.Revise):
		return true, m.editToolchainDraft()
	case keys.Match(msg, keys.Decision.Accept):
		if f == nil || f.draft == nil {
			return true, overlayAction{close: true}
		}
		path, err := m.toolchain().WriteDraft(f.draft.Content)
		if err != nil {
			return true, overlayAction{close: true, note: "could not write " + project.ToolchainFile + ": " + err.Error() + ". The draft waits for " + toolchainCommandName}
		}
		m.containment.Toolchain.drafting = nil
		// The session reads the file again once the write lands (toolchain.go),
		// so /status and /setup have it now; the assistant's prompt was
		// written before it existed, so the row says where that changes.
		note := "wrote " + path + ". /status and /setup read it now; the assistant is told of it from /new."
		if m.toolchain().Untrusted {
			note = "wrote " + path + ". It loads once the checkout is trusted — shhh trust."
		}
		m.containment.Toolchain.Exists = true
		return true, overlayAction{close: true, note: note}
	}
	return false, overlayAction{}
}

// toolchainEditorDoneMsg is the editor's exit from a draft. The file it was
// handed is removed on every path back.
type toolchainEditorDoneMsg struct {
	path string
	err  error
}

// editToolchainDraft hands the draft to the editor over a temporary file, as
// the backlog's draft card does: the declaration does not exist yet, and esc
// must still leave nothing in the checkout. The card comes down for the
// handoff and goes back up holding what the editor wrote.
func (m *Model) editToolchainDraft() overlayAction {
	f := m.toolchain().drafting
	if f == nil || f.draft == nil {
		return overlayAction{close: true}
	}
	if m.working() || m.frameWorking() {
		return overlayAction{note: "not while the turn is running — the editor takes the terminal with it. The draft is still on the card"}
	}
	path, err := writeToolchainDraftFile(f.draft.Content)
	if err != nil {
		return overlayAction{note: "could not write the draft out — " + err.Error() + ". The draft is still on the card"}
	}
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return overlayAction{close: true, run: tea.ExecProcess(proc, func(err error) tea.Msg {
		return toolchainEditorDoneMsg{path: path, err: err}
	})}
}

// writeToolchainDraftFile puts the draft where an editor can reach it, named
// so an editor that picks a mode from the extension picks TOML.
func writeToolchainDraftFile(content []byte) (string, error) {
	f, err := os.CreateTemp("", "shhh-toolchain-*.toml")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// toolchainEditorFinished takes the file back onto the card, through the
// loader: an edit it refuses leaves the card on the draft before it and says
// why in the loader's own words, so the card never shows a file the next
// session would refuse.
func (m Model) toolchainEditorFinished(msg toolchainEditorDoneMsg) (tea.Model, tea.Cmd) {
	defer func() { _ = os.Remove(msg.path) }()
	f := m.toolchain().drafting
	if f == nil || f.draft == nil {
		return m, nil
	}
	back := func(note string) (tea.Model, tea.Cmd) {
		m.enterSurface(stateToolchainDraft)
		m.syncViewport()
		if note == "" {
			return m, nil
		}
		return m.systemNotice(note)
	}
	if msg.err != nil {
		return back("The editor exited with an error, so the draft is as it was — " + msg.err.Error())
	}
	content, err := os.ReadFile(msg.path)
	if err != nil {
		return back("Could not read the draft back, so it is as it was — " + err.Error())
	}
	read, err := project.ParseToolchain(content)
	if err != nil {
		return back("The edit would not load, so the draft is as it was — " + err.Error())
	}
	edited := *f.draft
	edited.Content = content
	// What each line provides is the model's word about that line; a line
	// the edit rewrote is one it said nothing about.
	edited.Provides = map[string][]string{}
	for line, names := range f.draft.Provides {
		if slices.Contains(read.Install, line) {
			edited.Provides[line] = names
		}
	}
	f.draft = &edited
	return back("")
}

// toolchainDraftLines renders the card, one row per line.
func (m Model) toolchainDraftLines() []string {
	return strings.Split(m.toolchainDraftCard().View(m.contentWidth()), "\n")
}
