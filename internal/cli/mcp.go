package cli

// MCP servers: the catalog a session reads, the connect that turns it into
// tools, and the surfaces that show what happened. `shhh mcp` is the doctor
// screen re-cut over servers — a connect is a check, with the same seven
// fields and the fix on the row that failed — and `/mcp` is the same
// listing as text inside a session. Trusting a project server is the one
// thing either surface changes, and it asks first
// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// mcpDefinitions turns the config file's servers into definitions.
func mcpDefinitions(cfg config.Config) []mcp.Definition {
	names := make([]string, 0, len(cfg.MCP.Servers))
	for name := range cfg.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]mcp.Definition, 0, len(names))
	for _, name := range names {
		s := cfg.MCP.Servers[name]
		d := mcp.Definition{
			Name: name, Scope: mcp.ScopeUser, Source: config.WritePath(),
			Command: s.Command, Args: s.Args, Env: s.Env,
			URL: s.URL, Headers: s.Headers,
			ReadOnly: s.ReadOnly, Disabled: s.Disabled,
		}
		d.Transport = mcp.TransportFor(s.Type, s.Command, s.URL)
		if s.TimeoutSeconds > 0 {
			d.Timeout = time.Duration(s.TimeoutSeconds) * time.Second
		}
		defs = append(defs, d)
	}
	return defs
}

// loadMCPCatalog is every server a session opened here can see.
func loadMCPCatalog(cfg config.Config) *mcp.Catalog {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	var dirs []string
	for _, p := range config.Paths() {
		dirs = append(dirs, filepath.Dir(p))
	}
	return mcp.Discover(cwd, mcpDefinitions(cfg), dirs)
}

// mcpTrust is the trust store over the local database. A nil database
// trusts nothing, which is the safe reading of "cannot tell".
type mcpTrust struct{ db *storage.DB }

func (t mcpTrust) Trusted(root, name string) (string, bool) {
	if t.db == nil {
		return "", false
	}
	return t.db.MCPTrusted(root, name)
}

// mcpRoot is the repository root project servers are trusted under.
func mcpRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return mcp.ProjectRoot(cwd)
}

// mcpOptions are a session's connect options: the trust store, the root,
// and the timeout the config sets.
func mcpOptions(cfg config.Config, db *storage.DB, readOnlyOnly bool) mcp.Options {
	opts := mcp.Options{Root: mcpRoot(), Trust: mcpTrust{db}, ReadOnlyOnly: readOnlyOnly}
	if cfg.MCP.StartupTimeoutSeconds > 0 {
		opts.Timeout = time.Duration(cfg.MCP.StartupTimeoutSeconds) * time.Second
	}
	return opts
}

// openMCP connects a session's servers. Nil when nothing is defined or the
// section is disabled, so the session registers nothing and the prompt
// says nothing — a toolset with no tools is not a thing to describe.
func openMCP(ctx context.Context, cfg config.Config, db *storage.DB, readOnlyOnly bool) (*mcp.Toolset, *mcp.Catalog) {
	if cfg.MCP.Disabled {
		return nil, nil
	}
	cat := loadMCPCatalog(cfg)
	if cat.Len() == 0 && len(cat.Diagnostics) == 0 {
		return nil, nil
	}
	mcp.SetVersion(version)
	return mcp.Connect(ctx, cat, mcpOptions(cfg, db, readOnlyOnly)), cat
}

// mcpStartupNotes are the lines a session prints before it starts: every
// server that did not connect, and why, so a missing tool is never a
// silent one. Nothing for a server that connected — the prompt block and
// /mcp carry those.
func mcpStartupNotes(ts *mcp.Toolset, cat *mcp.Catalog) []string {
	var out []string
	if cat != nil {
		for _, d := range cat.Diagnostics {
			out = append(out, "mcp: "+d)
		}
	}
	if ts == nil {
		return out
	}
	for _, r := range ts.Reports {
		switch r.Status {
		case mcp.StatusConnected, mcp.StatusDisabled, mcp.StatusExcluded:
			continue
		}
		out = append(out, "mcp: "+r.Definition.Name+": "+mcpOutcome(r)+" — "+mcpConsequence(r))
	}
	return out
}

