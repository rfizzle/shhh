package chat

// Provider failures in the session (
// docs/interface/surfaces.md#the-recovery-row). A failure is drawn as a card
// the way a step is: the model and what the turn had done when it broke on
// the header, what the failure means for the reader as the body, and the
// provider's own words with the ways out as the footer.
//
// A stream that broke used to append `Error: <whatever Go said>` and hand the
// input back. Everything a reader needed was missing: whether the key was the
// problem, whether the three edits already applied survived, and what to
// press next. The classification arrives from internal/provider now; this
// file owns the two things the provider cannot know — how the failure reads
// on the grid, and which key gets you out of it in a session.
//
// The keys differ by class on purpose. A rate limit and a rejected key are
// both "the request failed", and the only useful thing to say about them is
// how differently they are fixed.

import (
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/receipt"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The keys a failure row can offer are keys.Row.Retry, Continue, Key and
// Provider, declared in the register rather than inline so the dispatch
// and the offers cannot drift apart.
//
// They are handled by focus mode on the row, so the input keeps all four
// letters for typing — which
// matters more here than anywhere else, since "run the tests again" and
// "check what it did" are exactly what gets typed after a failure. That is
// also why entering a key is [e] and not the artboard's [k]: k is the focus
// cursor's own.

// maxFailureDetail bounds the provider's own words on the row. The detail
// body exists so an unclassified failure still says something; it does not
// exist to paste an HTML error page into the transcript.
const maxFailureDetail = 3

// appendFailure records a classified provider failure as a transcript entry.
func (m *Model) appendFailure(err error) {
	m.appendFailureRecord(classifyFailure(err, m.providerName))
}

// classifyFailure names an error that somehow arrived unclassified rather
// than letting it through raw: no raw provider error string reaches the
// transcript.
func classifyFailure(err error, providerName string) *provider.Failure {
	if f, ok := provider.AsFailure(err); ok {
		return f
	}
	return &provider.Failure{
		Class:    provider.ClassUnclassified,
		Provider: providerName,
		Message:  err.Error(),
	}
}

// appendFailureRecord puts an already-classified failure on the grid. The
// pointer is kept as-is, because the retry wait identifies the row it stalled
// on by it.
//
// The card's header is read here, as the failure lands: the model the turn
// was on and what it had done, which a provider switch or a retry would
// otherwise rewrite under a failure that happened before either.
func (m *Model) appendFailureRecord(f *provider.Failure) {
	var acts []receipt.Act
	for _, e := range m.turnEntries() {
		if isActivityEntry(e) {
			acts = append(acts, m.actOf(e))
		}
	}
	m.appendEntry(entry{kind: entryFailure, fail: f, duration: m.turnElapsed(),
		failAfter: receipt.BuildStep(acts).Counts(), failModel: m.failureSubject(f)})
}

// failureCardFor is a failure entry as its card, in the state the selection
// puts its keys in (inertkeys.go). The note the row trailed its keys with is
// the body's second sentence on the card, since what the failure cost is
// part of what it means.
func (m Model) failureCardFor(e entry, sel rowSel) components.FailureCard {
	row := m.gateRecovery(e, m.failureRow(e), sel)
	if e.failModel != "" {
		row.Subject = e.failModel
	}
	sentence := failureSentence(e.fail)
	if row.Note != "" {
		sentence += " " + strings.ToUpper(row.Note[:1]) + row.Note[1:] + "."
	} else if e.fail != nil {
		// A row whose wait is draining under it hands its note and keys to
		// the wait; the card still says what the failure cost.
		note := failureNote(e.fail)
		sentence += " " + strings.ToUpper(note[:1]) + note[1:] + "."
	}
	return components.FailureCard{
		Row:      row,
		After:    e.failAfter,
		Sentence: sentence,
		Selected: sel != rowUnselected,
	}
}

// failureSentence is what a failure means for the reader, said as a
// sentence: the fact about its class that decides what to do next, which
// the row used to state in its outcome field and the card's header gives to
// the class.
func failureSentence(f *provider.Failure) string {
	if f == nil {
		return ""
	}
	switch f.Class {
	case provider.ClassAuth:
		if f.KeyTail == "" {
			return "No key was sent, so the provider refused the request."
		}
		return "The provider rejected key ···" + f.KeyTail + "."
	case provider.ClassRateLimit:
		if f.RetryAfter > 0 {
			return "The provider is limiting the rate and named a retry in " + components.FormatElapsed(f.RetryAfter) + "."
		}
		return "The provider is limiting the rate; a retry shortly should go through."
	case provider.ClassQuota:
		return "The account has run out of quota; it is the account, not the rate."
	case provider.ClassOverloaded:
		return "The provider is overloaded; the trouble is on its side."
	case provider.ClassContextLength:
		return "The conversation is over the model's context window."
	case provider.ClassModelNotFound:
		return "The provider serves no model by that id; the id is wrong, not the key."
	case provider.ClassNetwork:
		return "The request never reached the provider."
	case provider.ClassMalformed:
		return "The provider's reply could not be read."
	case provider.ClassCancelled:
		return "You stopped the request."
	}
	return "The provider refused the request; its own words are below."
}

// failureRow renders one failure on the column grid. The offers stay on the
// row whether or not they are still claimable: the row is a record of what
// was said, and a record that rewrites itself as you type is worse than a key
// that has quietly moved on.
func (m Model) failureRow(e entry) components.RecoveryRow {
	f := e.fail
	if f == nil {
		return components.RecoveryRow{}
	}
	row := components.RecoveryRow{
		State:     components.RecoveryBroken,
		Verb:      components.VerbModel,
		Subject:   m.failureSubject(f),
		Qualifier: f.Headline(),
		Outcome:   failureOutcome(f),
		Duration:  turnDuration(e.duration),
		Detail:    f.Detail(),
		MaxDetail: maxFailureDetail,
		Keys:      m.failureKeys(f),
		Note:      failureNote(f),
	}
	switch {
	case f.Class == provider.ClassCancelled:
		row.State = components.RecoveryStopped
	case f.Recoverable():
		row.State = components.RecoveryStalled
	}
	// While this row's own failure is being waited out, the live countdown
	// under it carries the offers. Two sets of keys for one stall
	// would be two answers to the same question.
	if m.retry != nil && m.retry.fail == f {
		row.Keys, row.Note = nil, ""
	}
	return row
}

// failureSubject leads the target field: the model that failed, falling back
// to the provider when the session never learned a model name.
func (m Model) failureSubject(f *provider.Failure) string {
	if m.modelName != "" {
		return m.modelName
	}
	if f.Provider != "" {
		return f.Provider
	}
	return m.providerName
}

// failureOutcome is the right-aligned field: the one fact about this class
// that decides what to do next. It never repeats the class, which is already
// in the target.
func failureOutcome(f *provider.Failure) string {
	switch f.Class {
	case provider.ClassAuth:
		if f.KeyTail == "" {
			return "no key was sent"
		}
		return "key ···" + f.KeyTail + " rejected"
	case provider.ClassRateLimit:
		if f.RetryAfter > 0 {
			return "retry in " + components.FormatElapsed(f.RetryAfter)
		}
		return "retry shortly"
	case provider.ClassQuota:
		return "the account, not the rate"
	case provider.ClassOverloaded:
		return "the provider's side"
	case provider.ClassContextLength:
		return "over the window"
	case provider.ClassModelNotFound:
		return "the id, not the key"
	case provider.ClassNetwork:
		return "never reached it"
	case provider.ClassMalformed:
		return "unreadable reply"
	case provider.ClassCancelled:
		return "stopped"
	}
	return "message below"
}

// failureNote trails the keys. The question a reader actually has after a
// failure is not "what broke" but "what did that cost me", and the answer is
// almost always the same one — the request never reached the conversation, so
// the turn's edits, its steps and its transcript are all still there. Two
// classes have something more useful to say instead.
func failureNote(f *provider.Failure) string {
	switch f.Class {
	case provider.ClassQuota:
		return "waiting will not clear this one"
	case provider.ClassContextLength:
		return "compacting keeps the plan and the recent turns"
	case provider.ClassModelNotFound:
		return "/model lists what this provider serves"
	}
	return "nothing in the turn was lost"
}

// failureKeys are the ways out of one class, in the order worth trying. A key
// the session cannot honour is not offered: a provider that cannot be
// switched offers no [p], and a session with nothing to compact offers no
// [c].
func (m Model) failureKeys(f *provider.Failure) []components.KeyOffer {
	var offers []components.KeyOffer
	add := func(b keys.Binding, label string) {
		offers = append(offers, rowOffer(b, label))
	}
	switch f.Class {
	case provider.ClassAuth:
		if m.replaceKeyFn != nil {
			add(keys.Row.Key, "enter a new key")
		}
		if m.canSwitchProvider() {
			add(keys.Row.Provider, "switch provider")
		}
	case provider.ClassQuota:
		if m.canSwitchProvider() {
			add(keys.Row.Provider, "switch provider")
		}
	case provider.ClassContextLength:
		add(keys.Row.Continue, "compact now")
		add(keys.Row.Retry, "then try again")
	case provider.ClassModelNotFound:
		// No [r]. The id is the failure, and the next request would carry
		// the same one — the note sends the reader to the picker that can
		// change it instead.
		if m.canSwitchProvider() {
			add(keys.Row.Provider, "switch provider")
		}
	case provider.ClassCancelled:
		// You stopped it on purpose. Offering a key here would be the
		// interface arguing with the decision.
	default:
		add(keys.Row.Retry, "try again")
		if m.canSwitchProvider() {
			add(keys.Row.Provider, "switch provider")
		}
	}
	return offers
}

// canSwitchProvider reports whether [p] can do anything: a switcher wired in,
// and more than one provider registered to switch to.
func (m Model) canSwitchProvider() bool {
	return m.switchProviderFn != nil && len(m.providerChoices()) > 1
}

// providerChoices is the registered providers, sorted — the built-ins plus
// any gateway profile this process loaded, since a profile is a
// provider to everything downstream of the registry.
func (m Model) providerChoices() []string {
	names := provider.Available()
	sort.Strings(names)
	return names
}

// focusedFailure returns the failure row the focus cursor is on, if it is on
// one. Failures live in the session's own transcript, so an attached child's
// feed never offers them.
func (m Model) focusedFailure() (entry, bool) {
	if m.attachedTo != "" || m.focusIdx < 0 || m.focusIdx >= len(m.transcript) {
		return entry{}, false
	}
	e := m.transcript[m.focusIdx]
	if e.kind != entryFailure || e.fail == nil {
		return entry{}, false
	}
	return e, true
}

// failureKey routes a keystroke to the live failure row. It reports false
// when the row is not claiming the key, leaving every ordinary handler
// exactly as it was.
func (m Model) failureKey(key string) (tea.Model, tea.Cmd, bool) {
	e, ok := m.focusedFailure()
	if !ok {
		return m, nil, false
	}
	offered := false
	for _, k := range m.failureKeys(e.fail) {
		if strings.Trim(k.Key, "[]") == key {
			offered = true
			break
		}
	}
	if !offered {
		return m, nil, false
	}
	switch key {
	case keys.Shown(keys.Row.Retry):
		next, cmd := m.retryTurn()
		return next, cmd, true
	case keys.Shown(keys.Row.Continue):
		next, cmd := m.startCompact()
		return next, cmd, true
	case keys.Shown(keys.Row.Key):
		next, cmd := m.openKeyEntry(e.fail)
		return next, cmd, true
	case keys.Shown(keys.Row.Provider):
		next, cmd := m.openProviderPick()
		return next, cmd, true
	}
	return m, nil, false
}

// retryTurn asks the model again with the conversation exactly as the failed
// turn left it. Nothing is re-sent and nothing is dropped: the request that
// broke never reached the transcript, so asking again is asking the same
// question, not asking twice.
func (m Model) retryTurn() (tea.Model, tea.Cmd) {
	if m.working() {
		return m.systemNotice("the turn is already running again")
	}
	// The failed request never reached the conversation, so asking again is
	// asking the same question rather than asking twice. What restarts is the
	// turn's own accounting: a retry is a turn, and /stats should say so.
	m.clearRetryChain()
	// The retry stands where the reader's words would, saying what it asks
	// again: the same prompt, and the model it asks it of where a provider
	// switch since the failure put the session on another one.
	retry := entry{kind: entryRetry}
	if was := m.lastFailureModel(); was != "" && m.modelName != "" && was != m.modelName {
		retry.failModel = m.modelName
	}
	m.appendEntry(retry)
	m.turnStarted, m.turnEnded = clock(), time.Time{}
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnTokensIn, m.turnTokensOut = 0, 0
	m.vitals.startTurn()
	m.resetRounds()
	m.setTurnState(stateStreaming)
	m.streaming = ""
	m.atBottom = true
	m.trimForRequest()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, m.requestStream()
}

// lastFailureModel is the model the newest failure in the transcript was on.
func (m Model) lastFailureModel() string {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if e := m.transcript[i]; e.kind == entryFailure {
			return e.failModel
		}
	}
	return ""
}

