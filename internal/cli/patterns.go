package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// What repeats, proposed. The readings in observepatterns.go count what this
// checkout's sessions kept doing; this turns each count into the one thing
// that would stop it, decided here by the code and never by a model
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project):
//
//   - a file read session after session is a memory of kind convention
//     naming what it is for;
//   - a command a person was asked about and allowed every time is a line in
//     the checkout's allowlist;
//   - three commands run in the same order session after session are a
//     skill;
//   - a suite that failed before it passed session after session is a memory
//     of kind lesson.
//
// Nothing here writes. The chat's cards do, on the person's yes.

// patternsWindow is the window the proposals are read over: the reading's own
// default, so /patterns and `shhh observe patterns` agree about what repeats.
const patternsWindow = "30d"

// patternInputs is everything the mapping reads, gathered by the host.
type patternInputs struct {
	Patterns  observePatterns
	Sequences []commandSequence
	// Reads are the transcript lines that read each path.
	Reads map[string][]string
	// Trusted is the person's answer about this checkout. Without it the
	// checkout's settings file and its skills are not read, so a proposal to
	// write either would be a write nobody's session would load
	// (docs/capabilities/skills.md#where-skills-live).
	Trusted bool
	// Memory is whether durable memory is on in this session.
	Memory bool
	// Allowlisted is the checkout's allowlist as its file holds it.
	Allowlisted []string
	// SkillExists reports a skill of that name already in the checkout.
	SkillExists func(name string) bool
	// Remembered reports a memory already naming the subject.
	Remembered func(subject string) bool
	// Declined reports a proposal of that kind and key the person declined.
	Declined func(kind, key string) bool
}

// patternProposals is the mapping: each pattern to one proposal kind, or to
// none where the pattern does not call for one — a command not allowed every
// time it was asked is a question still being answered, not a line to add.
// The order is the tables' own: files, commands, sequences, suites.
func patternProposals(in patternInputs) []chat.Proposal {
	var out []chat.Proposal
	add := func(p chat.Proposal) {
		if in.Declined != nil && in.Declined(p.Kind, p.Key) {
			return
		}
		out = append(out, p)
	}
	remembered := func(subject string) bool { return in.Remembered != nil && in.Remembered(subject) }
	for _, f := range in.Patterns.Files {
		// The tree is not a file, and a memory of "." would name nothing.
		if !in.Memory || f.Path == "." || remembered(f.Path) {
			continue
		}
		add(chat.Proposal{
			Kind: storage.ProposalMemory, Pattern: f.Path, What: "read", Sessions: f.Sessions,
			Key:        "read in session after session: " + f.Path,
			MemoryKind: "convention",
			Text:       f.Path + " is read in session after session in this checkout.",
			Facts: fmt.Sprintf("%s was read, searched or globbed in %d of this checkout's sessions, %d calls in all",
				f.Path, f.Sessions, f.Calls),
			Lines: in.Reads[f.Path],
		})
	}
	for _, c := range in.Patterns.Commands {
		// A proposal that could not be written is not made: it could never
		// reach a card to be answered, so it would be offered for ever. The
		// list is written the way `config set` takes one, where a comma
		// splits an entry in two.
		if !in.Trusted || c.Asked == 0 || c.Allowed != c.Asked || slices.Contains(in.Allowlisted, c.Command) ||
			strings.Contains(c.Command, ",") {
			continue
		}
		add(chat.Proposal{
			Kind: storage.ProposalAllowlist, Pattern: c.Command, Sessions: c.Sessions,
			What: fmt.Sprintf("asked about %s and allowed every time", countOf(c.Asked, "time", "times")),
			Key:  c.Command, Entry: c.Command,
		})
	}
	for _, s := range in.Sequences {
		if !in.Trusted {
			continue
		}
		name := agent.SkillName(strings.Join(s.Keys, " "))
		if name == "" || (in.SkillExists != nil && in.SkillExists(name)) {
			continue
		}
		draft := skill.Draft{Name: name,
			Description: "Use when you would run " + strings.Join(s.Keys, ", then ") + ", in that order.",
			Steps:       slices.Clone(s.Lines)}
		// The same rule as the allowlist's: a command over several lines is
		// not a step the file can hold, so the skill is not proposed.
		if draft.Check() != nil {
			continue
		}
		run := strings.Join(s.Keys, " → ")
		add(chat.Proposal{
			Kind: storage.ProposalSkill, Pattern: run, What: "run in this order", Sessions: s.Sessions,
			Key:   run,
			Skill: draft,
			Facts: fmt.Sprintf("these commands were run in this order in %d of this checkout's sessions: %s",
				s.Sessions, strings.Join(s.Lines, " ; ")),
			Lines: s.Lines,
		})
	}
	for _, s := range in.Patterns.Suites {
		if !in.Memory || remembered(s.Suite) {
			continue
		}
		add(chat.Proposal{
			Kind: storage.ProposalMemory, Pattern: s.Suite, What: "failed before it passed", Sessions: s.Sessions,
			Key:        "failed first in session after session: " + s.Suite,
			MemoryKind: "lesson",
			Text:       "The " + s.Suite + " gate suite fails on its first run in session after session here; run it before calling a change done.",
			Facts: fmt.Sprintf("the gate suite %s failed before it passed in %d of the %d sessions that ran it",
				s.Suite, s.Sessions, s.Ran),
		})
	}
	return out
}