// mcpOutcome is the right-hand word for a report: what became of it.
func mcpOutcome(r mcp.Report) string {
	switch r.Status {
	case mcp.StatusConnected:
		return countOf(len(r.Server.Tools), "tool", "tools")
	case mcp.StatusFailed:
		return "failed"
	case mcp.StatusDisabled:
		return "disabled"
	case mcp.StatusUntrusted:
		return "untrusted"
	case mcp.StatusChanged:
		return "changed"
	case mcp.StatusMissingEnv:
		return "unset: " + strings.Join(r.Missing, ", ")
	case mcp.StatusExcluded:
		return "not read-only"
	}
	return string(r.Status)
}

// mcpConsequence is what a report that is not a connect costs the reader,
// in the words of the surface they will meet it on.
func mcpConsequence(r mcp.Report) string {
	switch r.Status {
	case mcp.StatusFailed:
		return "its tools are not in this session"
	case mcp.StatusDisabled:
		return "its tools are not in any session until it is enabled"
	case mcp.StatusUntrusted:
		return "a project server does not start until you trust it"
	case mcp.StatusChanged:
		return "its definition changed since you trusted it, so it did not start"
	case mcp.StatusMissingEnv:
		return "its tools are not in this session until the variable is set"
	case mcp.StatusExcluded:
		return "a conversation connects only servers marked read-only"
	}
	return ""
}

// mcpFix is what would make a report connect, as the lines a fix key
// reveals.
func mcpFix(r mcp.Report, root string) []string {
	d := r.Definition
	switch r.Status {
	case mcp.StatusFailed:
		lines := []string{"the server's own output is above; check the command or the url", "shhh mcp show " + d.Name + " connects it alone and prints what it says"}
		return lines
	case mcp.StatusDisabled:
		if d.Scope == mcp.ScopeUser && strings.HasSuffix(d.Source, ".toml") {
			return []string{"[mcp.servers." + d.Name + "]", "disabled = false"}
		}
		return []string{"in " + d.Source + ": set \"disabled\": false"}
	case mcp.StatusUntrusted, mcp.StatusChanged:
		return []string{"shhh mcp show " + d.Name + "   # what it is, before you trust it", "shhh mcp trust " + d.Name + "   # or [a] on this row"}
	case mcp.StatusMissingEnv:
		var lines []string
		for _, name := range r.Missing {
			lines = append(lines, "export "+name+"=...")
		}
		return lines
	case mcp.StatusExcluded:
		if d.Scope == mcp.ScopeProject {
			return []string{"read-only is your word, not the checkout's: define the server under another name in your own config with read_only = true"}
		}
		return []string{"[mcp.servers." + d.Name + "]", "read_only = true   # only if nothing it does needs an answer"}
	}
	return nil
}

// mcpDetail is the dim half of the target field: transport target, scope,
// and the read-only mark.
func mcpDetail(d mcp.Definition) string {
	parts := []string{d.Target()}
	if d.Scope == mcp.ScopeProject {
		parts = append(parts, "project")
	}
	if d.ReadOnly {
		parts = append(parts, "read-only")
	}
	return strings.Join(parts, " · ")
}

