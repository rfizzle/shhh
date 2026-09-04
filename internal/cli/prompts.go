package cli

// Reading the wordings a config file named.
//
// The interruption machinery — the steer, the check-in — the two auxiliary
// readers — the summarizer, the permission classifier — and every stage of a
// backlog run carry built-in prose. A `[prompts]` block replaces any of it
// with a file, so tuning what a session is told costs an edit and a restart
// rather than a build (docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).
//
// Most of it is convention rather than a key. A file at `<key>.md` under a
// prompts directory is that wording: the trusted checkout's own directory
// first, then a `[prompts]` key where one names a path, then the person's
// own directory beside their settings. A file that is there is the
// override, and nothing has to point at it — which is what lets `shhh
// config init` write a directory somebody edits without also editing
// config.toml. The keys stay for the case they were built for: one wording,
// somewhere else.
//
// A checkout gets no key at all, only the convention: a path named in a
// checkout is a path in every clone of it, anywhere on the machine, and a
// wording that travels with the repository has to live in it
// (docs/capabilities/todo.md#the-stage-prompts-are-yours-to-edit).
//
// It is one door, opened where a session is built, because every surface
// that runs the machinery is built through the same env: a chat session, a
// headless run, and every child of either.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// sessionPrompts are the wordings this session runs, as read. An empty field
// is the built-in one, which is what the machinery falls back to on its own —
// so a session that configured nothing carries an empty value here and
// nothing downstream has to know whether a file existed.
type sessionPrompts struct {
	steer      string
	checkIn    string
	summary    string
	classifier string
	// todo is the backlog runner's set, handed to a run whole: the runner is
	// what places the blocks and appends the answer shape around each of
	// them, and a stage that got its wording from anywhere else would be a
	// second place the shape could be decided.
	todo run.Wordings
}

// loadPrompts reads every wording a file named — from the user's settings,
// and from the checkout under projectDir. An empty projectDir is a session
// with no checkout of its own to read, which is where an untrusted one lands
// too.
//
// A file that cannot be read, one that is empty, or one whose placeholders
// are not the ones its wording takes stops the session with the path and the
// reason. Falling back to the built-in text would leave a session running
// shhh's steer while the operator who wrote the path believes it is running
// theirs — and a fortnight of comparison over two cohorts that are in fact
// one. There is no reading of the record that recovers from that, so it fails
// here, where the person who wrote the path is still watching.
func loadPrompts(c config.PromptsConfig, projectDir string) (sessionPrompts, error) {
	var out sessionPrompts
	for _, w := range wordingKeys {
		from := promptSource(w.key, w.named(c), projectDir)
		if from.path == "" {
			continue
		}
		text, err := readWording(from, w.validate)
		if err != nil {
			return sessionPrompts{}, err
		}
		*w.into(&out) = text
	}
	return out, nil
}

// readWording is one file read and judged, with the sentence a reader acts
// on when it will not do.
func readWording(from promptFile, validate func(string) error) (string, error) {
	path, named, undo := from.path, from.named, from.undo
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", named, err)
	}
	text := string(data)
	// An empty file is the same failure as an unreadable one wearing a
	// disguise: every reader downstream takes an empty wording as "not
	// configured", so a truncated write would put the built-in words back
	// and the record would show a session that overrode nothing.
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s: %s: the file is empty; %s to use the built-in wording", named, path, undo)
	}
	if err := validate(text); err != nil {
		return "", fmt.Errorf("%s: %s: %w", named, path, err)
	}
	return text, nil
}

// wording is one key: what it is called in the file and under the checkout's
// prompts directory, where the settings state it, where the session keeps it,
// and which substitutions it takes.
type wording struct {
	key   string
	named func(config.PromptsConfig) string
	into  func(*sessionPrompts) *string
	// builtin is the text this wording carries when nothing replaced it. It
	// is here because two surfaces need it and neither can be told it by the
	// file: the scaffold writes it out to start from, and the fingerprint
	// compares a file against it — a file holding exactly the built-in text
	// asks the model for exactly what the built-in asks, and splitting the
	// record over it would report a change nobody made.
	builtin  func() string
	validate func(string) error
}