// namesSubject reports whether text names subject as a whole — a path or a
// suite standing on its own, not inside a longer word or path — so a memory
// naming data.go does not count as one naming a.go, and one about linting
// does not count as one about the lint suite.
func namesSubject(text, subject string) bool {
	if subject == "" {
		return false
	}
	part := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-./", r)
	}
	for from := 0; ; {
		i := strings.Index(text[from:], subject)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(subject)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		// A sentence's closing full stop is not part of the name.
		next, _ := utf8.DecodeRuneInString(text[min(end+1, len(text)):])
		trailing := after == '.' && (end+1 == len(text) || unicode.IsSpace(next))
		if (start == 0 || !part(before)) && (end == len(text) || !part(after) || trailing) {
			return true
		}
		from = start + 1
	}
}

// patternsHost is what /patterns reads and writes in one session.
type patternsHost struct {
	db *storage.DB
	// root is the checkout's root, where its settings and skills live;
	// declineRoot is the project key a no is recorded under, the one the
	// memory card's own declines use.
	root, declineRoot string
	// project is the fingerprint the record's sessions are stamped with.
	project string
	trusted bool
	mem     *memory.Store
	writer  *agent.PatternWriter
}

// inputs gathers the mapping's inputs. full reads the conversations too,
// which the sequences and a reading's lines need; the start screen's count
// is taken without them, so a session's open never pays for reading every
// conversation in the window.
func (h patternsHost) inputs(full bool) (patternInputs, error) {
	since, err := parseObserveWindow(patternsWindow)
	if err != nil {
		return patternInputs{}, err
	}
	fp := h.project
	p, err := readObservePatterns(h.db, patternsWindow, since, fp, observePatternsMinSessions)
	if err != nil {
		return patternInputs{}, err
	}
	in := patternInputs{Patterns: p, Trusted: h.trusted, Memory: h.mem != nil,
		Allowlisted: h.allowlisted(),
		SkillExists: func(name string) bool {
			_, err := os.Stat(skill.DraftPath(h.root, name))
			return err == nil
		},
		Declined: func(kind, key string) bool { return h.db.ProposalDeclined(h.declineRoot, kind, key) },
	}
	if h.mem != nil {
		entries, _ := h.mem.List() // a store that will not list remembers nothing, and asks
		in.Remembered = func(subject string) bool {
			return slices.ContainsFunc(entries, func(e memory.Entry) bool { return namesSubject(e.Text, subject) })
		}
	}
	if full {
		t, err := readPatternTranscripts(h.db, since, fp)
		if err != nil {
			return patternInputs{}, err
		}
		in.Reads = t.Reads
		in.Sequences = commandSequences(t.Commands, observePatternsMinSessions)
	}
	return in, nil
}

// allowlisted is the checkout's allowlist as its own file holds it.
func (h patternsHost) allowlisted() []string {
	raw, err := os.ReadFile(config.ProjectPath(h.root))
	if err != nil {
		return nil
	}
	var c config.Config
	if _, err := toml.Decode(string(raw), &c); err != nil {
		return nil
	}
	return c.Behavior.CommandAllowlist
}

