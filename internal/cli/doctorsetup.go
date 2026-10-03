package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/migrate"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
	"github.com/rfizzle/shhh/internal/update"
)

// The probes. Each one gathers what it can see and hands it to a reading; the
// reading is where the judgement lives, and it is a pure function so the
// whole report can be tested on a machine that has none of this.

func probeBinary(context.Context, config.Config) doctorFinding {
	path, err := os.Executable()
	if err != nil {
		path = ""
	}
	return doctorBinary(version, runtime.GOOS, runtime.GOARCH, path)
}

// doctorBinary states what is running. It is the one check that cannot fail:
// a report that could not say which binary produced it would be a report
// nobody could act on, so it leads.
func doctorBinary(version, goos, goarch, path string) doctorFinding {
	f := doctorFinding{Subject: "shhh " + version, Detail: goos + "/" + goarch, Outcome: "ok"}
	if path != "" {
		f.Detail += " · " + shortPath(path)
	}
	if version == "dev" || version == "" {
		// A dev build is not a problem, but every version-shaped answer below
		// it — the update check especially — is about to say nothing, and the
		// reader should know why before they read those rows.
		f.Subject = "shhh (dev build)"
		f.Detail = goos + "/" + goarch
		if path != "" {
			f.Detail += " · " + shortPath(path)
		}
		f.Outcome = "unversioned"
	}
	return f
}

// probeConfig reads the files again rather than taking the config it was
// handed: this is the one command that runs when the load failed (root.go
// lets it through on ownsConfigError), and the config in hand is then the
// zero value with the reason left behind in the startup path.
func probeConfig(_ context.Context, _ config.Config) doctorFinding {
	paths := config.Paths()
	read := ""
	for _, p := range paths {
		// The same test the load makes: a file that is there but cannot be
		// read is the file the row is about, not a missing one.
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			read = p
			break
		}
	}
	cfg, err := config.LoadFrom(paths...)
	var proj config.Project
	if err == nil {
		// The same layering a session would do, refusal included: the doctor
		// is where a file that stops every command is read, and a checkout's
		// file can stop them the same way the user's can.
		cfg, proj, err = layerProjectConfig(cfg, workingDir())
		if err != nil {
			read = config.ProjectPath(workingDir())
		}
	}
	f := doctorConfig(read, paths, cfg, proj, err)
	if read == "" {
		return f
	}
	// The file the row is about is the one read above: the person's own,
	// or the checkout's where that is the one that stopped the load.
	plan, perr := configInit(read != config.ProjectPath(workingDir()), workingDir())
	if perr != nil {
		return f
	}
	behind, berr := behindOf(plan)
	if berr != nil {
		return f
	}
	return withBehind(f, behind, plan)
}

// withBehind warns on the config row when the file is behind the table —
// keys added since it was written, a key that has moved, a wording with no
// file — and offers to bring it up to date the way the migrate row offers
// its move: asked first, and made by the same code `config init --update`
// runs. The offer is the row's own rather than a migration's, because what
// it updates is the file this row names, and the migrate row is about the
// machine's layout
// (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).
//
// A file whose load was refused keeps its failure; only an offer is added,
// and only where every key it was refused for is one that moved. A file that
// is already warned for something else keeps that warning's outcome and
// consequence, since a credential in the file matters more than a row it
// lacks.
func withBehind(f doctorFinding, b configBehind, plan initPlan) doctorFinding {
	if !b.due() {
		return f
	}
	command := config.UpdateUser
	if plan.project {
		command = config.UpdateProject
	}
	// Offered only where the update would go through: a file holding a key
	// that is neither a setting nor a rename is refused by the update too,
	// and an offer that cannot be honoured is worse than none.
	if _, _, err := config.Updated(plan.settings, plan.project); err == nil {
		f.Action = "update the file"
		f.ActionPrompt = "Update " + shortPath(plan.settings) + " now? Every value it sets is kept."
		if keymapBehind(plan) {
			f.ActionPrompt = "Update " + shortPath(plan.settings) + " and " + shortPath(plan.keymap) +
				" now? Every value and binding they set is kept."
		}
		f.Apply = func() ([]string, error) {
			done, err := plan.update()
			return done.lines(plan), err
		}
	}
	if f.State == components.DoctorFailed {
		return f
	}
	// The counts lead the detail: they are what the row is warning about,
	// and the detail clips from its end.
	f.Detail = joinDetail(b.phrase(), f.Detail)
	if f.State != components.DoctorWarned {
		f.State, f.Outcome = components.DoctorWarned, "behind"
		f.Consequence = behindConsequence(b)
		f.FixLabel = "bring it up to date"
	}
	f.Fix = append(f.Fix, command+"   keeps every value the file sets")
	return f
}

