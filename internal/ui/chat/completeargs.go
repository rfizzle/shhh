package chat

// Argument-level completion (S-079). The command registry in complete.go
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
)

// argOption is one candidate for an argument position: the value written
// into the input plus a one-line description for the menu's second column.
type argOption struct {
	value string
	desc  string
}

// argSpec describes one positional argument. options is the static list;
// dynamic (when set) replaces it and is resolved lazily. after gates the
// position on the preceding token, so "/ui verbosity <low|med|high>" is two
// plain specs rather than a special case. fuzzy allows subsequence matching
// for lists the user cannot be expected to type from the front.
type argSpec struct {
	options []argOption
	dynamic func(*Model) []argOption
	after   []string
	fuzzy   bool
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
func argSpecFor(c slashCommand, pos int, prior []string) (argSpec, bool) {
	if pos < 0 || pos >= len(c.argSpecs) {
		return argSpec{}, false
	}
	spec := c.argSpecs[pos]
	if len(spec.after) > 0 {
		prev := prior[len(prior)-1]
		if !containsString(spec.after, prev) {
			return argSpec{}, false
		}
	}
	return spec, true
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

// modeArgs offers the session's mode cycle plus /mode why.
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
	return append(out, argOption{value: "why", desc: "Explain the last approval decision"})
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
		desc := fmt.Sprintf("%d turns, %s", b.Turns, b.UpdatedAt.Format("Jan 2 15:04"))
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
		out = append(out, argOption{
			value: e.Name,
			desc:  fmt.Sprintf("%d turns, %s", e.Turns, e.UpdatedAt.Format("Jan 2 15:04")),
		})
	}
	return out
}
