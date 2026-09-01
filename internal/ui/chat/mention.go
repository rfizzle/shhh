package chat

// The @ file mention (docs/interface/surfaces.md#the-input-frame): typing
// `@` at the start of a word opens the completion menu over the same
// files the palette's FILES group offers — what this session changed,
// then what the checkout touched most recently — filtered by what follows
// the @. Choosing a row inserts the path into the sentence and nothing
// more: the model reads files through its tools, so a mention is a name,
// not an attachment. An image is the exception — no tool reads one, so a
// mentioned image is staged the way a pasted one is.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/attachment"
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
	if m.mentionCache == nil {
		m.mentionCache = m.paletteFileEntries()
	}
	tok := strings.ToLower(token)
	var exact, basePre, baseSub, pathSub, subseq []completionItem
	for _, e := range m.mentionCache {
		p := strings.ToLower(e.text)
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		item := completionItem{name: e.text, desc: e.desc, space: true}
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
	out := make([]completionItem, 0, len(exact)+len(basePre)+len(baseSub)+len(pathSub)+len(subseq))
	out = append(out, exact...)
	out = append(out, basePre...)
	out = append(out, baseSub...)
	out = append(out, pathSub...)
	return append(out, subseq...)
}

// insertMention writes the focused file row into the draft — the path,
// relative to the working directory, over the @ token — and stages it
// only when it is an image. Both tab and enter land here: a mention menu
// has nothing to run, so the two keys mean the one thing.
func (m Model) insertMention() (tea.Model, tea.Cmd) {
	item := m.completions[m.completeIdx]
	m.acceptCompletion()
	m.syncViewport()
	// The peek reads first bytes only, the way a dragged-in path's does;
	// the read that attaches the image happens in a command.
	path := m.inWorkspace(item.name)
	if kind, err := attachment.PeekKind(path); err == nil && kind == provider.AttachmentImage {
		return m, attachFileCmd(path)
	}
	return m, nil
}
