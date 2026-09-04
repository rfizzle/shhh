package cli

// Reading the wordings a config file named.
//
// The interruption machinery — the steer, the check-in — the two auxiliary
// readers — the summarizer, the permission classifier — and every stage of a
// backlog run carry built-in prose. A `[prompts]` block replaces any of it
// with a file, so tuning what a session is told costs an edit and a restart
// rather than a build (docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).
//
// A checkout says the same thing by convention instead of by key: a file at
// `.shhh/prompts/<key>.md` in a trusted checkout is that wording, and it
// beats the user's file for that project. It is a convention rather than a
// key because a checkout may not point the settings at a path — a path named
// in a checkout is a path in every clone of it, anywhere on the machine —
// and because a wording that travels with the repository has to live in it
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
		path, named, undo := promptSource(w.key, w.named(c), projectDir)
		if path == "" {
			continue
		}
		text, err := readWording(path, named, undo, w.validate)
		if err != nil {
			return sessionPrompts{}, err
		}
		*w.into(&out) = text
	}
	return out, nil
}

// readWording is one file read and judged, with the sentence a reader acts
// on when it will not do. undo is what puts the built-in wording back, which
// differs by where the file was named: a key is removed and a checkout's file
// is deleted, and a reader told to do the other one is being sent to edit a
// file that has nothing to do with it.
func readWording(path, named, undo string, validate func(string) error) (string, error) {
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
	key      string
	named    func(config.PromptsConfig) string
	into     func(*sessionPrompts) *string
	validate func(string) error
}

// wordingKeys is every wording a file can replace, in the order the settings
// table declares them. It is a list rather than a switch because two surfaces
// walk it — the loader that reads them and the doctor's row that says whether
// they can be read — and a key visible to one and not the other is a key
// whose failure nobody sees until a session will not start.
var wordingKeys = []wording{
	{"steer", func(c config.PromptsConfig) string { return c.Steer },
		func(p *sessionPrompts) *string { return &p.steer }, agent.ValidateSteer},
	{"check_in", func(c config.PromptsConfig) string { return c.CheckIn },
		func(p *sessionPrompts) *string { return &p.checkIn }, agent.ValidateCheckIn},
	{"summary", func(c config.PromptsConfig) string { return c.Summary },
		func(p *sessionPrompts) *string { return &p.summary }, agent.ValidateVerbatim},
	{"classifier", func(c config.PromptsConfig) string { return c.Classifier },
		func(p *sessionPrompts) *string { return &p.classifier }, agent.ValidateVerbatim},
	{"todo_standards", func(c config.PromptsConfig) string { return c.TodoStandards },
		func(p *sessionPrompts) *string { return &p.todo.Standards }, agent.ValidateVerbatim},
	{"todo_research", func(c config.PromptsConfig) string { return c.TodoResearch },
		func(p *sessionPrompts) *string { return &p.todo.Research }, agent.ValidateTodoResearch},
	{"todo_implement", func(c config.PromptsConfig) string { return c.TodoImplement },
		func(p *sessionPrompts) *string { return &p.todo.Implement }, agent.ValidateTodoImplement},
	{"todo_review", func(c config.PromptsConfig) string { return c.TodoReview },
		func(p *sessionPrompts) *string { return &p.todo.Review }, agent.ValidateTodoReview},
	{"todo_review_task", func(c config.PromptsConfig) string { return c.TodoReviewTask },
		func(p *sessionPrompts) *string { return &p.todo.ReviewTask }, agent.ValidateTodoReview},
	{"todo_remediate", func(c config.PromptsConfig) string { return c.TodoRemediate },
		func(p *sessionPrompts) *string { return &p.todo.Remediate }, agent.ValidateTodoRemediate},
	{"todo_commit", func(c config.PromptsConfig) string { return c.TodoCommit },
		func(p *sessionPrompts) *string { return &p.todo.Commit }, agent.ValidateTodoCommit},
}

// wordingRow is one wording a file replaced, as a surface that reports on
// them finds it: the key, and the reason it will not load if it will not.
type wordingRow struct {
	key string
	err error
}

// readWordings reads every wording in force and keeps going past a failure,
// which is what separates a report from a session: a session stops at the
// first one because it cannot run, and a reader fixing their files wants all
// of them named at once.
func readWordings(c config.PromptsConfig, projectDir string) []wordingRow {
	var rows []wordingRow
	for _, w := range wordingKeys {
		path, named, undo := promptSource(w.key, w.named(c), projectDir)
		if path == "" {
			continue
		}
		_, err := readWording(path, named, undo, w.validate)
		rows = append(rows, wordingRow{key: w.key, err: err})
	}
	return rows
}

// promptSource is the file one wording is read from, and the words an error
// about it names it by: the checkout's own file where there is one, and
// otherwise whatever the settings pointed at.
//
// The checkout wins for the reason its settings do — what is true of a
// repository travels with the repository — and it cannot take a wording
// away: a checkout with no file for a key leaves the person's answer
// standing, so the two files are read per wording rather than per set.
func promptSource(key, configured, projectDir string) (path, named, undo string) {
	if projectDir != "" {
		p := filepath.Join(projectDir, key+".md")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, project.PromptsDir + "/" + key + ".md", "delete it"
		}
	}
	if configured == "" {
		return "", "", ""
	}
	return promptPath(configured), "config prompts." + key, "remove the key"
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
func (p sessionPrompts) fingerprintOf(sysPrompt string) string {
	for _, part := range []struct{ name, text string }{
		{"steer", p.steer},
		{"check_in", p.checkIn},
		{"summary", p.summary},
		{"classifier", p.classifier},
		{"todo_standards", p.todo.Standards},
		{"todo_research", p.todo.Research},
		{"todo_implement", p.todo.Implement},
		{"todo_review", p.todo.Review},
		{"todo_review_task", p.todo.ReviewTask},
		{"todo_remediate", p.todo.Remediate},
		{"todo_commit", p.todo.Commit},
	} {
		if part.text != "" {
			sysPrompt += "\n\x00" + part.name + "\x00" + part.text
		}
	}
	return sysPrompt
}