// keymapBehind says the update the config row offers will also write the
// keymap beside the settings: there is one, and it lacks a key the update
// adds. The prompt names the file where it will be written, so a rewritten
// keymap is not news the reader first hears after the fact.
func keymapBehind(plan initPlan) bool {
	if !plan.keymapHeld {
		return false
	}
	b, err := keys.KeymapOutdated(plan.keymap)
	return err == nil && len(b.Missing) > 0
}

// behindConsequence is what leaving the file behind costs: a key that
// arrived since is not in the file to be found, and a key that moved stops
// every command once a session reads the file.
func behindConsequence(b configBehind) string {
	if len(b.Renamed) > 0 {
		return "a moved key stops every command until it is written under its new name"
	}
	return "what arrived since the file was written is not in it to find"
}

// doctorConfig says which file was read and what it set. No file at all is
// not a failure — shhh runs on its defaults — but the row says so plainly
// rather than being left out, because "why is this on" is the question a
// setup check gets asked. A file that would not load is the row's failure,
// worded as the refusal every other command gave, so the doctor is where the
// person who was just refused reads why.
func doctorConfig(read string, paths []string, cfg config.Config, proj config.Project, err error) doctorFinding {
	if err != nil {
		f := doctorFinding{
			Subject: shortPath(read), Detail: err.Error(),
			Outcome: "refused", State: components.DoctorFailed,
			Consequence: "no command starts until the file loads",
			FixLabel:    "fix the file",
			Fix:         []string{"edit " + shortPath(read)},
		}
		// The offer goes on a fix line of its own rather than in the
		// detail, which clips at the column's width: a row that reads
		// `unknown key "behaviour" (did you …` has cut off the one word
		// the reader came for.
		var unknown *config.UnknownKeyError
		if errors.As(err, &unknown) {
			names := make([]string, len(unknown.Keys))
			f.Fix = f.Fix[:0]
			for i, k := range unknown.Keys {
				names[i] = fmt.Sprintf("%q", k.Key)
				switch {
				case k.Renamed != "":
					f.Fix = append(f.Fix, k.Key+" moved to "+k.Renamed+": "+unknown.Update+" moves it")
				case k.Nearest != "":
					f.Fix = append(f.Fix, "rename "+k.Key+" to "+k.Nearest)
				default:
					f.Fix = append(f.Fix, "remove "+k.Key+": no setting reads it")
				}
			}
			noun := "unknown key "
			if len(names) > 1 {
				noun = "unknown keys "
			}
			f.Detail = noun + strings.Join(names, ", ")
		}
		return f
	}
	if read == "" {
		f := doctorFinding{
			Subject: "no config file", Detail: "every setting is on its default",
			Outcome: "defaults", State: components.DoctorSkipped,
		}
		if len(paths) > 0 {
			f.FixLabel = "show where one would go"
			f.Fix = []string{
				"shhh config          edit the settings and write the file",
				"it would be written to " + shortPath(paths[0]),
			}
		}
		return f
	}
	f := doctorFinding{
		Subject: shortPath(read),
		Detail:  countOf(configSettingsSet(cfg), "setting set", "settings set"),
		Outcome: "ok",
	}
	return withLiteralKeyWarning(withProjectFile(f, proj), cfg)
}