// openProviderPick opens the generic picker over the registered providers
// (statePick), focused on the session's own.
func (m Model) openProviderPick() (tea.Model, tea.Cmd) {
	choices := m.providerChoices()
	opts := make([]components.SelectOption, len(choices))
	focus := 0
	for i, name := range choices {
		label := name
		if name == m.providerName {
			label += "  (current)"
			focus = i
		}
		opts[i] = components.SelectOption{Label: label, Desc: providerDesc(name)}
	}
	updated, cmd := m.openPicker("Switch provider", opts, focus, func(m *Model, idx int) string {
		name := choices[idx]
		if name == m.providerName {
			return "Already on " + name + "."
		}
		if err := m.switchProviderFn(name); err != nil {
			return "Could not switch to " + name + ": " + err.Error()
		}
		m.providerName = name
		if model := provider.Defaults(name).Model; model != "" {
			m.modelName = model
		}
		return "switched to " + name + " on " + m.modelName + ". Ask again to use it"
	})
	// Esc leaves the session on the provider it is on, and says which one
	// (docs/interface/principles.md#esc-is-always-the-safe-answer); a session
	// that names none keeps the family's words.
	next := updated.(Model)
	if m.providerName != "" {
		next.picker.CancelLabel = "keep " + m.providerName
	}
	return next, cmd
}

