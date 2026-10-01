package project

// What a drafted toolchain declaration is made from and written as. The draft
// is a model's reading of the checkout, and three things about it are not the
// model's to decide: the grammar it is told (the same text the documentation
// carries, so the two cannot drift), the bytes it becomes (rendered here, in
// the file's own grammar, so the model writes no TOML), and whether those
// bytes load (ParseToolchain, the loader's own reading).
// See docs/capabilities/containment.md#a-declaration-can-be-drafted-for-you.

import (
	_ "embed"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/receipt/describe"
)

// ToolchainGrammar is the declaration's grammar and rules in prose: the four
// keys, the installers a pin is read from and how each spells one, that an
// unpinned line is refused, and where installed binaries land. It is the text
// the drafter is handed and the text the documentation's section is
// generated from (make docs), so what the model is told about the file and
// what a person reads about it are one text.
//
//go:embed toolchain_grammar.md
var ToolchainGrammar string

// ToolchainDraftToolName is the tool a drafting answers through. Its
// definition is the command line's, which sits above every package that
// reads a call; the name and how a call to it reads are declared here, with
// the rest of what a drafting is made from, so that they can be read.
const ToolchainDraftToolName = "draft_toolchain"

// Describers is how a call to the drafting tool reads, by tool name. It is
// the answer to one request and is drawn on no transcript, so it has no word
// and reads as its own name.
func Describers() map[string]describe.Describer {
	return map[string]describe.Describer{ToolchainDraftToolName: describe.Unworded}
}

// Render writes the declaration in the file's own grammar: the four keys in
// the order the documentation states them, an empty one left out, a list
// that would run past a line broken one entry to a line. Digest is not
// written; it is what reading the bytes back gives.
func (tc Toolchain) Render() []byte {
	var b strings.Builder
	for _, key := range []struct {
		name  string
		items []string
	}{
		{"packages", tc.Packages},
		{"install", tc.Install},
		{"hosts", tc.Hosts},
		{"check", tc.Check},
	} {
		if len(key.items) == 0 {
			continue
		}
		quoted := make([]string, len(key.items))
		for i, item := range key.items {
			quoted[i] = tomlString(item)
		}
		line := key.name + " = [" + strings.Join(quoted, ", ") + "]"
		if len(line) <= 78 {
			b.WriteString(line + "\n")
			continue
		}
		b.WriteString(key.name + " = [\n")
		for _, q := range quoted {
			b.WriteString("  " + q + ",\n")
		}
		b.WriteString("]\n")
	}
	return []byte(b.String())
}

// tomlString is a TOML basic string. The loader refuses a quote or a
// backslash in every entry that could carry one, so escaping them only has
// to keep a bad entry a refusal rather than a file that does not parse.
func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// Declared is the declaration file under root as it stands — its bytes, read
// whether or not the checkout is trusted, and whether there is one. A draft
// reviews it as text; loading it is LoadToolchain's, and that one asks
// about trust. A link or a directory is no declaration, for the reason
// LoadToolchain refuses one. A file that is there and cannot be read as a
// declaration — past the bound, or unreadable — is reported as there with no
// bytes, so nothing mistakes it for a checkout with no file and writes over
// it as a new one.
func Declared(root string) ([]byte, bool) {
	if root == "" {
		return nil, false
	}
	p := filepath.Join(root, filepath.FromSlash(ToolchainFile))
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	if info.Size() > maxToolchainBytes {
		return nil, true
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, true
	}
	return data, true
}

// DraftFile is one file of the checkout a draft is shown, as its path from
// the root and its text, cut at a bound.
type DraftFile struct {
	Path string
	Text string
	// Cut marks a file longer than the bound, whose text is its head.
	Cut bool
}

// DraftEvidence is what a draft is shown of the checkout beyond the survey:
// the files that say what its checks run, read, and the lockfiles, named.
type DraftEvidence struct {
	Files []DraftFile
	// Lockfiles are named and not read: a lockfile is thousands of lines of
	// versions, and the build files beside it already say which tools the
	// checks name.
	Lockfiles []string
}

// Bounds on the evidence. A draft needs the build files and the CI steps,
// which are short; the bound is there so a checkout cannot make the request
// any size, and a file cut at it says so.
const (
	draftFileBytes     = 8 << 10
	draftEvidenceBytes = 64 << 10
	draftWorkflows     = 8
)

// draftFiles are the files that say what a checkout's checks run, in the
// order a reader would look: the build files, the task runners, the linters'
// own configuration, the gate. CI workflows are read after them.
var draftFiles = []string{
	"go.mod", "Cargo.toml", "rust-toolchain.toml", "package.json",
	"pyproject.toml", "requirements.txt", "requirements-dev.txt", "setup.cfg",
	"Makefile", "GNUmakefile", "justfile", "Taskfile.yml",
	".golangci.yml", ".golangci.yaml", ".pre-commit-config.yaml",
	".gitlab-ci.yml", ".shhh/quality.json",
}

// draftLockfiles are the lockfiles a draft is told exist.
var draftLockfiles = []string{
	"go.sum", "Cargo.lock", "package-lock.json", "pnpm-lock.yaml",
	"yarn.lock", "poetry.lock", "uv.lock", "Pipfile.lock",
}

// ReadDraftEvidence reads what a draft is shown of the checkout under root.
// Every file is read only where it is a regular file in the checkout — a
// link is skipped, as the loader refuses one — and the whole is bounded. It
// never fails: a file that cannot be read is a file the draft is not shown.
func ReadDraftEvidence(root string) DraftEvidence {
	var ev DraftEvidence
	if root == "" {
		return ev
	}
	budget := draftEvidenceBytes
	add := func(rel string) {
		if budget <= 0 {
			return
		}
		text, cut, ok := readBounded(filepath.Join(root, filepath.FromSlash(rel)), min(draftFileBytes, budget))
		if !ok {
			return
		}
		budget -= len(text)
		ev.Files = append(ev.Files, DraftFile{Path: rel, Text: text, Cut: cut})
	}
	for _, rel := range draftFiles {
		add(rel)
	}
	for _, rel := range workflows(root) {
		add(rel)
	}
	for _, rel := range draftLockfiles {
		if info, err := os.Lstat(filepath.Join(root, rel)); err == nil && info.Mode().IsRegular() {
			ev.Lockfiles = append(ev.Lockfiles, rel)
		}
	}
	return ev
}

// workflows are the checkout's GitHub Actions workflows, by name, at most
// draftWorkflows of them.
func workflows(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		ext := path.Ext(e.Name())
		if e.Type().IsRegular() && (ext == ".yml" || ext == ".yaml") {
			out = append(out, ".github/workflows/"+e.Name())
		}
	}
	sort.Strings(out)
	if len(out) > draftWorkflows {
		out = out[:draftWorkflows]
	}
	return out
}

// readBounded reads at most limit bytes of a regular file, reporting whether
// it was cut and whether there was anything to read.
func readBounded(p string, limit int) (string, bool, bool) {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", false, false
	}
	f, err := os.Open(p)
	if err != nil {
		return "", false, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return "", false, false
	}
	return string(data), info.Size() > int64(len(data)), true
}