// count is how many proposals the start screen offers to show, or zero where
// the record will not read.
func (h patternsHost) count() int {
	in, err := h.inputs(false)
	if err != nil {
		return 0
	}
	return len(patternProposals(in))
}

// chat is the host as the session takes it.
func (h patternsHost) chat() chat.Patterns {
	settings := config.ProjectPath(h.root)
	shown := func(path string) string {
		if rel, err := filepath.Rel(h.root, path); err == nil {
			return rel
		}
		return path
	}
	return chat.Patterns{
		Read: func() ([]chat.Proposal, error) {
			in, err := h.inputs(true)
			if err != nil {
				return nil, err
			}
			return patternProposals(in), nil
		},
		Word: func(ctx context.Context, p chat.Proposal) (chat.Proposal, bool) {
			if !h.writer.Enabled() {
				return p, false
			}
			want := agent.WordMemory
			if p.Kind == storage.ProposalSkill {
				want = agent.WordSkill
			}
			v := h.writer.Word(ctx, agent.PatternRequest{Want: want, Facts: p.Facts, Lines: p.Lines})
			if v.Failed {
				return p, false
			}
			if want == agent.WordMemory {
				// The path or the suite is the code's: a sentence that does
				// not name it is not the proposal the code made, and a
				// memory saved without it would not be recognised as
				// already kept, so the pattern would be offered again.
				if !namesSubject(v.Text, p.Pattern) {
					return p, false
				}
				p.Text = v.Text
				return p, true
			}
			// The words are the model's; the commands are the code's, so a
			// step it reworded is not a step the person saw run.
			worded := p
			worded.Skill.Name, worded.Skill.Description = v.Name, v.Description
			if worded.Skill.Check() != nil {
				return p, false
			}
			return worded, true
		},
		Preview: func(p chat.Proposal) (string, string, string, error) {
			switch p.Kind {
			case storage.ProposalAllowlist:
				e, err := config.ListAdd(settings, "behavior.command_allowlist", p.Entry)
				if err != nil {
					return "", "", "", err
				}
				before, after, err := config.Preview(settings, e)
				return shown(settings), before, after, err
			case storage.ProposalSkill:
				if err := p.Skill.Check(); err != nil {
					return "", "", "", err
				}
				path := skill.DraftPath(h.root, p.Skill.Name)
				if _, err := os.Stat(path); err == nil {
					return "", "", "", fmt.Errorf("a skill named %s is already there", p.Skill.Name)
				}
				return shown(path), "", p.Skill.Render(), nil
			}
			return "", "", "", errors.New("a memory is answered on the memory card")
		},
		Write: func(p chat.Proposal) (string, error) {
			switch p.Kind {
			case storage.ProposalAllowlist:
				e, err := config.ListAdd(settings, "behavior.command_allowlist", p.Entry)
				if err != nil {
					return "", err
				}
				if _, err := writeConfigEdits(config.Project{}, settings, e); err != nil {
					return "", err
				}
				return shown(settings), nil
			case storage.ProposalSkill:
				path, err := skill.Write(h.root, p.Skill)
				return shown(path), err
			}
			return "", errors.New("a memory is answered on the memory card")
		},
		Decline: func(p chat.Proposal) error { return h.db.DeclineProposal(h.declineRoot, p.Kind, p.Key) },
	}
}

// withPatternsCount puts the number of proposals on the start screen's facts,
// read once at session open from the counts alone.
func withPatternsCount(info chat.StartInfo, h patternsHost, ok bool) chat.StartInfo {
	if ok {
		info.Patterns = h.count()
	}
	return info
}

// newPatternsHost is the session's host for /patterns, or false where there
// is no checkout to read the record of.
func newPatternsHost(db *storage.DB, cwd string, mem *memory.Store, writer *agent.PatternWriter) (patternsHost, bool) {
	if db == nil {
		return patternsHost{}, false
	}
	fp := fingerprint(projectFingerprintRoot())
	if fp == "" {
		return patternsHost{}, false
	}
	declineRoot := memory.ProjectScope(cwd)
	if mem != nil {
		declineRoot = mem.Project()
	}
	return patternsHost{db: db, root: project.Root(cwd), declineRoot: declineRoot, project: fp,
		trusted: projectTrust().Allows(), mem: mem, writer: writer}, true
}