// withProjectFile names the checkout's own settings file on the row and the
// keys it decided. Both files are named because the value in force can come
// from either, and a row naming one of two files is a row that sends the
// reader to edit the wrong one; the keys go on the fix lines rather than in
// the detail, which clips at its column's width.
func withProjectFile(f doctorFinding, proj config.Project) doctorFinding {
	if !proj.Loaded() {
		return f
	}
	f.Detail = joinDetail(f.Detail, proj.Display+" sets "+countOf(len(proj.Keys), "key", "keys"))
	if len(proj.Keys) == 0 {
		return f
	}
	// One line naming the file and its keys, so it still says whose keys
	// these are under whatever label the credential warning below may put on
	// the block.
	f.Fix = append(f.Fix, proj.Display+" sets "+strings.Join(proj.Keys, ", "))
	if f.FixLabel == "" {
		f.FixLabel = "show what this checkout sets"
	}
	return f
}

// withLiteralKeyWarning turns the config row into a warning when the file
// holds a credential rather than the name of one. It says what that costs
// rather than that the key is deprecated: a person who reads "api_key is
// deprecated" goes looking for the replacement, and a person who reads that
// the file is a copy of their key already knows why it matters and what a
// backup of it is.
//
// It is a warning and not a failure. The key works, the session starts, and
// the fix is two commands the reader chooses when to run — refusing to start
// over a file that has been fine for a year would be shhh deciding a security
// posture on someone's behalf.
// See docs/capabilities/secrets.md#where-a-value-comes-from.
func withLiteralKeyWarning(f doctorFinding, cfg config.Config) doctorFinding {
	held := literalKeys(cfg)
	if len(held) == 0 {
		return f
	}
	f.Outcome = "key in the file"
	f.State = components.DoctorWarned
	f.Detail = joinDetail(f.Detail, heldPhrase(held))
	f.Consequence = "this file is a copy of your key — so is every backup and every clone of it"
	f.FixLabel = "name the variable instead"
	for _, k := range held {
		f.Fix = append(f.Fix,
			"export "+k.envVar+"=… in your shell profile",
			"shhh config set --global "+k.envKey+" "+k.envVar,
			"then remove "+k.key+" from the file",
		)
	}
	return f
}

// literalKey is one credential the file holds as a value: the key holding it,
// the key that would name a variable instead, and the variable to name. The
// provider's own variable is the suggestion where the resolved provider has
// one, because a person exporting a key for anthropic already has somewhere
// the dialect will look for it.
type literalKey struct {
	key    string
	envKey string
	envVar string
}

// heldPhrase names the keys holding a value, agreeing with how many there
// are. A row reading `provider.api_key hold the key itself` is a row the
// reader stops trusting about the rest of the sentence.
func heldPhrase(held []literalKey) string {
	names := make([]string, len(held))
	for i, k := range held {
		names[i] = k.key
	}
	if len(names) == 1 {
		return names[0] + " holds the key itself"
	}
	return strings.Join(names, " and ") + " hold the key itself"
}

// literalKeys are the credentials this file holds as values rather than as
// names. It walks the settings table rather than naming the two keys, so a
// credential that gains the second spelling is warned about by existing
// rather than by being remembered here.
func literalKeys(cfg config.Config) []literalKey {
	var held []literalKey
	for _, s := range config.Settings() {
		envKey := s.EnvKey()
		if envKey == "" {
			continue
		}
		if value, set := config.Value(cfg, s.Key); !set || value == "" {
			continue
		}
		held = append(held, literalKey{key: s.Key, envKey: envKey, envVar: suggestedKeyVar(s.Key, cfg)})
	}
	return held
}

