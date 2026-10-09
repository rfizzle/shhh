// Package quality implements the repository quality gate: named
// suites of checks defined in trusted config, run read-only and contained
// when a mechanism is available, with every result fingerprinted against the
// git tree so a verdict can never silently vouch for code it did not see.
package quality

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfigRelPath is where a workspace's quality config lives, relative to the
// workspace root. The file is the only source of command text: the model can
// request a suite by name but can never supply an executable or arguments.
//
// "Trusted" is a fact about the checkout and not about the file. A clone's
// suites are command lines that run without an approval, so the caller does
// not build a runner at all until the person has answered for the checkout
// (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
const ConfigRelPath = ".shhh/quality.json"

// DefaultSuite is the suite a run uses when no name is given.
const DefaultSuite = "default"

// DefaultCloseRetries is how many times a failing on-close verdict is handed
// back to the model when the config names no number. One is chosen rather
// than derived: the first hand-back is the whole mechanism — the run was
// about to end believing itself finished, and now it knows otherwise — while
// a second and a third are a model that cannot fix this failure being
// charged for a suite run each to discover that again.
const DefaultCloseRetries = 1

// Check is one command a suite runs: a resolved executable and its argv,
// exactly as written. There is no shell and no parsing — the argv is spawned
// as-is.
type Check struct {
	Name string   `json:"name"`
	Exe  string   `json:"exe"`
	Args []string `json:"args"`
	// Scope, when "packages", has the runner fill PackagesPlaceholder in
	// Args with the Go packages of the changed files, and run the check
	// before the unscoped ones. Empty is a check over whatever its argv
	// says, as it always was.
	// See docs/capabilities/testing.md#how-do-quality-gates-stay-repeatable.
	Scope string `json:"scope,omitempty"`
}

// ScopePackages is the one scope a check may declare: it runs first, over
// the packages the changed files belong to.
const ScopePackages = "packages"

// PackagesPlaceholder is where a scoped check's argv takes its packages. An
// argument that is exactly it becomes one argument per package; an argument
// that holds it among other text takes the packages joined by a space, which
// is the form a make variable reads.
const PackagesPlaceholder = "{packages}"

// scoped reports whether the check runs ahead of the suite over the changed
// packages.
func (c Check) scoped() bool { return c.Scope == ScopePackages }

// validateScope refuses a scope that would run without the packages it names,
// and a placeholder with no scope to fill it: either would hand the tool a
// literal "{packages}" and report whatever it made of that as a verdict.
func (c Check) validateScope() error {
	has := false
	for _, a := range c.Args {
		has = has || strings.Contains(a, PackagesPlaceholder)
	}
	switch {
	case c.Scope != "" && !c.scoped():
		return fmt.Errorf("scope is %q; the only scope is %q", c.Scope, ScopePackages)
	case c.scoped() && !has:
		return fmt.Errorf("scope %q needs an argument holding %s", ScopePackages, PackagesPlaceholder)
	case !c.scoped() && has:
		return fmt.Errorf("%s is filled only for a check with scope %q", PackagesPlaceholder, ScopePackages)
	}
	return nil
}

// Suite is a named set of checks.
type Suite struct {
	Checks []Check `json:"checks"`
	// TimeoutSeconds bounds each check (default 600).
	TimeoutSeconds int `json:"timeout_seconds"`
	// RequireContainment makes an unavailable containment mechanism a blocked
	// verdict. A suite that is intended to certify work from an agent session
	// must not quietly run on the host when the boundary is missing.
	RequireContainment bool `json:"require_containment"`
	// AllowWrite grants the suite's checks write access to the workspace
	// inside containment; the default is a read-only workspace.
	AllowWrite bool `json:"allow_write"`
	// RerunFailed is how many times a check that failed is run again, alone,
	// over an unchanged tree. A pointer because zero is an answer: a suite
	// that wants every failure to stand writes 0, where an absent key takes
	// DefaultRerunFailed. One is the most it may be.
	// See docs/capabilities/testing.md#how-do-quality-gates-stay-repeatable.
	RerunFailed *int `json:"rerun_failed"`
}

// DefaultRerunFailed is how many reruns a failing check earns when the suite
// names no number: one, which is what tells a check that failed under load
// from one that fails.
const DefaultRerunFailed = 1

// Reruns is how many times this suite runs a failed check again.
func (s Suite) Reruns() int {
	if s.RerunFailed == nil {
		return DefaultRerunFailed
	}
	return *s.RerunFailed
}

