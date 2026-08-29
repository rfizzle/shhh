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
)

// ConfigRelPath is where a workspace's quality config lives, relative to the
// workspace root. The file is trusted — the user authors it — and it is the
// only source of command text: the model can request a suite by name but can
// never supply an executable or arguments.
const ConfigRelPath = ".shhh/quality.json"

// DefaultSuite is the suite a run uses when no name is given.
const DefaultSuite = "default"

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
	MaxParallel int              `json:"max_parallel"`
	Suites      map[string]Suite `json:"suites"`
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
