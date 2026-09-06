package cli

// The keys row: whether the terminal delivers the alt chords the keyboard
// offers.
//
// On macOS the Option key types a character — å for alt+a on a US layout —
// until the terminal's profile says to send the escape prefix instead, and
// the stock terminals ship with that off, so every alt chord shhh binds is
// dead on a Mac at its defaults. The chords stay on alt anyway: every ctrl
// letter the terminal delivers is spent or the line editor's, and the fix
// is one tick in a profile. This row is what tells a reader which tick, on
// the profile they are in, and the key list names the same setting beside
// the chords (docs/interface/reserved-keys.md#what-is-left).
//
// The setting is read from the terminal's preferences rather than by
// pressing anything: a diagnostic looks and does not touch, and a key
// pressed into the terminal would land in the doctor's own screen. What is
// read is the profile Terminal.app opens new windows with, or the profile
// the iTerm2 session says it is in, which is as close to "this window" as
// either application states.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// optionSends is what the Option key does in the profile that was read.
type optionSends int

const (
	// optionUnread: no setting was read — another platform, a terminal
	// whose preferences shhh does not know, or an export that failed.
	optionUnread optionSends = iota
	// optionCharacter: Option composes a character, and the chord never
	// arrives.
	optionCharacter
	// optionEscape: Option sends the escape prefix, which is what an alt
	// chord is on the wire.
	optionEscape
	// optionHighBit: iTerm2's "Meta" — the eighth bit set on the byte, which
	// is not the escape prefix and not a chord.
	optionHighBit
)

// optionKeyState is what could be read about the Option key.
type optionKeyState struct {
	GOOS string
	// Terminal is TERM_PROGRAM as the terminal set it, or TERM where it set
	// nothing, and Version is TERM_PROGRAM_VERSION.
	Terminal string
	Version  string
	// Profile is the profile the setting was read from.
	Profile string
	Sends   optionSends
	// RightOnly says the right Option key is the one that sends the prefix
	// while the left composes characters — iTerm2 sets the two apart.
	RightOnly bool
	// Elsewhere is the folder iTerm2 loads its preferences from when told
	// to, where the domain shhh reads is not what the application reads.
	Elsewhere string
	// Err is an export that could not be run or read, which is a different
	// answer from a terminal shhh does not read.
	Err error
}

// terminalName is the terminal as the row names it: the name a reader
// knows, with the version when the terminal states one.
func (s optionKeyState) terminalName() string {
	name := s.Terminal
	switch s.Terminal {
	case "Apple_Terminal":
		name = "Terminal.app"
	case "iTerm.app":
		name = "iTerm2"
	case "":
		name = "an unnamed terminal"
	}
	if s.Version != "" {
		return name + " " + s.Version
	}
	return name
}

// doctorPrefsTimeout bounds `defaults export`, which answers from cfprefsd
// in milliseconds and has no business taking longer.
const doctorPrefsTimeout = 3 * time.Second

// readPrefs exports one preference domain. A variable so the suite can run
// every probe without asking the developer's own terminal for its profile:
// a test reads its scratch directory, not the machine.
var readPrefs = func(ctx context.Context, domain string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, doctorPrefsTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "defaults", "export", domain, "-").Output()
	if err != nil {
		// The exit status alone says nothing; what `defaults` printed does.
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0 {
			return nil, fmt.Errorf("defaults export %s: %s", domain, bytes.TrimSpace(exit.Stderr))
		}
		return nil, fmt.Errorf("defaults export %s: %w", domain, err)
	}
	root, err := parsePlist(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("defaults export %s: %w", domain, err)
	}
	return root, nil
}

func probeOptionKey(ctx context.Context, _ config.Config) doctorFinding {
	if ctx == nil {
		ctx = context.Background()
	}
	return doctorOptionKey(readOptionKey(ctx, runtime.GOOS, os.Getenv, readPrefs))
}