// Config is the trusted quality-gate configuration.
type Config struct {
	// MaxParallel bounds how many checks run at once (default 1, max 4).
	MaxParallel int `json:"max_parallel"`
	// OnClose names the suite a turn runs as it closes over work it
	// changed, where nobody is watching. Empty leaves the gate to the model
	// and the reader, which is what every workspace without this key has.
	// See docs/capabilities/coding-agent.md#it-can-check-itself.
	OnClose string `json:"on_close"`
	// OnCloseRetries bounds the feedback rounds a failing on-close verdict
	// earns. It is a pointer because zero is an answer here and not a
	// silence: a workspace that wants the verdict reported and the turn
	// ended writes 0, where an absent key takes DefaultCloseRetries.
	OnCloseRetries *int             `json:"on_close_retries"`
	Suites         map[string]Suite `json:"suites"`
	// Generated is the project's generated paths and the command that
	// writes each. A landing regenerates a path listed here rather than
	// merging its bytes, and a path listed nowhere is text like any other.
	// See docs/capabilities/subagents.md#a-writer-starts-from-your-tree.
	Generated []Generator `json:"generated"`
}

// CloseRetries is how many feedback rounds a failing on-close verdict earns
// in this workspace. A negative number reads as none rather than as an
// unbounded loop — a config cannot be why a turn never ends.
func (c Config) CloseRetries() int {
	if c.OnCloseRetries == nil {
		return DefaultCloseRetries
	}
	if *c.OnCloseRetries < 0 {
		return 0
	}
	return *c.OnCloseRetries
}

// LoadConfig reads and validates the workspace's quality config. A missing
// file surfaces as an os.IsNotExist error so callers can distinguish "not set
// up" from "broken".
func LoadConfig(workspace string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(ConfigRelPath)))
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigRelPath, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigRelPath, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if len(c.Suites) == 0 {
		return fmt.Errorf("no suites defined")
	}
	// The name is checked here rather than at the run, because the run is
	// unattended by construction: a typo caught at load reaches whoever
	// edited the file, and the same typo caught at a close is a blocked
	// verdict on a turn nobody is reading. The message names the suite that
	// was asked for and the suites that exist, since one of the two is
	// almost always a misspelling of the other.
	if c.OnClose != "" {
		if _, ok := c.Suites[c.OnClose]; !ok {
			return fmt.Errorf("on_close names suite %q, which is not defined (defined: %s)", c.OnClose, strings.Join(c.SuiteNames(), ", "))
		}
	}
	for i, gen := range c.Generated {
		if err := gen.validate(); err != nil {
			return fmt.Errorf("generated %d: %w", i+1, err)
		}
	}
	for name, suite := range c.Suites {
		if name == "" {
			return fmt.Errorf("suite with an empty name")
		}
		if len(suite.Checks) == 0 {
			return fmt.Errorf("suite %q has no checks", name)
		}
		// More than one rerun is refused rather than clamped: a check given
		// three tries passes when it fails two times in three, and the gate
		// would then be vouching for the odds rather than the code.
		if n := suite.Reruns(); n < 0 || n > 1 {
			return fmt.Errorf("suite %q: rerun_failed is %d; it is 0 (failures stand) or 1 (a failure is run once more)", name, n)
		}
		for i, check := range suite.Checks {
			if check.Name == "" {
				return fmt.Errorf("suite %q: check %d has no name", name, i+1)
			}
			if check.Exe == "" {
				return fmt.Errorf("suite %q: check %q has no exe", name, check.Name)
			}
			if err := check.validateScope(); err != nil {
				return fmt.Errorf("suite %q: check %q: %w", name, check.Name, err)
			}
		}
	}
	return nil
}

// SuiteNames lists the configured suites in stable order.
func (c Config) SuiteNames() []string {
	names := make([]string, 0, len(c.Suites))
	for name := range c.Suites {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Commands is every check of every suite as the command line it runs, the
// executable and its arguments joined — what a session reads as the project's
// own word for which commands are checks, whoever runs them.
func (c Config) Commands() []string {
	var out []string
	for _, name := range c.SuiteNames() {
		for _, check := range c.Suites[name].Checks {
			out = append(out, strings.TrimSpace(strings.Join(append([]string{check.Exe}, check.Args...), " ")))
		}
	}
	return out
}

// effectiveParallel is the run's concurrency ceiling.
func (c Config) effectiveParallel() int {
	switch {
	case c.MaxParallel < 1:
		return 1
	case c.MaxParallel > MaxParallelChecks:
		return MaxParallelChecks
	}
	return c.MaxParallel
}
