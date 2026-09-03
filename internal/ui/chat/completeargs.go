package chat

// Argument-level completion. The command registry in complete.go
// carries a positional argument spec per command: a static subcommand list
// (/memory list|add|forget) or a dynamic source (saved chat names, branch
// names, the model catalog, checkpoint numbers). Dynamic sources are read
// once per menu — cached on the model, keyed by the command the menu is open
// for — so arrowing and typing never hit the database. Command names keep
// prefix matching; long dynamic lists fall back to subsequence matching when
// no prefix matches, so "/load btn" still finds "beta-notes".

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// argOption is one candidate for an argument position: the value written
// into the input plus a one-line description for the menu's second column.
type argOption struct {
	value string
	desc  string
}

// argSpec describes one positional argument. options is the static list;
// dynamic (when set) replaces it and is resolved lazily. after gates the
// position on the preceding token, so "/ui verbosity <low|normal|high>" is
// two plain specs rather than a special case. fuzzy allows subsequence
// matching for lists the user cannot be expected to type from the front.
type argSpec struct {
	options []argOption
	dynamic func(*Model) []argOption
	after   []string
	fuzzy   bool
}

// argPositions counts the argument positions a command has. A run of
// consecutive gated specs is a set of alternatives for one position, not one
// position each, so the count is not simply len(argSpecs) — /ui has two
// positions across three specs.
func argPositions(c slashCommand) int {
	n, prevGated := 0, false
	for _, s := range c.argSpecs {
		gated := len(s.after) > 0
		if !gated || !prevGated {
			n++
		}
		prevGated = gated
	}
	return n
}

// staticArgs is the common case: one position with a fixed candidate list.
func staticArgs(opts ...argOption) []argSpec {
	return []argSpec{{options: opts}}
}

// lookupCommand finds the registry row for a typed command name (or alias)
// that this session actually has wired.
func lookupCommand(m *Model, name string) (slashCommand, bool) {
	for _, c := range slashCommands {
		if c.enabled != nil && !c.enabled(m) {
			continue
		}
		if c.name == name {
			return c, true
		}
		for _, a := range c.aliases {
			if a == name {
				return c, true
			}
		}
	}
	return slashCommand{}, false
}

// argSpecFor returns the spec for argument position pos of cmd, given the
// tokens already typed. It reports false when the position is free-form (a
// chat name, a memory body) or gated on a different preceding token.
//
// Several gated specs may share one position: /ui's second argument is a
// verbosity level after "verbosity" and an on/off after "mono", so a
// gate that does not match falls through to the next alternative instead of
// ending the search.
func argSpecFor(c slashCommand, pos int, prior []string) (argSpec, bool) {
	if pos < 0 || pos >= len(c.argSpecs) {
		return argSpec{}, false
	}
	spec := c.argSpecs[pos]
	if len(spec.after) == 0 {
		return spec, true
	}
	if len(prior) == 0 {
		return argSpec{}, false
	}
	prev := prior[len(prior)-1]
	for _, alt := range c.argSpecs[pos:] {
		if len(alt.after) > 0 && containsString(alt.after, prev) {
			return alt, true
		}
	}
	return argSpec{}, false
}

// argCandidates resolves a position's candidates, reading a dynamic source
// at most once per open menu (the cache is dropped when the menu closes or
// moves to another command).
func (m *Model) argCandidates(cmd string, pos int, spec argSpec) []argOption {
	if spec.dynamic == nil {
		return spec.options
	}
	if m.argCacheFor != cmd || m.argCache == nil {
		m.argCache = make(map[int][]argOption)
		m.argCacheFor = cmd
	}
	if opts, ok := m.argCache[pos]; ok {
		return opts
	}
	opts := spec.dynamic(m)
	m.argCache[pos] = opts
	return opts
}

// filterArgs ranks candidates against the token under the cursor: an exact
// match first, then prefix matches in registry order, then — for fuzzy specs
// only — subsequence matches.
func filterArgs(opts []argOption, token string, fuzzy bool) []argOption {
	tok := strings.ToLower(token)
	var exact, prefix, sub []argOption
	for _, o := range opts {
		v := strings.ToLower(o.value)
		switch {
		case v == tok:
			exact = append(exact, o)
		case strings.HasPrefix(v, tok):
			prefix = append(prefix, o)
		case fuzzy && subsequence(v, tok):
			sub = append(sub, o)
		}
	}
	out := make([]argOption, 0, len(exact)+len(prefix)+len(sub))
	out = append(out, exact...)
	out = append(out, prefix...)
	return append(out, sub...)
}