// readOptionKey finds the terminal and reads its profile's Option setting.
func readOptionKey(ctx context.Context, goos string, getenv func(string) string,
	prefs func(context.Context, string) (any, error)) optionKeyState {
	s := optionKeyState{GOOS: goos, Terminal: getenv("TERM_PROGRAM"), Version: getenv("TERM_PROGRAM_VERSION")}
	if s.Terminal == "" {
		s.Terminal = getenv("TERM")
	}
	if goos != "darwin" {
		return s
	}
	switch s.Terminal {
	case "Apple_Terminal":
		root, err := prefs(ctx, "com.apple.Terminal")
		if err != nil {
			s.Err = err
			return s
		}
		// New windows open with the default profile; a fresh install has
		// never written the key, and Basic is the profile it means.
		s.Profile = plistString(root, "Default Window Settings")
		if s.Profile == "" {
			s.Profile = "Basic"
		}
		// "Use Option as Meta key" sends the escape prefix despite its name.
		// The key is absent until the box has been ticked once, and absent
		// is off.
		s.Sends = optionCharacter
		if plistBool(root, "Window Settings", s.Profile, "useOptionAsMetaKey") {
			s.Sends = optionEscape
		}
	case "iTerm.app":
		root, err := prefs(ctx, "com.googlecode.iterm2")
		if err != nil {
			s.Err = err
			return s
		}
		// Told to load its preferences from a folder, iTerm2 reads that
		// folder and the domain here is whatever was last copied out of it,
		// so a reading from the domain could pass on a profile nobody uses.
		if plistBool(root, "LoadPrefsFromCustomFolder") {
			s.Elsewhere = plistString(root, "PrefsCustomFolder")
			if s.Elsewhere == "" {
				s.Elsewhere = "a custom folder"
			}
			return s
		}
		s.Profile = getenv("ITERM_PROFILE")
		profile, ok := itermProfile(root, s.Profile)
		if !ok {
			return s
		}
		s.Profile = plistString(profile, "Name")
		// "Option Key Sends" is the left Option key and "Right Option Key
		// Sends" the right: 0 is Normal, 1 is Meta and 2 is Esc+, and absent
		// is Normal. Either key sending the prefix delivers the chords.
		left, _ := plistInt(profile, "Option Key Sends")
		right, _ := plistInt(profile, "Right Option Key Sends")
		switch {
		case left == 2:
			s.Sends = optionEscape
		case right == 2:
			s.Sends, s.RightOnly = optionEscape, true
		case left == 1 || right == 1:
			s.Sends = optionHighBit
		default:
			s.Sends = optionCharacter
		}
	}
	return s
}

// itermProfile is the session's profile, by the name iTerm2 put in the
// environment, else the profile new windows open with.
func itermProfile(root any, name string) (any, bool) {
	list, _ := plistAt(root, "New Bookmarks")
	profiles, _ := list.([]any)
	guid := plistString(root, "Default Bookmark Guid")
	for _, p := range profiles {
		if name != "" && plistString(p, "Name") == name {
			return p, true
		}
		if name == "" && plistString(p, "Guid") == guid {
			return p, true
		}
	}
	return nil, false
}

