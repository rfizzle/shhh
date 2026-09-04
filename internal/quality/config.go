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
// workspace root. The file is trusted — the user authors it — and it is the
// only source of command text: the model can request a suite by name but can
// never supply an executable or arguments.
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
}

// Suite is a named set of checks.
type Suite struct {
	Checks []Check `json:"checks"`
	// TimeoutSeconds bounds each check (default 600).
	TimeoutSeconds int `json:"timeout_seconds"`
	// AllowWrite grants the suite's checks write access to the workspace
	// inside containment; the default is a read-only workspace.
	AllowWrite bool `json:"allow_write"`
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
	for name, suite := range c.Suites {
		if name == "" {
			return fmt.Errorf("suite with an empty name")
		}
		if len(suite.Checks) == 0 {
			return fmt.Errorf("suite %q has no checks", name)
		}
		for i, check := range suite.Checks {
			if check.Name == "" {
				return fmt.Errorf("suite %q: check %d has no name", name, i+1)
			}
			if check.Exe == "" {
				return fmt.Errorf("suite %q: check %q has no exe", name, check.Name)
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
