package cli

// `shhh config list` and `shhh config get`: what every setting is, and where
// that answer came from. The session already says when a saved model is
// overruled; nothing said what the effective value of any other key was, so
// the only way to read one was to open the file and hope nothing outranked it
// (docs/capabilities/configuration.md#every-setting).

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/spf13/cobra"
)

// configReading is one setting as this machine would answer it: what is in
// force, and which rank it came from. The JSON carries the same figures the
// listing prints, plus what the key takes, so a script can check a value
// against the vocabulary it belongs to.
type configReading struct {
	Key     string   `json:"key"`
	Section string   `json:"section"`
	Takes   string   `json:"takes"`
	Values  []string `json:"values,omitempty"`
	Value   string   `json:"value"`
	Source  string   `json:"source"`
	Set     bool     `json:"set"`
	Default string   `json:"default"`
	Desc    string   `json:"description"`
	// Env and Flag are the ranks above the file, where the key has any. They
	// are what the key would be overruled by rather than what it was: a flag
	// is in force on the command that carries it, and this command carries
	// none.
	Env  string `json:"env,omitempty"`
	Flag string `json:"flag,omitempty"`
	// EnvSet says whether the variable a variable-naming key names is
	// exported on this machine. It is absent rather than false for every key
	// that names none, because a false there would read as a variable that is
	// missing.
	EnvSet *bool `json:"env_set,omitempty"`
}

func newConfigListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list [section]",
		Short: "List every setting with its value and where that came from",
		Long: "List every configuration key, the value in force, and the rank it came from: " +
			"the built-in default, the file, or an environment variable that outranks the file. " +
			"Name a section — provider, behavior, sandbox — to list one table of the file.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			section := ""
			if len(args) == 1 {
				section = strings.ToLower(strings.Trim(args[0], "[]"))
				if !slices.Contains(configSections(), section) {
					return fmt.Errorf("%s", notFound("config section", section,
						"`shhh config list`"))
				}
			}
			readings := configReadings(cfg, section)
			if asJSON {
				return writeJSON(cmd, readings)
			}
			return report.Fprint(cmd.OutOrStdout(),
				configListReport(readings, section, shortPath(config.WritePath())))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the readings as JSON")
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print one setting's value and where that came from",
		Long:  "Print one configuration key: the value in force, the rank it came from, and what the key decides.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			s, ok := config.Lookup(key)
			if !ok {
				return fmt.Errorf("%s", config.UnknownKeyMessage(key))
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			reading := configReadingOf(cfg, s)
			if asJSON {
				return writeJSON(cmd, reading)
			}
			return report.Fprint(cmd.OutOrStdout(), configGetReport(reading))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the reading as JSON")
	return cmd
}