// suggestedKeyVar is the variable the fix names: for the provider key, the
// one the resolved provider's dialect already reads, and otherwise a variable
// spelled from the key, which is the shape every other credential in the
// documentation uses.
func suggestedKeyVar(key string, cfg config.Config) string {
	if key == "provider.api_key" {
		vars := resolve.KeyVars(resolve.Resolve(resolve.Opts{ConfigProvider: cfg.Provider.Default}).Provider)
		return vars[len(vars)-1]
	}
	_, tail, _ := strings.Cut(key, ".")
	return "SHHH_" + strings.ToUpper(tail)
}

// configSettingsSet counts the settings standing against the defaults. It is
// the header count `shhh config` states, read here so the two screens agree
// on what "set" means: a value the file supplied, not a value shhh chose.
func configSettingsSet(cfg config.Config) int {
	n := 0
	for _, set := range []bool{
		cfg.Provider.Default != "", cfg.Provider.Model != "", cfg.Provider.APIKey != "",
		cfg.Provider.APIKeyEnv != "", cfg.Web.SearchAPIKeyEnv != "",
		cfg.Provider.BaseURL != "", cfg.Provider.Name != "",
		cfg.Behavior.SilentMode, cfg.Behavior.Shell != "", cfg.Behavior.ContextMaxTokens > 0,
		cfg.Behavior.MaxToolRounds != 0, cfg.Behavior.SafetyWarnings != nil,
		cfg.Appearance.Mouse != nil, cfg.Appearance.Notify != nil,
		cfg.Behavior.SystemPromptExtra != "", len(cfg.Behavior.CommandAllowlist) > 0,
		len(cfg.Behavior.ReadOnlyCommands) > 0,
		cfg.Sandbox.Profile != "", cfg.Sandbox.ContainerImage != "", cfg.Sandbox.ContainerEngine != "",
		cfg.Sandbox.RequireIsolation != "", len(cfg.Sandbox.DenyExtra) > 0, len(cfg.Sandbox.WriteExtra) > 0,
		cfg.Web.SearchAPIKey != "", cfg.Web.AllowPrivate, cfg.LSP.Disabled,
		cfg.Summary.Model != "", cfg.Summary.Disabled,
	} {
		if set {
			n++
		}
	}
	return n
}

func probeMigrate(context.Context, config.Config) doctorFinding {
	return doctorMigrate(migrate.Plan(migrationDir()))
}

// migrationDir is the checkout the project migrations are asked about. A
// working directory that cannot be read is named as nothing rather than as
// ".": a detector that walked up from the process would answer about a
// checkout the reader was never told it looked at.
func migrationDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// doctorMigrate reads whether this machine is still shaped the way an older
// shhh shaped it. It is the one check that can change something, and it is
// here rather than in a `shhh migrate` command on purpose: a migration nobody
// knows they need is a migration nobody runs, and the place a person already
// goes when something is not where they left it is the doctor
// (docs/capabilities/configuration.md#a-migration-is-a-doctor-check).
//
// It reads as a warning and never as a failure. Nothing is broken — shhh
// starts, runs, and records — it is just doing so without whatever is in the
// old place, and the consequence line is where that is said plainly.
func doctorMigrate(pending []migrate.Pending) doctorFinding {
	if len(pending) == 0 {
		return doctorFinding{
			Subject: "nothing to migrate", Detail: "this machine is on the current layout",
			Outcome: "ok",
		}
	}
	f := doctorFinding{
		Subject:     countOf(len(pending), "migration pending", "migrations pending"),
		Detail:      migrateDetail(pending),
		Outcome:     "pending",
		State:       components.DoctorWarned,
		Consequence: migrateConsequence(pending),
		FixLabel:    "show what would change",
		Fix:         migrateFix(pending),
	}
	if auto := migrateAuto(pending); len(auto) > 0 {
		f.Action = "make " + migrateThese(auto)
		f.ActionPrompt = "Make " + migrateThese(auto) + " now?"
		f.Apply = func() ([]string, error) { return applyMigrations(auto) }
	}
	return f
}

