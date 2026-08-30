package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Catalog is every server definition a session opened in one directory can
// see, user scope and project scope together, and the diagnostics for the
// ones that could not be read. Reading is lenient the way the skills
// catalog is: a definition that cannot load is named here, never a reason
// the session does not start, because a project file arrived with a clone
// and the person opening the session may not have written it
// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
type Catalog struct {
	Servers     []Definition
	Diagnostics []string
}

// Len is how many servers loaded.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Servers)
}

// Find returns the server with the given name.
func (c *Catalog) Find(name string) (Definition, bool) {
	if c == nil {
		return Definition{}, false
	}
	for _, d := range c.Servers {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

// JSONFileName is the cross-harness catalog file: a `mcpServers` object,
// the shape every MCP-speaking client documents and every vendor README
// prints, so a definition pasted from one works here as it is.
const JSONFileName = "mcp.json"

// ProjectFiles are the project-scope catalog files, relative to the
// repository root, in the order they are read. `.mcp.json` at the root is
// the name other harnesses look for; `.shhh/mcp.json` is shhh's own.
var ProjectFiles = []string{
	filepath.Join(".shhh", JSONFileName),
	"." + JSONFileName,
}

// Discover assembles the catalog: user-scope definitions the caller already
// holds (from the config file), the JSON catalog in each user directory, and
// the project files under the repository root of cwd. A project definition
// shadows a user one of the same name, the precedence every other harness
// applies and the one a person means when they copy a shared definition
// into a checkout to change it.
func Discover(cwd string, user []Definition, userDirs []string) *Catalog {
	c := &Catalog{}
	byName := map[string]Definition{}
	order := []string{}
	add := func(d Definition, diag *[]string) {
		if err := d.Validate(); err != nil {
			*diag = append(*diag, fmt.Sprintf("%s: %v", d.Source, err))
			return
		}
		prev, dup := byName[d.Name]
		if !dup {
			order = append(order, d.Name)
		} else if prev.Scope == ScopeUser && d.Scope == ScopeProject {
			// The shadow is the rule, and it is still said: a user who
			// marked their server read-only and finds it excluded needs to
			// know the project's definition is the one in force.
			*diag = append(*diag, fmt.Sprintf("%s: server %s shadows your own definition in %s; rename one to have both", d.Source, d.Name, prev.Source))
		}
		byName[d.Name] = d
	}
	for _, d := range user {
		if d.Scope == "" {
			d.Scope = ScopeUser
		}
		add(d, &c.Diagnostics)
	}
	for _, dir := range userDirs {
		defs, diags := ReadJSON(filepath.Join(dir, JSONFileName), ScopeUser)
		c.Diagnostics = append(c.Diagnostics, diags...)
		for _, d := range defs {
			add(d, &c.Diagnostics)
		}
	}
	if root := ProjectRoot(cwd); root != "" {
		for _, rel := range ProjectFiles {
			defs, diags := ReadJSON(filepath.Join(root, rel), ScopeProject)
			c.Diagnostics = append(c.Diagnostics, diags...)
			for _, d := range defs {
				add(d, &c.Diagnostics)
			}
		}
	}
	for _, name := range order {
		c.Servers = append(c.Servers, byName[name])
	}
	sort.SliceStable(c.Servers, func(i, j int) bool { return c.Servers[i].Name < c.Servers[j].Name })
	return c
}

// ProjectRoot is the repository root above dir, or "" outside one. Only a
// repository carries project servers: walking to the filesystem root would
// read a catalog out of a home directory as if the project had written it.
func ProjectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for probe := abs; ; {
		if _, err := os.Stat(filepath.Join(probe, ".git")); err == nil {
			return probe
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ""
		}
		probe = parent
	}
}

// jsonFile is the cross-harness file shape. Every field is optional and
// unknown fields are ignored, because the files are written for other
// clients as much as for this one.
type jsonFile struct {
	Servers map[string]jsonServer `json:"mcpServers"`
}

type jsonServer struct {
	Type      string            `json:"type"`
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Disabled  bool              `json:"disabled"`
	ReadOnly  bool              `json:"readOnly"`
	ReadOnly2 bool              `json:"read_only"`
	Timeout   int               `json:"timeout"`
}

// ReadJSON reads one catalog file. A missing file is nothing; a file that
// cannot be parsed is one diagnostic; a server that cannot be validated is
// one diagnostic and the rest of the file still loads.
func ReadJSON(path string, scope Scope) ([]Definition, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	var f jsonFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, []string{fmt.Sprintf("%s: not a valid catalog: %v", path, err)}
	}
	var (
		defs  []Definition
		diags []string
	)
	names := make([]string, 0, len(f.Servers))
	for name := range f.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := f.Servers[name]
		d := Definition{
			Name: name, Scope: scope, Source: path,
			Command: s.Command, Args: s.Args, Env: s.Env,
			URL: s.URL, Headers: s.Headers, Disabled: s.Disabled,
		}
		if s.Timeout > 0 {
			d.Timeout = secondsToDuration(s.Timeout)
		}
		d.Transport = TransportFor(firstNonEmpty(s.Type, s.Transport), s.Command, s.URL)
		readOnly := s.ReadOnly || s.ReadOnly2
		switch {
		case readOnly && scope == ScopeProject:
			// A checkout cannot mark its own server read-only: that is the
			// one word that lets its tools run without an answer, and the
			// person is the only one who may say it.
			diags = append(diags, fmt.Sprintf("%s: server %s: read-only is ignored in a project file; set it in your own config", path, name))
		case readOnly:
			d.ReadOnly = true
		}
		if err := d.Validate(); err != nil {
			diags = append(diags, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		defs = append(defs, d)
	}
	return defs, diags
}

// TransportFor resolves the transport from what a definition said and what
// it gave: a command is stdio, a url is streamable HTTP unless it said sse.
// The spellings are the ones other clients' files use, and the config file
// resolves through the same function so the two never disagree.
func TransportFor(kind, command, url string) Transport {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "stdio":
		return TransportStdio
	case "http", "streamable-http", "streamable_http", "streamablehttp":
		return TransportHTTP
	case "sse":
		return TransportSSE
	case "":
		switch {
		case command != "":
			return TransportStdio
		case url != "":
			return TransportHTTP
		}
		return ""
	}
	return Transport(kind)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
