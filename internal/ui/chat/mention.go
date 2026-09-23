package chat

// The @ file mention (docs/interface/surfaces.md#the-input-frame): typing
// `@` at the start of a word opens the completion menu over the same
// files the palette's FILES group offers — what this session changed,
// then what the checkout touched most recently — filtered by what follows
// the @. Choosing a row inserts the path into the sentence and nothing
// more: the model reads files through its tools, so a mention is a name,
// not an attachment. An image is the exception — reading it through a tool
// costs a round to arrive at the same picture, so a mentioned image is
// staged the way a pasted one is.
//
// A conversation's colleagues share the menu
// (docs/capabilities/chat.md#colleagues-not-workers): its read-only roles
// are rows beside the files, each with its description, and choosing one
// writes `@name` into the sentence. That is all naming does — the model
// reads the name as a hint about who should take the work and still
// chooses, so a spawn made under it is an ordinary spawn with an ordinary
// card.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/persona"
	"github.com/rfizzle/shhh/internal/provider"
)

// mentionMatches ranks the file candidates against the text typed after
// the @: exact name or path first, then base-name prefixes, base-name
// substrings, path substrings, and finally path subsequences — so `@mod`
// finds go.mod and model.go before anything that merely spells m-o-d
// somewhere along its directories. The walk behind the candidates runs
// once per open menu (the rule for dynamic sources) and is cached until
// the menu closes.
func (m *Model) mentionMatches(token string) []completionItem {
	if m.complete.mentionCache == nil {
		m.complete.mentionCache = m.paletteFileEntries()
	}
	tok := strings.ToLower(token)
	var exact, basePre, baseSub, pathSub, subseq []completionItem
	place := func(p, base string, item completionItem) {
		switch {
		case tok == "" || p == tok || base == tok:
			exact = append(exact, item)
		case strings.HasPrefix(base, tok):
			basePre = append(basePre, item)
		case strings.Contains(base, tok):
			baseSub = append(baseSub, item)
		case strings.Contains(p, tok):
			pathSub = append(pathSub, item)
		case subsequence(p, tok):
			subseq = append(subseq, item)
		}
	}
	// Colleagues are placed first so each tier lists them ahead of the
	// files: there are a handful of them and a checkout's worth of files.
	for _, r := range m.mentionColleagues() {
		name := strings.ToLower(r.Name)
		place(name, name, completionItem{name: "@" + r.Name, desc: r.Description, space: true, colleague: true})
	}
	for _, e := range m.complete.mentionCache {
		p := strings.ToLower(e.text)
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		place(p, base, completionItem{name: e.text, desc: e.desc, space: true})
	}
	out := make([]completionItem, 0, len(exact)+len(basePre)+len(baseSub)+len(pathSub)+len(subseq))
	out = append(out, exact...)
	out = append(out, basePre...)
	out = append(out, baseSub...)
	out = append(out, pathSub...)
	return append(out, subseq...)
}

// mentionColleagues is the roles a conversation can spawn, read at each
// keystroke rather than cached with the file walk: a role the drafter
// saves mid-session is spawnable at once, and the set is a few names. A
// coding session offers none — its roles are workers the orchestrator
// assigns, not colleagues a person addresses.
func (m *Model) mentionColleagues() []SpawnableRole {
	if m.personas.Kind != persona.KindChat || m.personas.Roles == nil {
		return nil
	}
	return m.personas.Roles()
}

// insertMention writes the focused file row into the draft — the path,
// relative to the working directory, over the @ token — and stages it
// only when it is an image. Both tab and enter land here: a mention menu
// has nothing to run, so the two keys mean the one thing.
func (m Model) insertMention() (tea.Model, tea.Cmd) {
	item := m.complete.items[m.complete.idx]
	m.acceptCompletion()
	m.syncViewport()
	if item.colleague {
		return m, nil
	}
	// The peek reads first bytes only, the way a dragged-in path's does;
	// the read that attaches the image happens in a command.
	path := m.inWorkspace(item.name)
	if kind, err := attachment.PeekKind(path); err == nil && kind == provider.AttachmentImage {
		return m, attachFileCmd(path)
	}
	return m, nil
}
