package cli

// `shhh config init`: the settings file and the wordings, written out.
//
// Two things were only reachable by reading Go. Most of the keys are in no
// surface but the file, so the way to find one was to open the reference and
// copy a key across; and every wording the machinery sends was a string in a
// package, so replacing one meant finding the built-in text, pasting it into
// a file of your own and pointing a key at it. This writes both: every key
// commented out at its default under the sentence that says what it decides,
// and every wording as a file already holding the built-in words, in the
// directory whose files are the override
// (docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).
//
// It is the one command that writes every key at once, and it refuses a file
// that is already there. `--stdout` is what the person with a file already
// gets: the same scaffold with their own values filled in, to read and paste
// rather than have shhh rewrite the file behind them
// (docs/capabilities/configuration.md#a-write-changes-one-line).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/spf13/cobra"
)

func newConfigInitCmd() *cobra.Command {
	var toProject, toStdout bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the settings file and the wordings at their defaults",
		Long: "Write a settings file holding every key, commented out at its default with the sentence " +
			"that says what it decides, and a prompts directory holding every wording the machinery " +
			"sends, each the built-in text ready to edit.\n" +
			"`--project` writes the checkout's own pair instead of yours, with the keys a checkout may " +
			"not decide left out. `--stdout` prints the settings file instead of writing it, with the " +
			"values your file already holds filled in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := configInit(toProject, workingDir())
			if err != nil {
				return err
			}
			if toStdout {
				_, err := io.WriteString(cmd.OutOrStdout(), config.Scaffold(plan.held, toProject))
				return err
			}
			if err := plan.refusal(); err != nil {
				return err
			}
			if err := plan.write(); err != nil {
				return err
			}
			return report.Fprint(cmd.OutOrStdout(), plan.wrote())
		},
	}
	cmd.Flags().BoolVar(&toProject, "project", false,
		"write this checkout's "+project.ConfigFile+" and "+project.PromptsDir+"/ rather than your own")
	cmd.Flags().BoolVar(&toStdout, "stdout", false,
		"print the settings file instead of writing it, with the values your file already holds filled in")
	return cmd
}

// initPlan is everything the command would write and everything already in
// its way, worked out before a byte is written. It is one pass because the
// refusal has to name every file that is there: a command that stopped at
// the first would have the person delete a file, run it again, and be told
// about the next one.
type initPlan struct {
	project bool
	// settings is the file the scaffold goes to and prompts the directory
	// the wordings go under, both stated whether or not they exist.
	settings string
	prompts  string
	files    []initFile
	// held is what the settings file already holds, which is what `--stdout`
	// fills in. It is the zero Config where there is no file, which is a
	// scaffold with every line commented.
	held config.Config
	// settingsHeld says the settings file is already there, and wordingsHeld
	// names the wordings that are. They are named rather than counted
	// because the reader's next act is to look at one of these files and
	// decide whether to keep it; they are named by key rather than by path,
	// so eleven of them are a clause and not a screen of directory.
	settingsHeld bool
	wordingsHeld []string
}

// initFile is one wording to write: the key it is named for, the file, and
// the text it holds. The key rides along because a refusal names the
// wordings in the way it does and reading it back off the path would be a
// second place to decide what a wording is called.
type initFile struct {
	key  string
	path string
	text string
}

// configInit works out that plan. dir is where a checkout is looked for
// from, and is only consulted for a `--project` write.
func configInit(toProject bool, dir string) (initPlan, error) {
	plan := initPlan{project: toProject}
	if toProject {
		if dir == "" {
			return plan, fmt.Errorf("there is no working directory here, so there is no checkout to write")
		}
		root := project.Root(dir)
		plan.settings = config.ProjectPath(dir)
		plan.prompts = filepath.Join(root, filepath.FromSlash(project.PromptsDir))
	} else {
		plan.settings = config.WritePath()
		plan.prompts = userPromptsDir()
	}
	// The wordings are written in the order the settings state them, so the
	// directory listing and the `[prompts]` table read the same way down.
	for _, w := range wordingKeys {
		plan.files = append(plan.files, initFile{w.key, filepath.Join(plan.prompts, w.key+".md"), w.builtin()})
	}
	if _, err := os.Stat(plan.settings); err == nil {
		plan.settingsHeld = true
		held, err := config.LoadFrom(plan.settings)
		if err != nil {
			return plan, err
		}
		plan.held = held
	}
	for _, f := range plan.files {
		if _, err := os.Stat(f.path); err == nil {
			plan.wordingsHeld = append(plan.wordingsHeld, f.key)
		}
	}
	return plan, nil
}