// migrateAuto is the pending migrations shhh can carry out itself. One it
// cannot is still reported — the fix lines say what to do — but it offers no
// key, because an offer that cannot be honoured is worse than none
// (invariant 5).
func migrateAuto(pending []migrate.Pending) []migrate.Pending {
	var auto []migrate.Pending
	for _, p := range pending {
		if p.Auto() {
			auto = append(auto, p)
		}
	}
	return auto
}

// migrateThese names what `[a]` would do, in the plural the count calls for.
func migrateThese(auto []migrate.Pending) string {
	if len(auto) == 1 {
		return "the change"
	}
	return countOf(len(auto), "change", "changes")
}

// migrateDetail is the target field: what each pending migration is about.
func migrateDetail(pending []migrate.Pending) string {
	summaries := make([]string, 0, len(pending))
	for _, p := range pending {
		summaries = append(summaries, p.Summary)
	}
	return strings.Join(summaries, " · ")
}

// migrateConsequence is what leaving them costs. The migrations write their
// own, because only the migration knows what the reader is missing.
func migrateConsequence(pending []migrate.Pending) string {
	lines := make([]string, 0, len(pending))
	for _, p := range pending {
		lines = append(lines, p.Consequence)
	}
	return strings.Join(lines, "; ")
}

// migrateFix is the lines behind `[f]`: every migration named, then its steps
// under it. A migration shhh will not make itself says so on its own line,
// because otherwise the reader would sit waiting for a key that never comes.
func migrateFix(pending []migrate.Pending) []string {
	var lines []string
	for i, p := range pending {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, p.Name+":")
		lines = append(lines, p.Steps...)
		if !p.Auto() {
			lines = append(lines, "shhh cannot make this one for you")
		}
	}
	return lines
}

