package cli

// `shhh keys`: the keyboard this process runs, a file checked before a
// session runs on it, and the shipped keyboard written out as a file to start
// from (docs/capabilities/configuration.md#the-keymap-file).
//
// The listing reads what the process start applied rather than applying the
// file again: the register is package data, and the one place a file is read
// is the top of main, where a refusal is said once on stderr. The check is
// the one verb that applies a file here, and it is asked of the keyboard shhh
// ships and puts the register back — this process draws nothing, so nothing
// ever reads the moved keys.

import (
	"fmt"
	"io"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/spf13/cobra"
)

func newKeysCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "List every key, with a mark on the ones your keymap moved",
		Long: "List every key shhh answers, grouped the way a keybindings.toml names them, with a mark on " +
			"each key your file moved and the keystrokes it shipped with beside it. `shhh keys check` " +
			"reads a file the way a session start will; `shhh keys defaults` prints the shipped keyboard " +
			"as a file with every line commented out.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, refused := keys.Applied()
			kb := keys.Keyboard()
			if asJSON {
				return writeJSON(cmd, keysDoc(kb, path, refused))
			}
			return report.Fprint(cmd.OutOrStdout(), keysReport(kb, path, refused))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the keyboard as JSON")
	cmd.AddCommand(newKeysCheckCmd(), newKeysDefaultsCmd())
	return cmd
}

func newKeysCheckCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check [path]",
		Short: "Say whether a session would run a keymap file, without starting one",
		Long: "Read a keybindings.toml the way a session start does — your own, or the file named — and " +
			"print ok or the refusal. It exits non-zero on a refusal.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := config.KeymapPaths()
			if len(args) == 1 {
				paths = args
			}
			path, err := keys.Check(paths...)
			if path == "" && len(args) == 1 {
				// A file named and not there is not "no keymap": the person
				// asked about that file, and ok would be a lie about it.
				return fmt.Errorf("there is no file at %s", args[0])
			}
			if asJSON {
				if jsonErr := writeJSON(cmd, keysCheckDoc(path, err)); jsonErr != nil {
					return jsonErr
				}
				return err
			}
			if err != nil {
				return err
			}
			return report.Fprint(cmd.OutOrStdout(), keysCheckReport(path))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the answer as JSON")
	return cmd
}

func newKeysDefaultsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "defaults",
		Short: "Print the shipped keyboard as a keybindings.toml, every line commented out",
		Long: "Print the file `shhh config init` writes beside your settings: every key at the keystrokes " +
			"it ships with, each line commented out, for the person who already has a keymap and wants " +
			"the names to copy from.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), keys.Scaffold())
			return err
		},
	}
}

// keysReport is the keyboard as a listing: a section per group, a row per
// key. A key a file moved is the one row with a pass mark, carrying the
// keystrokes it shipped with, because that is the row somebody debugging
// their own file is looking for; every other row is the shipped key, dotted
// the way the config listing dots a default.
func keysReport(kb []keys.Group, path string, refused error) report.Report {
	r := report.Report{Title: "shhh keys", Subject: "the keyboard shhh ships"}
	if path != "" && refused == nil {
		r.Subject = shortPath(path)
	}
	total, moved := 0, 0
	for _, g := range kb {
		section := report.Section{Header: strings.ToUpper(g.Name)}
		for _, a := range g.Acts {
			total++
			row := report.Row{
				State:   report.Queue,
				Name:    strings.TrimPrefix(a.Name, g.Name+"."),
				Subject: keysSpelled(a.Keys),
				Detail:  a.Words,
				Outcome: "shipped",
			}
			if !g.Movable {
				row.Outcome = "fixed"
			}
			if a.Moved() {
				moved++
				row.State = report.Pass
				row.Detail += " · shipped as " + keysSpelled(a.Shipped)
				row.Outcome = "moved"
			}
			section.Rows = append(section.Rows, row)
		}
		r.Sections = append(r.Sections, section)
	}
	if refused != nil {
		r.Notes = append(r.Notes, report.Note{State: report.Warn,
			Text: refused.Error() + "; the keyboard shhh ships is running instead"})
	}
	r.Tally = countOf(total, "key", "keys") + " · " + fmt.Sprint(moved) + " moved"
	return r
}

// keysSpelled is keystrokes as a listing prints them. A space is quoted,
// since a bare one is nothing to read.
func keysSpelled(presses []string) string {
	shown := make([]string, len(presses))
	for i, k := range presses {
		if k == " " {
			k = `" "`
		}
		shown[i] = k
	}
	return strings.Join(shown, " ")
}

// keysCheckReport is the answer to a file a session would run.
func keysCheckReport(path string) report.Report {
	r := report.Report{Title: "shhh keys check"}
	if path == "" {
		r.Sections = []report.Section{{Rows: []report.Row{
			report.Empty("no keybindings.toml", "`shhh config init --global` writes one to start from"),
		}}}
		return r
	}
	r.Sections = []report.Section{{Rows: []report.Row{{
		State: report.Pass, Subject: shortPath(path), Detail: "a session would run it", Outcome: "ok",
	}}}}
	return r
}

// keysJSON is the listing for a script: the platform whose keyboard shipped,
// the file this process read, the refusal if there was one, and every key.
type keysJSON struct {
	Platform string          `json:"platform"`
	File     string          `json:"file,omitempty"`
	Refused  string          `json:"refused,omitempty"`
	Groups   []keysGroupJSON `json:"groups"`
}

type keysGroupJSON struct {
	Name    string        `json:"name"`
	Movable bool          `json:"movable"`
	Keys    []keysActJSON `json:"keys"`
}

type keysActJSON struct {
	Name    string   `json:"name"`
	Does    string   `json:"does"`
	Keys    []string `json:"keys"`
	Shipped []string `json:"shipped"`
	Moved   bool     `json:"moved"`
}

func keysDoc(kb []keys.Group, path string, refused error) keysJSON {
	doc := keysJSON{Platform: keys.Platform(), File: path, Groups: []keysGroupJSON{}}
	if refused != nil {
		doc.Refused = refused.Error()
	}
	for _, g := range kb {
		group := keysGroupJSON{Name: g.Name, Movable: g.Movable, Keys: []keysActJSON{}}
		for _, a := range g.Acts {
			group.Keys = append(group.Keys, keysActJSON{
				Name: a.Name, Does: a.Words, Keys: a.Keys, Shipped: a.Shipped, Moved: a.Moved(),
			})
		}
		doc.Groups = append(doc.Groups, group)
	}
	return doc
}

// keysCheckJSON is the check's answer for a script, which also has the exit
// status.
type keysCheckJSON struct {
	File    string `json:"file,omitempty"`
	OK      bool   `json:"ok"`
	Refusal string `json:"refusal,omitempty"`
}

func keysCheckDoc(path string, err error) keysCheckJSON {
	doc := keysCheckJSON{File: path, OK: err == nil}
	if err != nil {
		doc.Refusal = strings.TrimPrefix(err.Error(), path+": ")
	}
	return doc
}