// mcpListing is the /mcp answer and the text `shhh mcp --table` falls
// back to: every server with what became of it, the tools of the ones that
// connected, then the diagnostics.
func mcpListing(ts *mcp.Toolset, cat *mcp.Catalog, root string) string {
	if ts == nil || len(ts.Reports) == 0 {
		var b strings.Builder
		b.WriteString("No MCP servers defined. Add one under [mcp.servers.<name>] in " + config.WritePath() +
			", or as mcpServers in " + mcp.JSONFileName + " beside it; a project carries its own in .shhh/mcp.json or .mcp.json at the repository root.")
		if cat != nil {
			for _, d := range cat.Diagnostics {
				b.WriteString("\n" + d)
			}
		}
		return b.String()
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, r := range ts.Reports {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mcpGlyph(r.Status), r.Definition.Name, mcpDetail(r.Definition), mcpOutcome(r))
	}
	tw.Flush()
	connected := 0
	for _, r := range ts.Reports {
		if r.Status != mcp.StatusConnected {
			if c := mcpConsequence(r); c != "" {
				fmt.Fprintf(&b, "\n%s: %s", r.Definition.Name, c)
				for _, line := range mcpFix(r, root) {
					fmt.Fprintf(&b, "\n    %s", line)
				}
			}
			if r.Error != "" {
				for _, line := range strings.Split(r.Error, "\n") {
					fmt.Fprintf(&b, "\n    %s", line)
				}
			}
			continue
		}
		connected++
		fmt.Fprintf(&b, "\n%s: %s", r.Definition.Name, countOf(len(r.Server.Tools), "tool", "tools"))
		for _, t := range r.Server.Tools {
			line := "  " + t.Name
			if t.ReadOnlyHint {
				line += "  (says read-only)"
			}
			fmt.Fprintf(&b, "\n%s", line)
		}
	}
	if cat != nil {
		for _, d := range cat.Diagnostics {
			fmt.Fprintf(&b, "\n%s", d)
		}
	}
	fmt.Fprintf(&b, "\n\n%s connected. Tools of a server marked read-only run without asking; every other server's calls ask, like a command.", countOf(connected, "server", "servers"))
	return strings.TrimRight(b.String(), "\n")
}

// mcpGlyph is the state glyph a listing row leads with — doctor's own, so
// the text and the screen say the same thing with the same mark.
func mcpGlyph(s mcp.Status) string {
	return doctorReportGlyph(mcpState(s))
}

func mcpState(s mcp.Status) components.DoctorState {
	switch s {
	case mcp.StatusConnected:
		return components.DoctorPassed
	case mcp.StatusFailed:
		return components.DoctorFailed
	case mcp.StatusDisabled, mcp.StatusExcluded:
		return components.DoctorSkipped
	}
	return components.DoctorWarned
}

// mcpManager backs /mcp in a session: the listing, and trust for a project
// server, which takes effect in the next session — the prompt that names
// the servers was built when this one started
// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
func mcpManager(ts *mcp.Toolset, cat *mcp.Catalog, db *storage.DB) func(args []string) string {
	root := mcpRoot()
	return func(args []string) string {
		if len(args) == 0 {
			return mcpListing(ts, cat, root)
		}
		switch args[0] {
		case "trust", "distrust":
			if len(args) != 2 {
				return "Usage: /mcp " + args[0] + " <name>"
			}
			note, err := mcpSetTrust(db, cat, root, args[1], args[0] == "trust")
			if err != nil {
				return err.Error()
			}
			return note + " It takes effect in the next session."
		}
		return "Usage: /mcp [trust <name>|distrust <name>]"
	}
}

// mcpSetTrust records or withdraws trust for a project server.
func mcpSetTrust(db *storage.DB, cat *mcp.Catalog, root, name string, trust bool) (string, error) {
	if db == nil {
		return "", errors.New("the local store is unavailable, so trust cannot be recorded")
	}
	d, ok := cat.Find(name)
	if !ok {
		return "", fmt.Errorf("no server named %q; `shhh mcp` lists them", name)
	}
	if d.Scope != mcp.ScopeProject {
		return "", fmt.Errorf("%s is your own definition, not a project's; only a project server needs trusting", name)
	}
	if root == "" {
		return "", errors.New("not inside a repository")
	}
	if !trust {
		had, err := db.DistrustMCP(root, name)
		if err != nil {
			return "", err
		}
		if !had {
			return name + " was not trusted.", nil
		}
		return name + " is no longer trusted: it will not start.", nil
	}
	if err := db.TrustMCP(root, name, d.Fingerprint()); err != nil {
		return "", err
	}
	return name + " is trusted at its current definition (" + d.Target() + "); an edit to " + d.Source + " asks again.", nil
}