// configSections is every table of the file, in the file's own order.
func configSections() []string {
	var out []string
	for _, s := range config.Settings() {
		if g := s.Group(); !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// configReadings is every setting this config answers, filtered to one
// section when one was named.
func configReadings(cfg config.Config, section string) []configReading {
	entries := configEntries(cfg)
	out := make([]configReading, 0, len(entries))
	for _, s := range entries {
		if section != "" && s.Group() != section {
			continue
		}
		out = append(out, configReadingOf(cfg, s))
	}
	return out
}

// configReadingOf resolves one key the way a run would: the environment
// outranks the file and the file outranks the default.
//
// A flag is not among the answers, and cannot be: a flag is in force on the
// command that carries it, and this command carries none — reporting
// `--model` here would be naming a rank that was not consulted. The
// documentation's own table says which keys a flag outranks, and the listing
// says so on the row.
func configReadingOf(cfg config.Config, s config.Setting) configReading {
	reading := configReading{
		Key: s.Key, Section: s.Group(), Takes: s.Kind.String(), Values: s.Values,
		Default: s.Default, Desc: s.Desc, Env: s.Env, Flag: s.Flag,
	}
	stored, set := config.Value(cfg, s.Key)
	switch env := os.Getenv(s.Env); {
	case s.Env != "" && env != "":
		reading.Value, reading.Source, reading.Set = env, s.Env, true
	case set:
		reading.Value, reading.Source, reading.Set = stored, "file", true
	default:
		reading.Value, reading.Source = s.Default, "default"
	}
	if s.Secret && reading.Set {
		reading.Value = components.MaskSecret(reading.Value)
	}
	// A key that names a variable answers only half the question by itself.
	// The name is what the file decided; whether this machine's shell exports
	// it is what decides whether a session starts, and a listing that printed
	// the name alone would read the same on both machines.
	if s.Kind == config.KindEnvVar && reading.Set {
		set := config.EnvVarSet(reading.Value)
		reading.EnvSet = &set
	}
	return reading
}

// configListReport is the listing: one block per table of the file, in the
// file's order, each row saying what is in force and which rank decided it.
// The path is passed in rather than read here, so the render is a function of
// its arguments and a fixture of it says the same thing on every machine.
func configListReport(readings []configReading, section, path string) report.Report {
	set := 0
	for _, r := range readings {
		if r.Set {
			set++
		}
	}
	title := "shhh config list"
	if section != "" {
		title += " " + section
	}
	r := report.Report{
		Title:   title,
		Subject: fmt.Sprintf("%d settings · %s", len(readings), path),
		Tally:   fmt.Sprintf("%d set · %d at their default", set, len(readings)-set),
	}
	group := ""
	for _, reading := range readings {
		if reading.Section != group {
			group = reading.Section
			r.Sections = append(r.Sections, report.Section{Header: strings.ToUpper(group)})
		}
		last := &r.Sections[len(r.Sections)-1]
		last.Rows = append(last.Rows, configRow(reading))
	}
	if len(readings) == 0 {
		return emptyInto(r, "no settings in this section", "`shhh config list` lists them all")
	}
	return r
}

// configRow is one setting on the listing. The tick is a value somebody
// chose and the dot is the built-in standing, because the question a listing
// of eighty settings is read with is which of them anyone has touched.
func configRow(reading configReading) report.Row {
	state := report.Queue
	if reading.Set {
		state = report.Pass
	}
	_, tail, _ := strings.Cut(reading.Key, ".")
	row := report.Row{
		State: state, Name: tail, Subject: reading.Value, Outcome: reading.Source,
	}
	if reading.EnvSet != nil {
		row.Detail = envVarStateOf(*reading.EnvSet)
	}
	return row
}

// configGetReport is one setting at length: the row the listing would print,
// with what the key decides and the words it takes under it. A reader who
// asked about one key wanted the sentence too, or they would have run the
// listing.
func configGetReport(reading configReading) report.Report {
	row := configRow(reading)
	row.Name = ""
	row.Body = []string{reading.Desc}
	if len(reading.Values) > 0 {
		row.Body = append(row.Body, "One of: "+strings.Join(reading.Values, ", ")+".")
	}
	if reading.Source != "default" {
		row.Body = append(row.Body, "Unset it and "+reading.Default+" stands.")
	}
	if outranks := configOutranks(reading); outranks != "" {
		row.Body = append(row.Body, outranks)
	}
	return report.Report{
		Title:    "shhh config get",
		Subject:  reading.Key,
		Sections: []report.Section{{Rows: []report.Row{row}}},
	}
}

// configOutranks is the line under a key that something can overrule: what
// would win, and where it would have to be. It is left off the key whose
// value already came from the environment, because that rank is the source
// the row has just stated.
func configOutranks(reading configReading) string {
	switch {
	case reading.Source == reading.Env:
		return ""
	case reading.Flag != "" && reading.Env != "":
		return reading.Flag + " on the command line, then " + reading.Env +
			" in the environment, outrank this."
	case reading.Env != "":
		return reading.Env + " in the environment outranks this."
	}
	return ""
}