// subsequence reports whether every rune of token appears in s in order.
func subsequence(s, token string) bool {
	if token == "" {
		return true
	}
	want := []rune(token)
	i := 0
	for _, r := range s {
		if r == want[i] {
			if i++; i == len(want) {
				return true
			}
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// tokenAtCursor splits the input at the cursor: the whitespace-separated
// tokens fully before the cursor's token, the current token's text, and the
// rune offsets the token spans (so tab replaces only it).
func tokenAtCursor(val string, cursor int) (prior []string, token string, start, end int) {
	r := []rune(val)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(r) {
		cursor = len(r)
	}
	start = cursor
	for start > 0 && !unicode.IsSpace(r[start-1]) {
		start--
	}
	end = cursor
	for end < len(r) && !unicode.IsSpace(r[end]) {
		end++
	}
	return strings.Fields(string(r[:start])), string(r[start:cursor]), start, end
}

// inputCursor is the cursor's rune offset within the input's current line.
// Completion only runs on single-line input, so that is the whole value.
func (m Model) inputCursor() int {
	info := m.input.LineInfo()
	return info.StartColumn + info.ColumnOffset
}

// --- dynamic sources -------------------------------------------------------

// modelArgs offers the /model picker's catalog to "/model <name>".
func modelArgs(m *Model) []argOption {
	choices := m.modelPickChoices()
	out := make([]argOption, 0, len(choices))
	for _, name := range choices {
		desc := ""
		if name == m.modelName {
			desc = "current"
		} else if m.prices != nil {
			if in, outCost, ok := m.prices.Cost(name, 1_000_000, 1_000_000); ok {
				desc = fmt.Sprintf("$%.2f in / $%.2f out per Mtok", in, outCost)
			}
		}
		out = append(out, argOption{value: name, desc: desc})
	}
	return out
}

// scopeDropArgs offers the directories this session has added to its working
// scope — the only ones /add-dir drop can take back, since the
// session's own directory is never dropped.
func scopeDropArgs(m *Model) []argOption {
	dirs := m.scopeDirs()
	out := make([]argOption, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, argOption{value: d, desc: "Stop the session writing here"})
	}
	return out
}

// attachmentDropArgs offers the attachments staged for the next message
// — the names `/paste drop` takes back out. A chip has no key of its
// own, so its name is the handle, and this is what keeps the handle
// from having to be typed from memory.
func attachmentDropArgs(m *Model) []argOption {
	out := make([]argOption, 0, len(m.attachments))
	for _, a := range m.attachments {
		out = append(out, argOption{value: a.Name,
			desc: "Drop this attachment · " + attachment.HumanSize(len(a.Data))})
	}
	return out
}

// attachmentShowArgs offers the staged attachments `/paste show` can open
// . Not the PDFs: shhh does not render one, so the surface refuses
// them, and a menu that offered a name it would then decline is a menu that
// made the reader find that out by typing.
func attachmentShowArgs(m *Model) []argOption {
	out := make([]argOption, 0, len(m.attachments))
	for _, a := range m.attachments {
		what := "Look at this image · "
		switch a.Kind {
		case provider.AttachmentImage:
		case provider.AttachmentText:
			what = "Read this text · "
		default:
			continue
		}
		out = append(out, argOption{value: a.Name,
			desc: what + attachment.HumanSize(len(a.Data))})
	}
	return out
}

// railArgs offers the inspector rail's widths: the ladder, and the two ends
// of the range a number is held to. It is dynamic rather than a static list
// because the useful third offer is what this terminal allows right now — a
// person on a 144-column screen who is offered 72 has been offered a number
// the layout will cut down.
func railArgs(m *Model) []argOption {
	out := []argOption{{value: components.RailWidthAuto, desc: "Widen the rail with the terminal"}}
	here := components.InspectorWidthFor(m.contentWidth())
	if here > components.InspectorWidth {
		out = append(out, argOption{value: strconv.Itoa(here),
			desc: "As wide as this terminal allows"})
	}
	return append(out, argOption{value: strconv.Itoa(components.InspectorWidth),
		desc: "The narrowest rail — the most transcript"})
}

// modeArgs offers the session's mode cycle plus /permissions' own
// subcommands.
func modeArgs(m *Model) []argOption {
	cycle := m.modeCycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	out := make([]argOption, 0, len(cycle)+1)
	for _, mode := range cycle {
		desc := mode.Describe()
		if mode == m.mode {
			desc = "current — " + desc
		}
		out = append(out, argOption{value: mode.String(), desc: desc})
	}
	return append(out,
		argOption{value: "why", desc: "Explain the last approval decision"},
		argOption{value: "grants", desc: "What this session has stopped asking about"},
		argOption{value: "allow", desc: "Grant a whole category, for the session"},
		argOption{value: "revoke", desc: "Take the session's grants back"})
}

// agentArgs offers this session's sub-agents for /attach, blocked ones
// first — those are the agents waiting on the user.
func agentArgs(m *Model) []argOption {
	if m.subagents == nil {
		return nil
	}
	var blocked, rest []argOption
	for _, st := range m.subagents.Snapshot() {
		opt := argOption{value: st.Name, desc: st.Detail}
		if st.State == subagent.StateBlocked {
			blocked = append(blocked, opt)
		} else {
			rest = append(rest, opt)
		}
	}
	out := append(blocked, rest...)
	if m.attachedTo != "" {
		out = append(out, argOption{value: "orchestrator", desc: "back to your own session"})
	}
	return out
}

// sessionFileArgs offers the paths this session has changed, each described
// by what it cost the file. It is the rail's CHANGES block as a list, in the
// same order the rail draws it, so the row a reader is looking at is the row
// the menu offers them.
//
// The spec is fuzzy because a path is long and its distinguishing part is at
// the end: nobody types the directories to reach the file they mean.
func sessionFileArgs(m *Model) []argOption {
	files := m.changes.SessionFiles()
	out := make([]argOption, 0, len(files))
	for _, f := range files {
		// A file whose whole change is its permissions has no lines to count,
		// so the description says the change it has where it would say the
		// counts — the same substitution the rail's row makes, and for the
		// same reason: `+0 −0` beside a real change is a zero nothing
		// measured.
		// See docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out.
		change := fmt.Sprintf("+%d −%d", f.Added, f.Removed)
		if f.ModeChange != "" && f.Added == 0 && f.Removed == 0 {
			change = f.ModeChange
		}
		out = append(out, argOption{
			value: f.Path,
			desc:  fmt.Sprintf("%s · %s", change, plural(f.Turns, "turn")),
		})
	}
	return out
}

// reviewTurnArgs offers the turns the changeset store still holds, latest
// first, described by what each of them changed.
func reviewTurnArgs(m *Model) []argOption {
	turns := m.changes.Turns()
	out := make([]argOption, 0, len(turns))
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		out = append(out, argOption{
			value: strconv.FormatInt(t.N, 10),
			desc:  fmt.Sprintf("%s · +%d −%d", plural(t.Files(), "file"), t.Added, t.Removed),
		})
	}
	return out
}

// checkpointArgs offers the rewind turn numbers, latest first.
func checkpointArgs(m *Model) []argOption {
	out := make([]argOption, 0, len(m.checkpoints))
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		out = append(out, argOption{
			value: strconv.Itoa(i + 1),
			desc:  m.checkpoints[i].preview,
		})
	}
	return out
}

// branchArgs offers this session's branch family.
func branchArgs(m *Model) []argOption {
	if m.db == nil {
		return nil
	}
	branches, err := m.db.ListChatBranches(m.sessionName)
	if err != nil {
		return nil
	}
	out := make([]argOption, 0, len(branches))
	for _, b := range branches {
		desc := sessionDesc(b.Turns, b.UpdatedAt)
		if b.Name == m.sessionName {
			desc = "current — " + desc
		}
		out = append(out, argOption{value: b.Name, desc: desc})
	}
	return out
}

// chatArgs offers the saved chats, most recently updated first.
func chatArgs(m *Model) []argOption {
	if m.db == nil {
		return nil
	}
	entries, err := m.db.ListChats()
	if err != nil {
		return nil
	}
	out := make([]argOption, 0, len(entries))
	for _, e := range entries {
		out = append(out, argOption{value: e.Name, desc: chatDesc(e)})
	}
	return out
}