// mcpProbes is `shhh mcp` as doctor probes: one per server, each a connect
// and a tool listing, then closed — the screen is a reading, not a
// session. The verb field is the transport, which is a closed vocabulary
// of three words and fits the column; the server's name leads the target.
//
// The runner is sequential and a dead server costs its whole timeout, so
// every dial is started when the probes are built and each probe waits for
// its own: five unreachable servers cost one timeout, not five. A re-run
// dials again.
func mcpProbes(ctx context.Context, cat *mcp.Catalog, db *storage.DB, cfg config.Config) []doctorProbe {
	root := mcpRoot()
	dial := func(def mcp.Definition) <-chan mcp.Report {
		ch := make(chan mcp.Report, 1)
		go func() {
			ts := mcp.Connect(ctx, &mcp.Catalog{Servers: []mcp.Definition{def}}, mcpOptions(cfg, db, false))
			ts.Close()
			ch <- ts.Reports[0]
		}()
		return ch
	}
	probes := make([]doctorProbe, 0, cat.Len())
	for _, def := range cat.Servers {
		def := def
		pending := dial(def)
		probes = append(probes, doctorProbe{
			name:   string(def.Transport),
			queued: def.Name + " · " + def.Target(),
			run: func(context.Context, config.Config) doctorFinding {
				if pending == nil {
					pending = dial(def)
				}
				r := <-pending
				pending = nil
				return mcpFinding(r, root, db)
			},
		})
	}
	return probes
}

// mcpFinding is the reading of one report: the row, its consequence, its
// fix, and — for a project server waiting on the person — the offer.
func mcpFinding(r mcp.Report, root string, db *storage.DB) doctorFinding {
	d := r.Definition
	f := doctorFinding{
		Subject: d.Name, Detail: mcpDetail(d),
		Outcome: mcpOutcome(r), State: mcpState(r.Status),
		Consequence: mcpConsequence(r),
	}
	if r.Status == mcp.StatusConnected {
		names := make([]string, 0, len(r.Server.Tools))
		for _, t := range r.Server.Tools {
			names = append(names, t.Remote)
		}
		if len(names) > 0 {
			f.Fix = []string{strings.Join(names, ", ")}
			f.FixLabel = "list the tools"
		}
		if title := r.Server.Info.Name; title != "" && !strings.EqualFold(title, d.Name) {
			f.Detail += " · " + title
			if r.Server.Info.Version != "" {
				f.Detail += " " + r.Server.Info.Version
			}
		}
		return f
	}
	f.Fix = mcpFix(r, root)
	if r.Error != "" {
		f.Fix = append(strings.Split(r.Error, "\n"), f.Fix...)
	}
	if len(f.Fix) > 0 {
		f.FixLabel = fmt.Sprintf("show the %s", countOf(len(f.Fix), "line", "lines"))
	}
	if (r.Status == mcp.StatusUntrusted || r.Status == mcp.StatusChanged) && db != nil && root != "" {
		f.Action = "trust " + d.Name
		f.ActionPrompt = "Trust " + d.Name + " to start from " + d.Source + "? It runs " + d.Target() + " as you."
		f.Apply = func() ([]string, error) {
			note, err := mcpSetTrust(db, &mcp.Catalog{Servers: []mcp.Definition{d}}, root, d.Name, true)
			if err != nil {
				return nil, err
			}
			return []string{note}, nil
		}
	}
	return f
}

