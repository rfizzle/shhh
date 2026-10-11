// Package calltier is where a tool says which security tier its calls sit at.
// Each package that defines a tool declares the tier beside the definition, the
// way it declares how a call reads (describe.Describer), and the agent's
// classifier reads the one registry built from them rather than switching on
// names of its own. A tool that declares no tier does not register, so a new
// tool cannot be added without one.
//
// A tool that sits at the read tier declares nothing: the read tier is what a
// name the registry does not hold is classified at, unless the surface has an
// answer for it.
//
// It is a leaf: the tool packages declare into it and the agent reads from it,
// and it imports neither.
package calltier

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Tier is the tier a tool's calls sit at. The zero value is no tier, which
// is what a registration is refused for.
type Tier int

const (
	// Write changes the tree, and is answered the way an edit is.
	Write Tier = iota + 1
	// Command runs a program or reaches past the tree, and is answered by
	// its own kind.
	Command
)

// Kind is what the policy is asked about a call to the tool.
type Kind int

const (
	// Edit is answered as a change to a file, or to what a line stands for.
	Edit Kind = iota + 1
	// Run is answered as a command line.
	Run
	// Fetch is answered as a visit to a host.
	Fetch
)

// Contribution is what one call adds to the action the policy is asked
// about, read off the call's own arguments by the package that owns the tool.
type Contribution struct {
	// Path is the file an edit names.
	Path string
	// Line is the command line the call stands for.
	Line string
	// Reading is the tool's reading of the call's host, for a fetch. It is
	// the owning package's type; the classifier that asks for it knows which.
	Reading any
}

// Declaration is one tool's statement of its tier.
type Declaration struct {
	Tier Tier
	Kind Kind
	// Contribute reads a call's arguments into what it adds to the action.
	// Nil is a tool whose calls add nothing.
	Contribute func(name string, args json.RawMessage) Contribution
}

// Read is the contribution of one call to the tool d declares.
func (d Declaration) Read(name string, args json.RawMessage) Contribution {
	if d.Contribute == nil {
		return Contribution{}
	}
	return d.Contribute(name, args)
}

// Registry is the declarations of every tool, by name.
type Registry map[string]Declaration

// New merges the declarations each tool package makes. A tool with no tier or
// no kind, a nameless tool, and a name two packages declare are all errors:
// the registry is the one place a tier is decided, so it is never silently
// partial.
func New(sources ...map[string]Declaration) (Registry, error) {
	r := Registry{}
	var bad []string
	for _, src := range sources {
		for name, d := range src {
			switch {
			case strings.TrimSpace(name) == "":
				bad = append(bad, "a tool with no name")
			case d.Tier != Write && d.Tier != Command:
				bad = append(bad, fmt.Sprintf("%s declares no tier", name))
			case d.Kind < Edit || d.Kind > Fetch:
				bad = append(bad, fmt.Sprintf("%s declares no kind", name))
			default:
				if _, dup := r[name]; dup {
					bad = append(bad, fmt.Sprintf("%s is declared twice", name))
					continue
				}
				r[name] = d
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("calltier: cannot register: %s", strings.Join(bad, "; "))
	}
	return r, nil
}

// Must is New for the package-level registry, which is built at start and
// whose failure is a programming error.
func Must(sources ...map[string]Declaration) Registry {
	r, err := New(sources...)
	if err != nil {
		panic(err)
	}
	return r
}
