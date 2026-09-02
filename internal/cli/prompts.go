package cli

// Reading the wordings a config file named.
//
// The interruption machinery — the steer, the check-in — and the two
// auxiliary readers — the summarizer, the permission classifier — carry
// built-in prose. A `[prompts]` block replaces any of it with a file, so
// tuning what a session is told costs an edit and a restart rather than a
// build (docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration).
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
}

// loadPrompts reads every wording the config named.
//
// A file that cannot be read, one that is empty, or one whose placeholders
// are not the ones its wording takes stops the session with the path and the
// reason. Falling back to the built-in text would leave a session running
// shhh's steer while the operator who wrote the path believes it is running
// theirs — and a fortnight of comparison over two cohorts that are in fact
// one. There is no reading of the record that recovers from that, so it fails
// here, where the person who wrote the path is still watching.
func loadPrompts(c config.PromptsConfig) (sessionPrompts, error) {
	var out sessionPrompts
	for _, f := range []struct {
		key      string
		path     string
		into     *string
		validate func(string) error
	}{
		{"prompts.steer", c.Steer, &out.steer, agent.ValidateSteer},
		{"prompts.check_in", c.CheckIn, &out.checkIn, agent.ValidateCheckIn},
		{"prompts.summary", c.Summary, &out.summary, agent.ValidateVerbatim},
		{"prompts.classifier", c.Classifier, &out.classifier, agent.ValidateVerbatim},
	} {
		if f.path == "" {
			continue
		}
		path := promptPath(f.path)
		data, err := os.ReadFile(path)
		if err != nil {
			return sessionPrompts{}, fmt.Errorf("config %s: %w", f.key, err)
		}
		text := string(data)
		// An empty file is the same failure as an unreadable one wearing a
		// disguise: every reader downstream takes an empty wording as "not
		// configured", so a truncated write would put the built-in words
		// back and the record would show a session that overrode nothing.
		if strings.TrimSpace(text) == "" {
			return sessionPrompts{}, fmt.Errorf("config %s: %s: the file is empty; remove the key to use the built-in wording", f.key, path)
		}
		if err := f.validate(text); err != nil {
			return sessionPrompts{}, fmt.Errorf("config %s: %s: %w", f.key, path, err)
		}
		*f.into = text
	}
	return out, nil
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
// steer file is part of what a session was sent. A session that overrode
// nothing contributes nothing and hashes exactly as it did before, so this
// does not move the population it is meant to divide.
func (p sessionPrompts) fingerprintOf(sysPrompt string) string {
	for _, part := range []struct{ name, text string }{
		{"steer", p.steer},
		{"check_in", p.checkIn},
		{"summary", p.summary},
		{"classifier", p.classifier},
	} {
		if part.text != "" {
			sysPrompt += "\n\x00" + part.name + "\x00" + part.text
		}
	}
	return sysPrompt
}