func newMCPCmd() *cobra.Command {
	var table bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Connect each MCP server a session here would use, and report it",
		Long: "Read every MCP server definition visible from the current directory — your config file, mcp.json beside it, " +
			"and the project's .shhh/mcp.json or .mcp.json — connect each one, and report it as a row: what it reaches, " +
			"how many tools it offers, and, for one that did not connect, why and what would fix it. " +
			"A project server does not start until you trust it; [a] on its row, or `shhh mcp trust <name>`, does that.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())
			cat := loadMCPCatalog(cfg)
			db, _ := storage.Open()
			if db != nil {
				defer db.Close()
			}
			mcp.SetVersion(version)
			if cat.Len() == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), mcpListing(nil, cat, mcpRoot()))
				return nil
			}
			probes := mcpProbes(cmd.Context(), cat, db, cfg)
			if table || !term.IsTerminal(os.Stdout.Fd()) {
				checks := runDoctorChecks(cmd.Context(), cfg, probes)
				fmt.Fprintln(cmd.OutOrStdout(), doctorReportTitled("shhh mcp", "server", "servers", checks))
				for _, d := range cat.Diagnostics {
					fmt.Fprintln(cmd.OutOrStdout(), d)
				}
				return nil
			}
			return runDoctorScreenTitled(cfg, probes, "shhh mcp", [2]string{"server", "servers"})
		},
	}
	cmd.Flags().BoolVar(&table, "table", false, "print the report as text instead of the surface")

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Connect one server alone and print what it says and offers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())
			cat := loadMCPCatalog(cfg)
			d, ok := cat.Find(args[0])
			if !ok {
				return fmt.Errorf("no server named %q; `shhh mcp` lists them", args[0])
			}
			db, _ := storage.Open()
			if db != nil {
				defer db.Close()
			}
			mcp.SetVersion(version)
			ts := mcp.Connect(cmd.Context(), &mcp.Catalog{Servers: []mcp.Definition{d}}, mcpOptions(cfg, db, false))
			defer ts.Close()
			fmt.Fprintln(cmd.OutOrStdout(), mcpShow(ts.Reports[0], mcpRoot()))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "trust <name>",
		Short: "Let a project server start, at its current definition",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return mcpTrustCmd(cmd, args[0], true) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "distrust <name>",
		Short: "Withdraw that",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return mcpTrustCmd(cmd, args[0], false) },
	})
	cmd.AddCommand(newMCPAddCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a server from your config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFrom(cmd.Context())
			if _, ok := cfg.MCP.Servers[args[0]]; !ok {
				if d, found := loadMCPCatalog(cfg).Find(args[0]); found {
					return fmt.Errorf("%s is defined in %s, not in the config file; edit that file", args[0], d.Source)
				}
				return fmt.Errorf("no server named %q in %s", args[0], config.WritePath())
			}
			delete(cfg.MCP.Servers, args[0])
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s.\n", args[0], config.WritePath())
			return nil
		},
	})
	return cmd
}

func mcpTrustCmd(cmd *cobra.Command, name string, trust bool) error {
	cfg := ConfigFrom(cmd.Context())
	db, err := storage.Open()
	if err != nil {
		return fmt.Errorf("the local store is unavailable, so trust cannot be recorded: %w", err)
	}
	defer db.Close()
	note, err := mcpSetTrust(db, loadMCPCatalog(cfg), mcpRoot(), name, trust)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), note)
	return nil
}

