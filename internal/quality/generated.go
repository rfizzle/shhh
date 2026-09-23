package quality

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

// Generator is one set of paths the project generates rather than writes,
// and the command that writes them. Paths are repository-relative globs in
// which `**` spans any number of directories; Args may say `{pkg}`, which is
// the Go package a matched path belongs to — the directory above its
// `testdata`, or its own directory where it has none — so one declaration
// covers every package's golden files and each landing runs only the
// packages it touched.
//
// A generated file is regenerated rather than merged because a three-way
// merge of one is wrong even when it comes out clean: two changes to the
// source each rewrite the fixture, and interleaving the two rewrites gives a
// file no generator would write.
// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
type Generator struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
	Exe   string   `json:"exe"`
	Args  []string `json:"args"`
	// TimeoutSeconds bounds the command (default 600).
	TimeoutSeconds int `json:"timeout_seconds"`
}

// pkgPlaceholder is the one substitution a generator's arguments take.
const pkgPlaceholder = "{pkg}"

func (g Generator) validate() error {
	if g.Name == "" {
		return fmt.Errorf("a generator has no name")
	}
	if g.Exe == "" {
		return fmt.Errorf("generator %q has no exe", g.Name)
	}
	if len(g.Paths) == 0 {
		return fmt.Errorf("generator %q names no paths", g.Name)
	}
	for _, p := range g.Paths {
		if p == "" || strings.HasPrefix(p, "/") {
			return fmt.Errorf("generator %q: path %q is not a repository-relative glob", g.Name, p)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "**" {
				continue
			}
			if _, err := path.Match(seg, ""); err != nil {
				return fmt.Errorf("generator %q: path %q: %v", g.Name, p, err)
			}
		}
	}
	return nil
}

// matches reports whether a repository-relative path is one of this
// generator's.
func (g Generator) matches(name string) bool {
	for _, p := range g.Paths {
		if matchGlob(strings.Split(p, "/"), strings.Split(name, "/")) {
			return true
		}
	}
	return false
}

// matchGlob matches path segments against pattern segments, `**` standing for
// any number of whole segments, none included.
func matchGlob(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchGlob(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// pkgOf is `{pkg}` for one path: the directory above its first `testdata`
// segment, or its own directory, spelled as a Go package pattern.
func pkgOf(name string) string {
	dir := path.Dir(name)
	segs := strings.Split(name, "/")
	for i, s := range segs {
		if s == "testdata" {
			dir = strings.Join(segs[:i], "/")
			break
		}
	}
	if dir == "" || dir == "." {
		return "."
	}
	return "./" + dir
}

// generation is one command a set of generated paths asks to be run.
type generation struct {
	gen  Generator
	args []string
}

// generations is what regenerating these paths runs, in declaration order and
// each command once however many of the paths asked for it, beside the paths
// some generator claims. A path two generators claim is the first one's.
func (c Config) generations(paths []string) ([]generation, []string) {
	var out []generation
	var claimed []string
	seen := map[string]bool{}
	for _, p := range paths {
		for _, g := range c.Generated {
			if !g.matches(p) {
				continue
			}
			claimed = append(claimed, p)
			args := make([]string, len(g.Args))
			for i, a := range g.Args {
				args[i] = strings.ReplaceAll(a, pkgPlaceholder, pkgOf(p))
			}
			key := g.Exe + "\x00" + strings.Join(args, "\x00")
			if !seen[key] {
				seen[key] = true
				out = append(out, generation{gen: g, args: args})
			}
			break
		}
	}
	return out, claimed
}

// Generated is the subset of these repository-relative paths the project
// declares generated. A workspace with no config, or one that will not
// parse, declares nothing, so every path merges as text there as it always
// did. Safe on a nil Runner.
func (r *Runner) Generated(paths []string) []string {
	if r == nil {
		return nil
	}
	cfg, err := LoadConfig(r.Workspace)
	if err != nil {
		return nil
	}
	_, claimed := cfg.generations(paths)
	return claimed
}

// Regenerate runs, in dir, every generator the given paths belong to, one at
// a time, contained by WrapIn and kept by the evidence hook the way a check
// is. It answers with the command lines that ran, and stops at the first
// that does not finish: the error names it and carries its output, whose
// whole capture is in the evidence store where one is wired. Paths no
// generator claims run nothing.
func (r *Runner) Regenerate(ctx context.Context, dir string, paths []string) ([]string, error) {
	cfg, err := LoadConfig(r.Workspace)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ConfigRelPath, err)
	}
	gens, _ := cfg.generations(paths)
	var ran []string
	for _, g := range gens {
		check := Check{Name: g.gen.Name, Exe: g.gen.Exe, Args: g.args}
		command := strings.Join(append([]string{check.Exe}, check.Args...), " ")
		exe, err := resolveExe(dir, check.Exe)
		if err != nil {
			return ran, fmt.Errorf("%s did not run: %v", command, err)
		}
		argv := append([]string{exe}, check.Args...)
		if r.WrapIn != nil {
			if argv, err = r.WrapIn(dir, argv); err != nil {
				return ran, fmt.Errorf("%s did not run: containment failed: %v", command, err)
			}
		}
		timeout := DefaultCheckTimeout
		if g.gen.TimeoutSeconds > 0 {
			timeout = time.Duration(g.gen.TimeoutSeconds) * time.Second
		}
		cr := r.runCheck(ctx, dir, "generated", check, argv, timeout)
		if !cr.OK() {
			return ran, generateFailure(cr)
		}
		ran = append(ran, cr.Command)
	}
	return ran, nil
}

// generateFailure says which generator did not finish and why on its first
// line, which is the line a card has room for, and carries its output under
// it.
func generateFailure(cr CheckResult) error {
	why := fmt.Sprintf("exit %d", cr.ExitCode)
	switch {
	case cr.Err != "":
		why = cr.Err
	case cr.TimedOut:
		why = "timed out"
	}
	head := cr.Command + " failed (" + why + ")"
	if cr.EvidenceID != "" {
		head += "; full output " + cr.EvidenceID
	}
	if out := strings.TrimSpace(cr.Output); out != "" {
		return fmt.Errorf("%s\n%s", head, out)
	}
	return fmt.Errorf("%s", head)
}
