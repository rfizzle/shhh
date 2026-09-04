// Package mcp connects a session to Model Context Protocol servers and
// turns what they offer into tools the agent can call. shhh speaks the
// protocol and nothing else: a server is a command to spawn or a URL to
// reach, and the authorisation a remote server wants is the business of the
// forwarder the user put in front of it, never of shhh
// (docs/capabilities/mcp.md#shhh-speaks-the-protocol-and-nothing-else).
package mcp

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scope is where a server definition came from.
type Scope string

const (
	// ScopeUser is the person's own configuration: the config file, or the
	// JSON catalog beside it.
	ScopeUser Scope = "user"
	// ScopeProject is a file in the checkout. A project server is not the
	// person's own decision, so it is not started until they have trusted it
	// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
	ScopeProject Scope = "project"
)

// Transport is how a server is reached.
type Transport string

const (
	// TransportStdio spawns a command and speaks JSON-RPC over its pipes.
	TransportStdio Transport = "stdio"
	// TransportHTTP is the streamable HTTP transport, the current remote
	// transport of the specification.
	TransportHTTP Transport = "http"
	// TransportSSE is the older server-sent-events transport some remote
	// servers still serve.
	TransportSSE Transport = "sse"
)

// DefaultStartupTimeout bounds one server's connect and tool listing. A
// stdio server is usually up in under a second and `npx` fetching a package
// for the first time is the slow case; twenty seconds covers a cold cache
// without making a dead server cost the session a minute.
const DefaultStartupTimeout = 20 * time.Second

// DefaultCallTimeout bounds one tool call. Remote tools that take longer
// than this are rare and a session waiting on one has no way to tell it
// from a hang.
const DefaultCallTimeout = 2 * time.Minute

// MaxNameLength caps a server name. Names prefix every tool name the model
// sees, and provider tool names are capped at 64 characters, so a long
// server name would eat the tool's own.
const MaxNameLength = 24

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidName reports whether name may name a server: a lowercase letter,
// then letters, digits and dashes, at most MaxNameLength. The alphabet is
// what every provider accepts at the head of a tool name — Gemini rejects a
// function name that starts with a digit — and what reads as one word in a
// row.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("server name is empty")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("server name %q is longer than %d characters", name, MaxNameLength)
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("server name %q must start with a letter and use only lowercase letters, digits and dashes", name)
	}
	return nil
}

// Definition is one server as the user wrote it.
type Definition struct {
	Name  string
	Scope Scope
	// Source is the file the definition was read from, for diagnostics.
	Source string

	Transport Transport
	// Command and Args are the stdio server's argv. Env is added to the
	// process environment shhh runs it with.
	Command string
	Args    []string
	Env     map[string]string
	// URL and Headers reach a remote server. A bearer token belongs in a
	// header through an environment reference, the way a secret does
	// anywhere else in the config
	// (docs/capabilities/mcp.md#a-value-in-the-file-is-a-value-in-a-backup).
	URL     string
	Headers map[string]string

	// ReadOnly is the user's statement that nothing this server does needs
	// an answer: its tools run the way a file read does. It is the user's
	// word, not the server's — a server's own read-only hints are shown and
	// grant nothing (docs/capabilities/mcp.md#a-server-cannot-vouch-for-itself).
	ReadOnly bool
	// Disabled keeps the definition and starts nothing.
	Disabled bool
	// Timeout bounds connect and tool listing; zero is DefaultStartupTimeout.
	Timeout time.Duration
}

// Validate reports the first thing wrong with a definition, or nil.
func (d Definition) Validate() error {
	if err := ValidName(d.Name); err != nil {
		return err
	}
	switch d.Transport {
	case TransportStdio:
		if d.Command == "" {
			return fmt.Errorf("server %s: a stdio server needs a command", d.Name)
		}
		if d.URL != "" {
			return fmt.Errorf("server %s: a command and a url are two servers, not one", d.Name)
		}
	case TransportHTTP, TransportSSE:
		if d.URL == "" {
			return fmt.Errorf("server %s: a %s server needs a url", d.Name, d.Transport)
		}
		if d.Command != "" {
			return fmt.Errorf("server %s: a command and a url are two servers, not one", d.Name)
		}
		if !strings.HasPrefix(d.URL, "http://") && !strings.HasPrefix(d.URL, "https://") {
			return fmt.Errorf("server %s: url %q must start with http:// or https://", d.Name, d.URL)
		}
	case "":
		return fmt.Errorf("server %s: neither a command nor a url", d.Name)
	default:
		return fmt.Errorf("server %s: unknown transport %q (stdio, http or sse)", d.Name, d.Transport)
	}
	return nil
}

// Target is the one-line description of what the definition reaches: the
// argv of a stdio server, the URL of a remote one.
func (d Definition) Target() string {
	if d.Transport == TransportStdio {
		return strings.Join(append([]string{d.Command}, d.Args...), " ")
	}
	return d.URL
}

var envRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand resolves every `${NAME}` in the definition's args, environment,
// URL and headers from the process environment, and reports the names
// that were not set. A reference to an unset variable is a reason not to
// start the server rather than a value to send empty: a bearer header
// with nothing after the word reads as a bug at the far end, and the
// person finds out a round later, if at all.
func (d Definition) Expand(lookup func(string) (string, bool)) (Definition, []string) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	var missing []string
	seen := map[string]bool{}
	expand := func(s string) string {
		return envRefRE.ReplaceAllStringFunc(s, func(ref string) string {
			name := ref[2 : len(ref)-1]
			v, ok := lookup(name)
			if !ok {
				if !seen[name] {
					seen[name] = true
					missing = append(missing, name)
				}
				return ""
			}
			return v
		})
	}
	out := d
	out.Args = make([]string, len(d.Args))
	for i, a := range d.Args {
		out.Args[i] = expand(a)
	}
	out.Env = expandMap(d.Env, expand)
	out.Headers = expandMap(d.Headers, expand)
	out.URL = expand(d.URL)
	out.Command = expand(d.Command)
	sort.Strings(missing)
	return out, missing
}

func expandMap(m map[string]string, expand func(string) string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = expand(v)
	}
	return out
}

// SecretNames are the environment variables the definition references,
// sorted. They are what a listing can show without showing the values.
func (d Definition) SecretNames() []string {
	seen := map[string]bool{}
	var names []string
	collect := func(s string) {
		for _, m := range envRefRE.FindAllStringSubmatch(s, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				names = append(names, m[1])
			}
		}
	}
	collect(d.Command)
	collect(d.URL)
	for _, a := range d.Args {
		collect(a)
	}
	for _, v := range d.Env {
		collect(v)
	}
	for _, v := range d.Headers {
		collect(v)
	}
	sort.Strings(names)
	return names
}

// StartupTimeout is Timeout or the default.
func (d Definition) StartupTimeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return DefaultStartupTimeout
}