// mcpShow is `shhh mcp show <name>`: the definition as written, secrets by
// name only, then what the server said and every tool with its
// description and its own hints, quoted as hints.
func mcpShow(r mcp.Report, root string) string {
	d := r.Definition
	var b strings.Builder
	fmt.Fprintf(&b, "name:       %s\nscope:      %s\nsource:     %s\ntransport:  %s\ntarget:     %s\n", d.Name, d.Scope, d.Source, d.Transport, d.Target())
	if names := d.SecretNames(); len(names) > 0 {
		fmt.Fprintf(&b, "reads env:  %s\n", strings.Join(names, ", "))
	}
	if len(d.Env) > 0 {
		keys := make([]string, 0, len(d.Env))
		for k := range d.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "sets env:   %s\n", strings.Join(keys, ", "))
	}
	if len(d.Headers) > 0 {
		keys := make([]string, 0, len(d.Headers))
		for k := range d.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "headers:    %s\n", strings.Join(keys, ", "))
	}
	fmt.Fprintf(&b, "read-only:  %v\nstatus:     %s\n", d.ReadOnly, mcpOutcome(r))
	if r.Status != mcp.StatusConnected {
		if c := mcpConsequence(r); c != "" {
			fmt.Fprintf(&b, "\n%s\n", c)
		}
		if r.Error != "" {
			fmt.Fprintf(&b, "%s\n", r.Error)
		}
		for _, line := range mcpFix(r, root) {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	s := r.Server
	if s.Info.Name != "" {
		fmt.Fprintf(&b, "server:     %s %s\n", s.Info.Name, s.Info.Version)
	}
	if s.Protocol != "" {
		fmt.Fprintf(&b, "protocol:   %s\n", s.Protocol)
	}
	if s.Instructions != "" {
		b.WriteString("\ninstructions (sent to the model):\n")
		for _, line := range strings.Split(s.Instructions, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	fmt.Fprintf(&b, "\n%s:\n", countOf(len(s.Tools), "tool", "tools"))
	for _, t := range s.Tools {
		fmt.Fprintf(&b, "  %s", t.Name)
		var hints []string
		if t.ReadOnlyHint {
			hints = append(hints, "says read-only")
		}
		if !t.Destructive {
			hints = append(hints, "says non-destructive")
		}
		if len(hints) > 0 {
			fmt.Fprintf(&b, "  (%s; hints, not grants)", strings.Join(hints, ", "))
		}
		b.WriteString("\n")
		if t.Description != "" {
			fmt.Fprintf(&b, "      %s\n", clipRunes(strings.ReplaceAll(t.Description, "\n", " "), 200))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// newMCPAddCmd writes one server to the config file: `shhh mcp add docs
// --url https://... --header "Authorization=Bearer ${DOCS_TOKEN}"`, or
// `shhh mcp add github -- npx -y mcp-remote https://...`. It writes what
// was said and nothing it inferred: read-only is a flag the person passes.
func newMCPAddCmd() *cobra.Command {
	var (
		url, kind        string
		headers, envs    []string
		readOnly, disabl bool
		timeout          int
	)
	cmd := &cobra.Command{
		Use:   "add <name> [--url <url>] [-- <command> [args...]]",
		Short: "Add a server to your config file",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := mcp.ValidName(name); err != nil {
				return err
			}
			s := config.MCPServer{URL: url, Type: kind, ReadOnly: readOnly, Disabled: disabl, TimeoutSeconds: timeout}
			if len(args) > 1 {
				s.Command, s.Args = args[1], args[2:]
			}
			var err error
			if s.Headers, err = pairs(headers); err != nil {
				return fmt.Errorf("--header: %w", err)
			}
			if s.Env, err = pairs(envs); err != nil {
				return fmt.Errorf("--env: %w", err)
			}
			for _, v := range append(append([]string{url}, headers...), envs...) {
				if looksLikeToken(v) {
					return fmt.Errorf("%q looks like a credential; write it as ${NAME} and export NAME instead, so the value stays out of the file", clipRunes(v, 24))
				}
			}
			cfg := ConfigFrom(cmd.Context())
			probe := config.Config{MCP: config.MCPConfig{Servers: map[string]config.MCPServer{name: s}}}
			if err := mcpDefinitions(probe)[0].Validate(); err != nil {
				return err
			}
			if cfg.MCP.Servers == nil {
				cfg.MCP.Servers = map[string]config.MCPServer{}
			}
			if _, exists := cfg.MCP.Servers[name]; exists {
				return fmt.Errorf("%s is already defined in %s; `shhh mcp remove %s` first", name, config.WritePath(), name)
			}
			cfg.MCP.Servers[name] = s
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s to %s. `shhh mcp` connects it and lists its tools.\n", name, config.WritePath())
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "a remote server's endpoint (streamable HTTP unless --type sse)")
	cmd.Flags().StringVar(&kind, "type", "", "stdio, http or sse (inferred from the command or url)")
	cmd.Flags().StringArrayVar(&headers, "header", nil, "NAME=VALUE sent on every request; reference a token as ${VAR}")
	cmd.Flags().StringArrayVar(&envs, "env", nil, "NAME=VALUE added to a stdio server's environment")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "your statement that nothing this server does needs an answer")
	cmd.Flags().BoolVar(&disabl, "disabled", false, "write it, start nothing")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "startup timeout in seconds for this server")
	return cmd
}

// pairs parses NAME=VALUE flags.
func pairs(kvs []string) (map[string]string, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%q is not NAME=VALUE", kv)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

// looksLikeToken is the guard against writing a credential into the config
// file: a long run of key-shaped characters after a bearer word or a
// key-shaped prefix. It errs toward asking for a reference; a false
// positive costs one retry with ${NAME}.
func looksLikeToken(v string) bool {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "${") {
		return false
	}
	lower := strings.ToLower(v)
	for _, prefix := range []string{"sk-", "sk_", "ghp_", "gho_", "github_pat_", "xoxb-", "xoxp-", "ya29.", "AKIA"} {
		if strings.HasPrefix(v, prefix) || strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if _, rest, ok := strings.Cut(lower, "bearer "); ok {
		return len(strings.TrimSpace(rest)) >= 16
	}
	return false
}

// mcpGatedPreview is the approval card for a server call: where it goes,
// what leaves with it, what comes back — the fetch card's three fields,
// because a server call is the same kind of act.
func mcpGatedPreview(ts *mcp.Toolset, name string, args json.RawMessage) (chat.GatedPreview, error) {
	p, err := ts.Preview(name, args)
	if err != nil {
		return chat.GatedPreview{}, err
	}
	where := chat.GatedField{Label: "server", Value: p.Server, Detail: "a process on this machine: " + p.Target}
	if p.Transport != mcp.TransportStdio {
		where = chat.GatedField{Label: "server", Value: p.Server, Detail: "the request leaves this machine for " + p.Target, Open: true}
	}
	sends := p.Args
	if sends == "" {
		sends = "no arguments"
	}
	receives := "the tool's result, into context"
	if p.ReadOnlyHint {
		receives += " · the server says this tool changes nothing (a hint, not a grant)"
	}
	return chat.GatedPreview{Action: "call", Summary: p.Summary, Fields: []chat.GatedField{
		where,
		{Label: "sends", Value: sends, Detail: "the arguments, as the model wrote them"},
		{Label: "receives", Value: receives, Detail: "it counts against the context window"},
	}}, nil
}

// attachMCP connects the session's servers and puts their tools on the
// toolset and their block in the prompt; it returns what ends them. It is
// one function for the interactive and the headless session because the
// two differ in exactly one word — whether only read-only servers join —
// and a second copy of the rest would drift.
func (s *chatSession) attachMCP(ctx context.Context, db *storage.DB, readOnlyOnly bool) func() {
	s.mcpTools, s.mcpCatalog = openMCP(ctx, ConfigFrom(ctx), db, readOnlyOnly)
	if s.mcpTools == nil {
		return func() {}
	}
	for _, note := range mcpStartupNotes(s.mcpTools, s.mcpCatalog) {
		fmt.Fprintln(os.Stderr, "warning: "+note)
	}
	if s.mcpTools.Len() > 0 {
		s.toolDefs = append(append([]provider.Tool{}, s.toolDefs...), s.mcpTools.Definitions()...)
		s.promptExtra = prompt.CombineExtra(s.promptExtra, mcp.PromptBlock(s.mcpTools))
	}
	return s.mcpTools.Close
}