// applyMigrations carries out every automatic migration and reports what
// changed, one line each. It stops at the first failure and keeps the lines
// from before it: what already moved is what the reader needs to know before
// they try again.
func applyMigrations(auto []migrate.Pending) ([]string, error) {
	var done []string
	for _, p := range auto {
		lines, err := p.Apply()
		done = append(done, lines...)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// probeKeymap reads what the top of this process made of the keymap file
// (cmd/shhh/main.go) rather than reading the file again: the doctor draws
// its screen from the register while it probes, and applying a file under
// it would be a screen offering keys for as long as the check took.
func probeKeymap(context.Context, config.Config) doctorFinding {
	path, err := keys.Applied()
	moved := 0
	for _, g := range keys.Keyboard() {
		for _, a := range g.Acts {
			if a.Moved() {
				moved++
			}
		}
	}
	behind := 0
	if path != "" && err == nil {
		// A reading that fails leaves the row as it was: the file loaded,
		// and whether it lists every key is not worth a warning of its own.
		if b, berr := keys.KeymapOutdated(path); berr == nil {
			behind = b.KeysBehind()
		}
	}
	return doctorKeymap(path, moved, behind, keys.Dead(), err)
}

// doctorKeymap is the keymap row. A refused file is said once on stderr as
// the process starts and the keyboard shhh ships runs instead, so this row is
// the place somebody whose keys went back to the defaults finds out why:
// it names the file and quotes the refusal
// (docs/capabilities/configuration.md#the-keymap-file).
//
// behind is how many keys arrived since a file listing every key was
// written, counted the way the config row counts settings: a warning with
// the update as its fix, since a key the file has no row for is a key its
// reader will not find to move
// (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).
//
// A behind file offers `[a]` on this row, running the keymap's half of the
// update and nothing else. The config row's own offer writes the keymap too,
// but it is made only where the settings are behind: a keymap behind on its
// own would otherwise have its warning on one row and the only key that
// answers it on another row that reads ok, since the screen offers `[a]` for
// the row under the pointer.
//
// dead is the file's lines that name a key shhh has since given up. The
// file was read without them rather than refused, so this row is where
// somebody tidying it learns which lines do nothing now
// (docs/capabilities/configuration.md#the-keymap-file).
func doctorKeymap(path string, moved, behind int, dead []string, err error) doctorFinding {
	switch {
	case err != nil:
		return doctorFinding{
			Subject: shortPath(path), Outcome: "refused", State: components.DoctorWarned,
			Consequence: "the keyboard shhh ships runs instead of this file",
			FixLabel:    "fix the file",
			Fix: []string{
				strings.TrimPrefix(err.Error(), path+": "),
				"shhh keys check reads it again the way a session will",
			},
		}
	case path == "":
		return doctorFinding{Subject: "no keybindings.toml", Detail: "the keyboard shhh ships", Outcome: "ok"}
	}
	f := doctorFinding{Subject: shortPath(path), Detail: countOf(moved, "key", "keys") + " moved", Outcome: "ok"}
	if behind > 0 {
		f.Detail = joinDetail("behind by "+countOf(behind, "key", "keys"), f.Detail)
		f.State, f.Outcome = components.DoctorWarned, "behind"
		f.Consequence = "what arrived since the file was written is not in it to find"
		f.FixLabel = "bring it up to date"
		f.Fix = []string{config.UpdateUser + "   keeps every key the file binds"}
		f.Action = "update the file"
		f.ActionPrompt = "Update " + shortPath(path) + " now? Every key it binds is kept."
		f.Apply = func() ([]string, error) {
			n, err := updateKeymap(path)
			if err != nil || n == 0 {
				return nil, err
			}
			return []string{"updated " + shortPath(path) + ": added " + countOf(n, "key", "keys")}, nil
		}
	}
	if len(dead) > 0 {
		f.Detail = joinDetail(countOf(len(dead), "line", "lines")+" doing nothing", f.Detail)
		note := strings.Join(dead, ", ") + " name keys shhh no longer has; those lines do nothing"
		if f.Outcome == "behind" {
			f.Fix = append(f.Fix, note)
		} else {
			f.State, f.Outcome = components.DoctorWarned, "stale"
			f.Consequence = "a line naming a key shhh no longer has is read and does nothing"
			f.FixLabel = "delete the lines"
			f.Fix = []string{note}
		}
	}
	return f
}

func probeUpdate(context.Context, config.Config) doctorFinding {
	return doctorUpdate(version, updateCheck(version))
}

// updateCheck is the release lookup, a variable so the report can be tested
// without the network. It answers the latest released version, or "" for a
// feed that did not answer.
var updateCheck = func(current string) string {
	res := update.Check(current)
	if res == nil {
		return ""
	}
	return res.Latest
}

// doctorUpdate says whether a newer shhh exists. A dev build has no version
// to compare, and an unreachable release feed is not a fault of this machine
// — both are `⊘`, and both say which.
func doctorUpdate(current string, latest string) doctorFinding {
	if current == "dev" || current == "" {
		return doctorFinding{
			Subject: "not checked", Detail: "a dev build has no released version to compare",
			Outcome: "unversioned", State: components.DoctorSkipped,
		}
	}
	if latest == "" {
		return doctorFinding{
			Subject: "no answer from the release feed", Detail: "this says nothing about your install",
			Outcome: "unknown", State: components.DoctorSkipped,
		}
	}
	if latest == current {
		return doctorFinding{Subject: "shhh " + current, Detail: "the latest release", Outcome: "ok"}
	}
	return doctorFinding{
		Subject: "shhh " + latest + " is out", Detail: "this machine is on " + current,
		Outcome: "out of date", State: components.DoctorWarned,
		FixLabel: "show how to upgrade",
		Fix: []string{
			"brew upgrade shhh                       if it came from the tap",
			"go install github.com/rfizzle/shhh/cmd/shhh@latest",
		},
	}
}