// altChords are the chords the keyboard spends on alt, read from the
// register so a binding moved there is named by existing rather than by
// being remembered here.
func altChords() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range append(keys.Surfaces(), keys.Programs()...) {
		for _, b := range s.Bindings {
			for _, k := range b.Keys() {
				if strings.HasPrefix(k, "alt+") && !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// optionFix is the tick in the words of the terminal's own settings, with
// the profile the reading was taken from in its place. Both terminals'
// lines when the reading could not say which one the reader is in. The
// screen clips a fix line rather than wrapping it, so each stays under the
// seventy-odd columns an 80-column window leaves — the app is the first
// crumb and the Settings window is left unsaid.
func optionFix(terminal, profile string) []string {
	crumb := ""
	if profile != "" {
		crumb = profile + " › "
	}
	terminalApp := "Terminal.app › Profiles › " + crumb + "Keyboard › \"Use Option as Meta key\""
	iterm := "iTerm2 › Profiles › " + crumb + "Keys › Left Option key: Esc+"
	var fix []string
	switch terminal {
	case "Apple_Terminal":
		fix = []string{terminalApp}
	case "iTerm.app":
		fix = []string{iterm}
	default:
		fix = []string{terminalApp, iterm}
	}
	return append(fix, "shhh doctor   to check it took, once the settings window is closed")
}

// doctorOptionKey reads the Option key. Only a Mac has the question, and
// only the two stock terminals have a setting shhh knows where to read; a
// profile where Option types a character is a warning rather than a failure
// — the keyboard works, and four of its chords do not.
func doctorOptionKey(s optionKeyState) doctorFinding {
	chords := joinAnd(altChords())
	if s.GOOS != "darwin" {
		return doctorFinding{
			Subject: "no Option key on " + s.GOOS, Detail: s.terminalName(),
			Outcome: "not a question here", State: components.DoctorSkipped,
		}
	}
	if s.Err != nil {
		return doctorFinding{
			Subject: "the Option setting of " + s.terminalName() + " could not be read", Detail: s.Err.Error(),
			Outcome: "unread", State: components.DoctorWarned,
			Consequence: "if alt+a types a character, " + chords + " never reach shhh",
			FixLabel:    "show the setting to turn on",
			Fix:         optionFix(s.Terminal, s.Profile),
		}
	}
	profile := s.Profile + " profile"
	if s.Terminal == "Apple_Terminal" {
		// The one Terminal.app names is the one new windows open with; a
		// window opened on another profile has a setting of its own.
		profile += " (new windows)"
	}
	switch s.Sends {
	case optionEscape:
		detail := joinDetail(s.terminalName(), profile)
		if s.RightOnly {
			detail = joinDetail(detail, "the right Option key")
		}
		return doctorFinding{
			Subject: "Option sends the escape prefix", Detail: detail,
			Outcome: "delivered",
		}
	case optionCharacter:
		return doctorFinding{
			Subject: "Option types a character", Detail: joinDetail(s.terminalName(), profile),
			Outcome: "chords dead", State: components.DoctorWarned,
			Consequence: chords + " type a character and never reach shhh",
			FixLabel:    "show the setting to turn on",
			Fix:         optionFix(s.Terminal, s.Profile),
		}
	case optionHighBit:
		return doctorFinding{
			Subject: "Option sets the eighth bit", Detail: joinDetail(s.terminalName(), profile),
			Outcome: "not a chord", State: components.DoctorWarned,
			Consequence: chords + " arrive as bytes, not chords; pick Esc+",
			FixLabel:    "show the setting to change",
			Fix:         optionFix(s.Terminal, s.Profile),
		}
	}
	f := doctorFinding{
		Subject: "the Option setting of " + s.terminalName() + " is not one shhh reads",
		Detail:  "if alt+a types a character, its option-as-alt setting is off",
		Outcome: "not read", State: components.DoctorSkipped,
	}
	switch {
	case s.Terminal == "tmux":
		// The terminal behind tmux is the one with the setting, and it is
		// nearly always one of the two stock ones — so the ticks are worth
		// showing even though the row cannot say which applies.
		f.Detail = "the terminal behind it is the one with the setting"
		f.FixLabel = "show the setting in either stock terminal"
		f.Fix = optionFix("", "")
	case s.Elsewhere != "":
		f.Detail = "its preferences are loaded from " + s.Elsewhere + ", which shhh does not read"
		f.FixLabel = "show the setting to check"
		f.Fix = optionFix(s.Terminal, "")
	case s.Terminal == "iTerm.app" && s.Profile != "":
		f.Detail = "no profile named " + s.Profile + " in its preferences"
	case s.Terminal == "iTerm.app":
		f.Detail = "its default profile is not in its preferences"
	}
	return f
}