// wordingKeys is every wording a file can replace, in the order the settings
// table declares them. It is a list rather than a switch because two surfaces
// walk it — the loader that reads them and the doctor's row that says whether
// they can be read — and a key visible to one and not the other is a key
// whose failure nobody sees until a session will not start.
var wordingKeys = []wording{
	{"steer", func(c config.PromptsConfig) string { return c.Steer },
		func(p *sessionPrompts) *string { return &p.steer },
		agent.SteerWording, agent.ValidateSteer},
	{"check_in", func(c config.PromptsConfig) string { return c.CheckIn },
		func(p *sessionPrompts) *string { return &p.checkIn },
		agent.CheckInWording, agent.ValidateCheckIn},
	{"summary", func(c config.PromptsConfig) string { return c.Summary },
		func(p *sessionPrompts) *string { return &p.summary },
		agent.SummaryWording, agent.ValidateVerbatim},
	{"classifier", func(c config.PromptsConfig) string { return c.Classifier },
		func(p *sessionPrompts) *string { return &p.classifier },
		agent.ClassifierWording, agent.ValidateVerbatim},
	{"todo_standards", func(c config.PromptsConfig) string { return c.TodoStandards },
		func(p *sessionPrompts) *string { return &p.todo.Standards },
		func() string { return builtinStages.Standards }, agent.ValidateVerbatim},
	{"todo_research", func(c config.PromptsConfig) string { return c.TodoResearch },
		func(p *sessionPrompts) *string { return &p.todo.Research },
		func() string { return builtinStages.Research }, agent.ValidateTodoResearch},
	{"todo_implement", func(c config.PromptsConfig) string { return c.TodoImplement },
		func(p *sessionPrompts) *string { return &p.todo.Implement },
		func() string { return builtinStages.Implement }, agent.ValidateTodoImplement},
	{"todo_review", func(c config.PromptsConfig) string { return c.TodoReview },
		func(p *sessionPrompts) *string { return &p.todo.Review },
		func() string { return builtinStages.Review }, agent.ValidateTodoReview},
	{"todo_review_task", func(c config.PromptsConfig) string { return c.TodoReviewTask },
		func(p *sessionPrompts) *string { return &p.todo.ReviewTask },
		func() string { return builtinStages.ReviewTask }, agent.ValidateTodoReview},
	{"todo_remediate", func(c config.PromptsConfig) string { return c.TodoRemediate },
		func(p *sessionPrompts) *string { return &p.todo.Remediate },
		func() string { return builtinStages.Remediate }, agent.ValidateTodoRemediate},
	{"todo_commit", func(c config.PromptsConfig) string { return c.TodoCommit },
		func(p *sessionPrompts) *string { return &p.todo.Commit },
		func() string { return builtinStages.Commit }, agent.ValidateTodoCommit},
}

// builtinStages is the backlog runner's own set, read once. It is a value
// rather than a call per wording because the seven are one set and pairing a
// stage with another stage's text is exactly the mistake a list of eleven
// entries invites.
var builtinStages = run.BuiltinWordings()

// wordingRow is one wording a file replaced, as a surface that reports on
// them finds it: the key, where it was read from, and the reason it will not
// load if it will not.
type wordingRow struct {
	key  string
	from string
	err  error
}

// readWordings reads every wording in force and keeps going past a failure,
// which is what separates a report from a session: a session stops at the
// first one because it cannot run, and a reader fixing their files wants all
// of them named at once.
func readWordings(c config.PromptsConfig, projectDir string) []wordingRow {
	var rows []wordingRow
	for _, w := range wordingKeys {
		from := promptSource(w.key, w.named(c), projectDir)
		if from.path == "" {
			continue
		}
		_, err := readWording(from, w.validate)
		rows = append(rows, wordingRow{key: w.key, from: from.named, err: err})
	}
	return rows
}

// projectWordings is the keys this checkout's own directory supplied, in the
// settings table's order. A session running words the reader did not write
// is one they cannot account for from their own files, so the start screen
// names them the way it names the checkout's settings file.
func projectWordings(c config.PromptsConfig, projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	var out []string
	for _, w := range wordingKeys {
		if promptSource(w.key, w.named(c), projectDir).project {
			out = append(out, w.key)
		}
	}
	return out
}

// promptFile is where one wording is read from: the file itself, the words
// an error about it names it by, and what puts the built-in wording back —
// which differs by where the file was found. A key is removed and a file
// found by convention is deleted, and a reader told to do the other one is
// being sent to edit a file that has nothing to do with it.
type promptFile struct {
	path  string
	named string
	undo  string
	// project marks a wording this checkout handed the session rather than
	// one the person keeps. It is the difference the start screen states.
	project bool
}

