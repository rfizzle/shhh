// Package memory implements durable cross-session memory
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project):
// short text
// entries in the existing SQLite storage, scoped global or per-project, with
// bounded recall into the system prompt. Its two principles come straight
// from the story: admission control by provenance — an agent-proposed memory
// persists only after explicit user confirmation, because memory an agent
// writes to itself is an injection surface — and cheap to carry: recall is
// keyword-free scope matching under a hard token budget, with zero model
// calls just to maintain memory.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/storage"
)

// GlobalScope is the scope key for memories that apply in every workspace;
// any other scope is a per-project key (the project root path).
const GlobalScope = "global"

// Entry kinds.
const (
	KindPreference = "preference"
	KindConvention = "convention"
	KindCorrection = "correction"
	KindLesson     = "lesson"
)

// Provenance values: who stated the memory.
const (
	ProvenanceUser  = "user"
	ProvenanceAgent = "agent"
)

// MaxTextLen bounds one entry's text: memories are short, durable statements,
// not documents.
const MaxTextLen = 500

// Entry is one stored memory.
type Entry = storage.Memory

// Kinds lists the valid entry kinds.
func Kinds() []string {
	return []string{KindPreference, KindConvention, KindCorrection, KindLesson}
}

// ValidKind reports whether k is a recognized entry kind.
func ValidKind(k string) bool {
	switch k {
	case KindPreference, KindConvention, KindCorrection, KindLesson:
		return true
	}
	return false
}

// ProjectScope derives the per-project scope key for a directory: the
// enclosing repository root (nearest ancestor containing .git), else the
// directory itself, absolute in both cases.
func ProjectScope(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			return probe
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return abs
		}
		probe = parent
	}
}

// Store is the memory store for one session: the shared database plus the
// session's project scope key.
type Store struct {
	db      *storage.DB
	project string
}

func NewStore(db *storage.DB, project string) *Store {
	return &Store{db: db, project: project}
}

// Project is this session's per-project scope key.
func (s *Store) Project() string { return s.project }

// Add validates and persists one entry. Callers own the trust rule: only
// user-confirmed content may arrive here with ProvenanceAgent.
func (s *Store) Add(scope, kind, text, provenance string) (Entry, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return Entry{}, fmt.Errorf("memory text is required")
	case len(text) > MaxTextLen:
		return Entry{}, fmt.Errorf("memory text is too long (%d chars, max %d) — memories are short, durable statements", len(text), MaxTextLen)
	case !ValidKind(kind):
		return Entry{}, fmt.Errorf("unknown memory kind %q (valid: %s)", kind, strings.Join(Kinds(), ", "))
	case provenance != ProvenanceUser && provenance != ProvenanceAgent:
		return Entry{}, fmt.Errorf("unknown provenance %q", provenance)
	case scope == "":
		return Entry{}, fmt.Errorf("memory scope is required")
	}
	return s.db.AddMemory(scope, kind, text, provenance)
}

// List returns every entry visible to this session — the project's plus the
// global ones — most recently updated first.
func (s *Store) List() ([]Entry, error) {
	return s.db.ListMemories(GlobalScope, s.project)
}

// Forget deletes one entry by id.
func (s *Store) Forget(id int64) error {
	return s.db.DeleteMemory(id)
}

// Recall returns the entries to inject into the system prompt: project-scoped
// first (they are the most relevant to this session), then global, newest
// first within each — hard-bounded by maxEntries and by maxTokens of
// estimated prompt cost. No model is ever called.
func (s *Store) Recall(maxEntries int, maxTokens int64) ([]Entry, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	ordered := make([]Entry, 0, len(all))
	for _, e := range all {
		if e.Scope != GlobalScope {
			ordered = append(ordered, e)
		}
	}
	for _, e := range all {
		if e.Scope == GlobalScope {
			ordered = append(ordered, e)
		}
	}

	var picked []Entry
	budget := maxTokens - agent.EstimateTokens(promptBlockHeader)
	for _, e := range ordered {
		if len(picked) >= maxEntries {
			break
		}
		cost := agent.EstimateTokens(EntryLine(e))
		if cost > budget {
			break
		}
		budget -= cost
		picked = append(picked, e)
	}
	return picked, nil
}

// ScopeLabel renders an entry's scope for display: "global" or "project".
func ScopeLabel(scope string) string {
	if scope == GlobalScope {
		return GlobalScope
	}
	return "project"
}

// EntryLine is one entry as it appears in the injected block, citing its id
// so a bad memory can be found and deleted.
func EntryLine(e Entry) string {
	return fmt.Sprintf("- [m%d] (%s %s) %s", e.ID, ScopeLabel(e.Scope), e.Kind, e.Text)
}

const promptBlockHeader = `# Memory
Durable memories from earlier sessions, each cited by id (a wrong or stale one can be removed with /memory forget <id>):`

// PromptBlock renders the injected system-prompt section; empty input renders
// nothing.
func PromptBlock(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, promptBlockHeader)
	for _, e := range entries {
		lines = append(lines, EntryLine(e))
	}
	return strings.Join(lines, "\n")
}

// ParseID parses a user-facing memory id ("m12" or "12").
func ParseID(s string) (int64, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(s), "m")
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid memory id %q (use the [mN] id from /memory list)", s)
	}
	return id, nil
}
