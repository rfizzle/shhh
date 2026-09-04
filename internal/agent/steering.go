package agent

// Tuning the machinery that interrupts a turn.
//
// The mechanism stays here and the wording and the numbers come out of it.
// What a check-in is for, when it widens, that a steer asks rather than
// accuses, that a steer counts as a check-in — all of that is the product
// and none of it is a setting. How many rounds pass, how far the interval
// may widen, how much of the instruction is quoted back, and the sentences
// themselves are none of the product's business, and they are what a
// maintainer tuning a session has to be able to change without a build.
// See docs/capabilities/configuration.md#the-mechanism-is-code-its-wording-is-configuration.

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The substitutions an overriding wording may name. A check-in is told how
// many rounds have gone and how the turn it is addressed to finishes; a
// steer is told the instruction it was judged against and the reading's own
// reason. Nothing else varies between two of either.
const (
	PlaceholderRounds   = "{{rounds}}"
	PlaceholderFinished = "{{finished}}"
	PlaceholderTarget   = "{{target}}"
	PlaceholderReason   = "{{reason}}"
)

// The substitutions a backlog run's stage wordings may name: the blocks the
// run hands the model, which it places itself where the wording does not.
// They are spelled here and in the runner that builds the prompts, because
// this package validates a file before a session runs on it and the runner
// must not have to import the agent loop to assemble a string; a test holds
// the two sets together.
const (
	PlaceholderItem     = "{{item}}"
	PlaceholderPlan     = "{{plan}}"
	PlaceholderAnswers  = "{{answers}}"
	PlaceholderFindings = "{{findings}}"
	PlaceholderDiff     = "{{diff}}"
)

// What each of this package's own wordings may use. They are a function
// apiece rather than one taking a list because pairing a wording with the
// wrong list is itself a way to be silently wrong, and there is no reason a
// caller should have to get it right.
var (
	checkInPlaceholders = []string{PlaceholderRounds, PlaceholderFinished}
	steerPlaceholders   = []string{PlaceholderTarget, PlaceholderReason}
)

// ValidateCheckIn and ValidateSteer report the first substitution a wording
// names that it does not take. ValidateVerbatim is for the wordings that are
// sent as written — the reading instruction, the classifier's, and the
// standards sentence a run's stages share — where any substitution at all is
// a mistake.
func ValidateCheckIn(text string) error  { return validatePlaceholders(text, checkInPlaceholders) }
func ValidateSteer(text string) error    { return validatePlaceholders(text, steerPlaceholders) }
func ValidateVerbatim(text string) error { return validatePlaceholders(text, nil) }

// ValidateBlocks is the same reading for a wording whose substitutions the
// caller states: a backlog run's steps are the profile's, so which blocks
// each of them carries is something only the runner knows, and a list of
// functions here would be a second copy of a set that has moved.
func ValidateBlocks(text string, allowed []string) error {
	return validatePlaceholders(text, allowed)
}

// placeholderPattern matches anything written as a placeholder, so a
// mistyped one is caught as one rather than read as prose.
var placeholderPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// validatePlaceholders reports the first substitution in text that is not one
// of allowed.
//
// A mistyped placeholder is otherwise invisible: it reaches the model as
// literal braces, and the value it stood for — the instruction a steer
// quotes, the rounds a check-in names — never arrives at all. The wording
// still reads like a wording, so nothing on the way past looks wrong.
func validatePlaceholders(text string, allowed []string) error {
	for _, found := range placeholderPattern.FindAllString(text, -1) {
		if !slices.Contains(allowed, found) {
			return &PlaceholderError{Found: found, Allowed: allowed}
		}
	}
	return nil
}

// PlaceholderError names the substitution that was written and the ones that
// exist, because the reader's next act is to fix the file.
type PlaceholderError struct {
	Found   string
	Allowed []string
}

func (e *PlaceholderError) Error() string {
	if len(e.Allowed) == 0 {
		return e.Found + " is not a placeholder this wording takes; it takes none"
	}
	return e.Found + " is not a placeholder this wording takes; it takes " + strings.Join(e.Allowed, " and ")
}

// Steering is the interruption machinery's tuning: the two thresholds, the
// bound on what a steer quotes back, and the wordings themselves. A zero
// Steering is the built-in set, which is what every surface that configures
// nothing runs.
type Steering struct {
	// CheckInInterval is how many rounds pass before a turn is asked to take
	// stock. Zero or less keeps DefaultCheckInInterval. It is per-surface —
	// see SetCheckInInterval, which sets this field alone.
	CheckInInterval int
	// CheckInDoublings bounds how far that interval widens over one turn.
	// Zero keeps the built-in bound; any negative fixes the interval, so a
	// long turn is asked at the same rate from first round to last.
	CheckInDoublings int
	// SteerTargetChars bounds the instruction a steer quotes back. Zero keeps
	// the built-in bound; any negative quotes it whole, however long the user
	// typed.
	SteerTargetChars int
	// CheckIn and Steer replace the built-in wordings. Empty keeps them.
	// Each may name its own placeholders and nothing else — a loader checks
	// that before a session runs on one.
	CheckIn string
	Steer   string
}

// SetSteering installs the tuning for this surface. It replaces the whole
// set: a caller that means to change one number passes the rest as they were.
func (a *Agent) SetSteering(s Steering) { a.steering = s }

// Steering reports the tuning in force, for a caller that has to state what
// it configured.
func (a *Agent) Steering() Steering { return a.steering }

// doublings is how many times the check-in interval may double, defaulted. A
// negative is the caller's own answer of "never", and is kept.
func (s Steering) doublings() int {
	if s.CheckInDoublings == 0 {
		return DefaultCheckInDoublings
	}
	if s.CheckInDoublings < 0 {
		return 0
	}
	return s.CheckInDoublings
}

// checkInPrompt is the check-in this surface asks, built from the override
// when there is one.
func (s Steering) checkInPrompt(used int, whenFinished string) string {
	if s.CheckIn == "" {
		return CheckInPrompt(used, whenFinished)
	}
	return strings.NewReplacer(
		PlaceholderRounds, strconv.Itoa(used),
		PlaceholderFinished, whenFinished,
	).Replace(s.CheckIn)
}

// steerPrompt is the steer this surface sends, built from the override when
// there is one. The target is clamped either way: the anchor is whatever the
// user typed, and the bound is the setting rather than the wording.
func (s Steering) steerPrompt(target, reason string) string {
	target = strings.TrimSpace(target)
	if n := s.SteerTargetChars; n > 0 {
		target = clampRunes(target, n)
	} else if n == 0 {
		target = clampRunes(target, DefaultSteerTargetChars)
	}
	if s.Steer == "" {
		return buildSteer(target, reason)
	}
	return strings.NewReplacer(
		PlaceholderTarget, target,
		PlaceholderReason, strings.TrimSpace(reason),
	).Replace(s.Steer)
}