// promptSource is the file one wording is read from, most specific first:
// the checkout's own directory, then whatever the settings pointed at, then
// the person's own directory beside their settings. A file that is there is
// the override and nothing has to name it.
//
// The checkout wins for the reason its settings do — what is true of a
// repository travels with the repository — and it cannot take a wording
// away: a checkout with no file for a key leaves the person's answer
// standing, so the directories are read per wording rather than per set.
//
// A key that names a file which is not there is still an answer, and a wrong
// one: it stops the session with the path rather than quietly falling
// through to the directory below, because a person who wrote a path and got
// the wording under it would have no way to see the typo.
func promptSource(key, configured, projectDir string) promptFile {
	if projectDir != "" {
		if p := filepath.Join(projectDir, key+".md"); isPromptFile(p) {
			return promptFile{p, project.PromptsDir + "/" + key + ".md", "delete it", true}
		}
	}
	if configured != "" {
		return promptFile{promptPath(configured), "config prompts." + key, "remove the key", false}
	}
	if p := filepath.Join(userPromptsDir(), key+".md"); isPromptFile(p) {
		return promptFile{p, shortPath(p), "delete it", false}
	}
	return promptFile{}
}

// isPromptFile reports whether a wording is actually there. A directory of
// that name is not a wording, and reading one would fail with an error about
// a directory rather than about a wording nobody wrote.
func isPromptFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// projectPrompts is where this checkout keeps its wordings, and "" where
// there is none this session may read. The root and the answer about it come
// from one reading, so a session cannot load a checkout's wordings under one
// answer and report the other.
func projectPrompts() string {
	t := projectTrust()
	if t.Root == "" || !t.Allows() {
		return ""
	}
	return filepath.Join(t.Root, filepath.FromSlash(project.PromptsDir))
}

// userPromptsDir is where a person's own wordings live: a directory beside
// the settings file, so everything they wrote for shhh sits in one place and
// a wording travels with the settings rather than with whichever directory a
// session was opened in.
func userPromptsDir() string {
	return filepath.Join(filepath.Dir(config.WritePath()), promptsDirName)
}

// promptsDirName is what that directory is called, in both scopes.
const promptsDirName = "prompts"

// promptPath resolves a configured path against the directory the config
// file itself lives in, so a wording kept beside the file travels with it
// instead of depending on which directory a session was opened in.
func promptPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(config.WritePath()), path)
}

// steering is the interruption machinery's tuning as this config left it: the
// numbers from their keys, the wordings from their files, and a zero field
// wherever nothing was set — which is what the built-in value stands on.
func steering(cfg config.Config, prompts sessionPrompts) agent.Steering {
	return agent.Steering{
		CheckInInterval:  cfg.Behavior.CheckInIntervalRounds,
		CheckInDoublings: cfg.Behavior.CheckInMaxDoublings,
		SteerTargetChars: cfg.Summary.SteerTargetChars,
		CheckIn:          prompts.checkIn,
		Steer:            prompts.steer,
	}
}

// fingerprintOf is the text a session's prompt_hash is taken over: the system
// prompt as sent, and every wording a file replaced.
//
// An override that did not split cohorts would be a knob with no instrument
// on it — the hash is what puts sessions on either side of an edit, and a
// steer file is part of what a session was sent. A stage wording is the same
// fact about a backlog run: an edit to one changes what the run asked for and
// has to divide the record the same way. A session that overrode nothing
// contributes nothing and hashes exactly as it did before, so this does not
// move the population it is meant to divide.
//
// A file holding the built-in text contributes nothing either, which is what
// makes a scaffold safe to write: the whole point of one is a directory of
// the built-in wordings to start editing from, and a scaffold that put every
// session after it in a different cohort from every session before it would
// divide the record on a change nobody made.
func (p sessionPrompts) fingerprintOf(sysPrompt string) string {
	for _, w := range wordingKeys {
		text := *w.into(&p)
		// Compared with the surrounding whitespace off, because that is
		// what an editor adds when it saves a scaffolded file and it is not
		// a change to what the model is asked. A newline at the end of a
		// file must not put a session in a cohort of its own.
		if text == "" || strings.TrimSpace(text) == strings.TrimSpace(w.builtin()) {
			continue
		}
		sysPrompt += "\n\x00" + w.key + "\x00" + text
	}
	return sysPrompt
}