// refusal is the error a plan with something in its way answers with, and
// nil for one with a clear field.
//
// Nothing is written when anything is in the way, rather than the missing
// half being filled in around what is there. A person who ran this expecting
// a file of defaults and got a directory of wordings beside the settings
// they had already tuned would have no way to tell which of the two the
// command decided for them; and the file they have is the one thing here
// that cannot be got back.
func (p initPlan) refusal() error {
	var held []string
	if p.settingsHeld {
		held = append(held, shortPath(p.settings))
	}
	if len(p.wordingsHeld) > 0 {
		held = append(held, fmt.Sprintf("%s under %s%c",
			strings.Join(p.wordingsHeld, ", "), shortPath(p.prompts), filepath.Separator))
	}
	if len(held) == 0 {
		return nil
	}
	scope := ""
	if p.project {
		scope = " --project"
	}
	return fmt.Errorf("nothing was written, to leave what is already here: %s; `shhh config init%s --stdout` prints the settings with your own values filled in, to read and paste",
		strings.Join(held, ", "), scope)
}

// write creates the two directories and the files under them. The settings
// go last: a run that fails part-way leaves no settings file, so running it
// again is the same command rather than one that now refuses itself.
func (p initPlan) write() error {
	mode, dirMode := os.FileMode(0o600), os.FileMode(0o700)
	if p.project {
		// A checkout's files are read by everyone who clones it and are
		// committed as they are, so they take the permissions the rest of
		// the checkout has rather than the private ones a home directory
		// wants.
		mode, dirMode = 0o644, 0o755
	}
	if err := os.MkdirAll(p.prompts, dirMode); err != nil {
		return err
	}
	for _, f := range p.files {
		// Every wording ends in a newline it did not have as a Go string: it
		// is a text file now, and one without a final newline is one every
		// editor and every diff will add one to.
		if err := os.WriteFile(f.path, []byte(f.text+"\n"), mode); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.settings), dirMode); err != nil {
		return err
	}
	return os.WriteFile(p.settings, []byte(config.Scaffold(config.Config{}, p.project)), mode)
}

// wrote is the confirmation: the two things written and what each holds, and
// under them what to do with them. A person who has just been given a
// hundred commented keys and eleven files of prose needs the sentence that
// says which of them is the override.
func (p initPlan) wrote() report.Report {
	settings := report.Done("wrote", shortPath(p.settings))
	settings.Fix = []string{"uncomment a line to change one; above each key is what it decides"}
	settings.Detail = countOf(config.ScaffoldKeys(p.project), "setting", "settings") + ", each commented out"
	wordings := report.Done("wrote", shortPath(p.prompts)+string(filepath.Separator))
	wordings.Detail = countOf(len(p.files), "wording", "wordings") + ", each the built-in text"
	wordings.Fix = []string{
		"edit one and it is what a session is told; delete it and the built-in words are back",
	}
	if p.project {
		// Writing anything the checkout declares changes what the person
		// answered for, so a write made through shhh takes the checkout's
		// own files out of the next session until they answer again — and a
		// confirmation that did not say so would leave them reading a file
		// that is written down and not in force.
		wordings.Fix = append(wordings.Fix, projectTrustNote())
	}
	return report.Report{
		Title:    "shhh config init",
		Subject:  shortPath(filepath.Dir(p.settings)),
		Sections: []report.Section{{Rows: []report.Row{settings, wordings}}},
	}
}