// providerDesc is the one-line description a provider row carries: the model
// a session on it would start on, which is the part of the choice that is not
// in the name.
func providerDesc(name string) string {
	if model := provider.Defaults(name).Model; model != "" {
		return "starts on " + model
	}
	return ""
}

// openKeyEntry borrows the bottom panel for the masked key prompt. The
// failure's own key hint is what the prompt names, so the reader is told
// which variable the replacement stands in for.
func (m Model) openKeyEntry(f *provider.Failure) (tea.Model, tea.Cmd) {
	if m.replaceKeyFn == nil {
		return m.systemNotice("this session cannot replace its key")
	}
	m.keyAsk = &components.SecretPrompt{
		Prompt:  "Paste a key for " + m.providerName,
		Hint:    f.KeyEnv,
		Replace: f.KeyTail,
	}
	m.enterSurface(stateKeyEntry)
	m.syncViewport()
	return m, nil
}

// updateKeyEntry routes keys while the prompt is up. Esc declines and writes
// nothing — the old key stays exactly where it was.
func (m *Model) answerKeyEntry(msg tea.KeyPressMsg) (bool, overlayAction) {
	if m.keyAsk == nil {
		return true, m.closeKeyEntry("")
	}
	done, result := m.keyAsk.Update(msg)
	if !done {
		m.syncViewport()
		return false, overlayAction{}
	}
	return true, m.closeKeyEntry(result.Value)
}

// closeKeyEntry hands the screen back and applies the key, if one was given.
// The notice names the key by its last four characters and never by more.
func (m *Model) closeKeyEntry(secret string) overlayAction {
	m.keyAsk = nil
	if strings.TrimSpace(secret) == "" {
		return overlayAction{close: true, note: "key unchanged"}
	}
	if err := m.replaceKeyFn(secret); err != nil {
		return overlayAction{close: true, note: "that key was not accepted: " + err.Error()}
	}
	return overlayAction{close: true, note: "key ···" + lastFour(secret) +
		" is in use for this session. Ask again to try it, or set provider.api_key in /config to keep it"}
}

// keyEntryLines renders the prompt for the bottom panel.
func (m Model) keyEntryLines() []string {
	if m.keyAsk == nil {
		return nil
	}
	return strings.Split(m.keyAsk.View(m.contentWidth()), "\n")
}

// lastFour is the tail of a secret, the only part of one that is ever shown.
func lastFour(secret string) string {
	runes := []rune(strings.TrimSpace(secret))
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return string(runes)
}
