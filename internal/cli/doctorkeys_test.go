package cli

// The keys row (docs/interface/reserved-keys.md#what-is-left). The reading
// is a pure function of what the export said, so every outcome is checked
// here on a machine with no Mac, no `defaults` and no terminal.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// terminalPlist is Terminal.app's export cut down to the keys the reading
// walks, with the profile's colours as the data value the real one holds.
const terminalPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Default Window Settings</key>
	<string>Pro</string>
	<key>SecureKeyboardEntry</key>
	<false/>
	<key>Window Settings</key>
	<dict>
		<key>Basic</key>
		<dict>
			<key>name</key>
			<string>Basic</string>
			<key>type</key>
			<string>Window Settings</string>
		</dict>
		<key>Pro</key>
		<dict>
			<key>BackgroundColor</key>
			<data>
			YnBsaXN0MDDUAQIDBAUGBwpYJHZlcnNpb25ZJGFyY2hpdmVy
			</data>
			<key>Font</key>
			<data>YnBsaXN0MDA=</data>
			<key>columnCount</key>
			<integer>120</integer>
			<key>useOptionAsMetaKey</key>
			<true/>
			<key>name</key>
			<string>Pro</string>
		</dict>
	</dict>
</dict>
</plist>
`

const itermPlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Default Bookmark Guid</key>
	<string>DEFAULT-GUID</string>
	<key>New Bookmarks</key>
	<array>
		<dict>
			<key>Guid</key>
			<string>DEFAULT-GUID</string>
			<key>Name</key>
			<string>Default</string>
			<key>Transparency</key>
			<real>0.1</real>
		</dict>
		<dict>
			<key>Guid</key>
			<string>WORK-GUID</string>
			<key>Name</key>
			<string>Work</string>
			<key>Option Key Sends</key>
			<integer>2</integer>
			<key>Right Option Key Sends</key>
			<integer>1</integer>
		</dict>
		<dict>
			<key>Guid</key>
			<string>META-GUID</string>
			<key>Name</key>
			<string>Meta</string>
			<key>Option Key Sends</key>
			<integer>1</integer>
		</dict>
		<dict>
			<key>Guid</key>
			<string>RIGHT-GUID</string>
			<key>Name</key>
			<string>Right</string>
			<key>Right Option Key Sends</key>
			<integer>2</integer>
		</dict>
	</array>
</dict>
</plist>
`

func TestParsePlist_ReadsEveryValueTypeAnExportHolds(t *testing.T) {
	root, err := parsePlist(strings.NewReader(terminalPlist))
	if err != nil {
		t.Fatal(err)
	}
	if got := plistString(root, "Default Window Settings"); got != "Pro" {
		t.Fatalf("the default profile reads %q", got)
	}
	if !plistBool(root, "Window Settings", "Pro", "useOptionAsMetaKey") {
		t.Fatal("a ticked box reads as unticked")
	}
	if plistBool(root, "Window Settings", "Basic", "useOptionAsMetaKey") {
		t.Fatal("an absent key reads as ticked")
	}
	if plistBool(root, "SecureKeyboardEntry") {
		t.Fatal("<false/> reads as true")
	}
	if n, ok := plistInt(root, "Window Settings", "Pro", "columnCount"); !ok || n != 120 {
		t.Fatalf("the integer reads %d, %v", n, ok)
	}
	if got := plistString(root, "Window Settings", "Pro", "Font"); got != "YnBsaXN0MDA=" {
		t.Fatalf("data is not kept as its text: %q", got)
	}
	if got := plistString(root, "Window Settings", "Pro", "BackgroundColor"); got != "YnBsaXN0MDDUAQIDBAUGBwpYJHZlcnNpb25ZJGFyY2hpdmVy" {
		t.Fatalf("data broken across lines keeps its layout: %q", got)
	}
	if _, ok := plistAt(root, "Window Settings", "Pro", "columnCount", "deeper"); ok {
		t.Fatal("walking through a scalar found something")
	}

	iterm, err := parsePlist(strings.NewReader(itermPlist))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := plistAt(iterm, "New Bookmarks")
	if profiles, _ := list.([]any); len(profiles) != 4 {
		t.Fatalf("the array reads as %v", list)
	}
	if f, _ := plistAt(iterm, "New Bookmarks"); f == nil {
		t.Fatal("the array is nil")
	}
}

func TestParsePlist_RefusesWhatIsNotOne(t *testing.T) {
	for _, in := range []string{
		`<html></html>`,
		`<plist version="1.0"><dict><string>value with no key</string></dict></plist>`,
		`<plist version="1.0"><dict><key>k</key></dict></plist>`,
		`<plist version="1.0"></plist>`,
		`<plist version="1.0"><thing/></plist>`,
	} {
		if _, err := parsePlist(strings.NewReader(in)); err == nil {
			t.Errorf("%q was read as a property list", in)
		}
	}
}

// prefsFrom is an export that answers with the fixture for the domain.
func prefsFrom(t *testing.T, domains map[string]string) func(context.Context, string) (any, error) {
	t.Helper()
	return func(_ context.Context, domain string) (any, error) {
		src, ok := domains[domain]
		if !ok {
			return nil, errors.New("Domain " + domain + " does not exist")
		}
		return parsePlist(strings.NewReader(src))
	}
}

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestReadOptionKey_TerminalAppReadsTheDefaultProfile(t *testing.T) {
	prefs := prefsFrom(t, map[string]string{"com.apple.Terminal": terminalPlist})
	s := readOptionKey(t.Context(), "darwin",
		env(map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM_PROGRAM_VERSION": "455"}), prefs)
	if s.Profile != "Pro" || s.Sends != optionEscape {
		t.Fatalf("a ticked profile reads as %+v", s)
	}
	if got := s.terminalName(); got != "Terminal.app 455" {
		t.Fatalf("the terminal is named %q", got)
	}

	// The profile a fresh install opens with has never had the key written.
	fresh := strings.Replace(terminalPlist, "<string>Pro</string>", "<string>Basic</string>", 1)
	s = readOptionKey(t.Context(), "darwin", env(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}),
		prefsFrom(t, map[string]string{"com.apple.Terminal": fresh}))
	if s.Profile != "Basic" || s.Sends != optionCharacter {
		t.Fatalf("an unticked profile reads as %+v", s)
	}
}

func TestReadOptionKey_ITerm2ReadsTheSessionsProfile(t *testing.T) {
	prefs := prefsFrom(t, map[string]string{"com.googlecode.iterm2": itermPlist})
	cases := []struct {
		profile string
		want    optionSends
		name    string
	}{
		{"", optionCharacter, "Default"},
		{"Work", optionEscape, "Work"},
		{"Meta", optionHighBit, "Meta"},
		{"Right", optionEscape, "Right"},
	}
	for _, c := range cases {
		s := readOptionKey(t.Context(), "darwin",
			env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_PROFILE": c.profile}), prefs)
		if s.Sends != c.want || s.Profile != c.name {
			t.Errorf("profile %q reads as %+v", c.profile, s)
		}
		if s.RightOnly != (c.profile == "Right") {
			t.Errorf("profile %q names the right key wrongly: %+v", c.profile, s)
		}
	}

	// Preferences loaded from a folder are not the domain's, and a reading
	// taken there could pass on a profile nobody uses.
	custom := strings.Replace(itermPlist, "<key>Default Bookmark Guid</key>",
		"<key>LoadPrefsFromCustomFolder</key><true/><key>PrefsCustomFolder</key><string>~/dotfiles/iterm</string><key>Default Bookmark Guid</key>", 1)
	s := readOptionKey(t.Context(), "darwin", env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_PROFILE": "Work"}),
		prefsFrom(t, map[string]string{"com.googlecode.iterm2": custom}))
	if s.Sends != optionUnread || s.Elsewhere != "~/dotfiles/iterm" {
		t.Fatalf("preferences kept elsewhere read as %+v", s)
	}
	s = readOptionKey(t.Context(), "darwin",
		env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_PROFILE": "Gone"}), prefs)
	if s.Sends != optionUnread || s.Profile != "Gone" {
		t.Fatalf("a profile the preferences lack reads as %+v", s)
	}
}

func TestReadOptionKey_AsksNothingOffAMac(t *testing.T) {
	asked := false
	prefs := func(context.Context, string) (any, error) { asked = true; return nil, nil }
	s := readOptionKey(t.Context(), "linux", env(map[string]string{"TERM": "xterm-kitty"}), prefs)
	if asked {
		t.Fatal("a Linux machine was asked for Mac preferences")
	}
	if s.Terminal != "xterm-kitty" || s.Sends != optionUnread {
		t.Fatalf("off a Mac the state reads %+v", s)
	}
	s = readOptionKey(t.Context(), "darwin", env(map[string]string{"TERM_PROGRAM": "ghostty"}), prefs)
	if asked {
		t.Fatal("a terminal shhh does not read was asked for preferences")
	}
	if s.Sends != optionUnread {
		t.Fatalf("an unread terminal reads %+v", s)
	}
}

func TestReadOptionKey_KeepsAnExportThatFailed(t *testing.T) {
	prefs := prefsFrom(t, map[string]string{})
	s := readOptionKey(t.Context(), "darwin", env(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}), prefs)
	if s.Err == nil || !strings.Contains(s.Err.Error(), "com.apple.Terminal") {
		t.Fatalf("the failure is not kept: %+v", s)
	}
}

// The chords the row names are the register's, so a binding moved onto alt
// is named without this file hearing about it.
func TestAltChords_AreTheRegisters(t *testing.T) {
	got := altChords()
	want := map[string]bool{}
	for _, b := range []keys.Binding{keys.Draft.Reasoning, keys.Draft.Agents, keys.Draft.NextAgent, keys.Draft.PrevAgent} {
		for _, k := range b.Keys() {
			if strings.HasPrefix(k, "alt+") {
				want[k] = true
			}
		}
	}
	for _, k := range got {
		if !strings.HasPrefix(k, "alt+") {
			t.Errorf("%q is not an alt chord", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("the register's %q is not named", k)
	}
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Errorf("%q is named twice", k)
		}
		seen[k] = true
	}
}

func TestDoctorOptionKey_OffAMacIsNotAQuestion(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "linux", Terminal: "xterm-256color"})
	if f.State != components.DoctorSkipped {
		t.Fatalf("a Linux machine is judged: %+v", f)
	}
	if !strings.Contains(f.Subject, "linux") || f.Detail != "xterm-256color" {
		t.Fatalf("the row does not say where it is: %+v", f)
	}
}

func TestDoctorOptionKey_ATickedProfilePasses(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "Apple_Terminal", Version: "455",
		Profile: "Pro", Sends: optionEscape})
	if f.State != components.DoctorPassed || f.Outcome != "delivered" {
		t.Fatalf("a profile that sends the prefix does not pass: %+v", f)
	}
	if !strings.Contains(f.Detail, "Terminal.app 455") || !strings.Contains(f.Detail, "Pro profile (new windows)") {
		t.Fatalf("the row does not name the terminal and which profile: %q", f.Detail)
	}
	if f.Consequence != "" || len(f.Fix) != 0 {
		t.Fatalf("a passing check offered a fix: %+v", f)
	}
	right := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Right", Sends: optionEscape, RightOnly: true})
	if right.State != components.DoctorPassed || !strings.Contains(right.Detail, "right Option") {
		t.Fatalf("a profile whose right key sends the prefix does not say so: %+v", right)
	}
}

// The screen clips a fix line and a consequence rather than wrapping them,
// so every one of them has to fit an 80-column window with the row's own
// indent taken off — or the reader loses the name of the box, which is the
// whole of what the row is for.
func TestDoctorOptionKey_FitsAnEightyColumnScreen(t *testing.T) {
	states := []optionKeyState{
		{GOOS: "darwin", Terminal: "Apple_Terminal", Profile: "Basic", Sends: optionCharacter},
		{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Default", Sends: optionCharacter},
		{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Default", Sends: optionHighBit},
		{GOOS: "darwin", Terminal: "Apple_Terminal", Err: errors.New("defaults export com.apple.Terminal: exit status 1")},
		{GOOS: "darwin", Terminal: "tmux"},
		{GOOS: "darwin", Terminal: "iTerm.app", Elsewhere: "~/dotfiles/iterm"},
	}
	for _, s := range states {
		f := doctorOptionKey(s)
		if n := len([]rune(f.Consequence)); n > 76 {
			t.Errorf("%+v: the consequence is %d columns: %q", s, n, f.Consequence)
		}
		for _, line := range f.Fix {
			if n := len([]rune(line)); n > 74 {
				t.Errorf("%+v: a fix line is %d columns: %q", s, n, line)
			}
		}
	}
}

// The warning names every alt chord the keyboard has, the character the
// reader will see instead, and the tick in the terminal's own words with
// the profile it was read from — the reader is sent to one box, not to a
// settings window.
func TestDoctorOptionKey_TypingACharacterWarnsAndNamesTheTick(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "Apple_Terminal", Profile: "Basic", Sends: optionCharacter})
	if f.State != components.DoctorWarned {
		t.Fatalf("a profile that types characters did not warn: %+v", f)
	}
	for _, chord := range altChords() {
		if !strings.Contains(f.Consequence, chord) {
			t.Errorf("the consequence does not name %q: %q", chord, f.Consequence)
		}
	}
	if !strings.Contains(f.Consequence, "never reach shhh") {
		t.Errorf("the consequence does not say what the reader loses: %q", f.Consequence)
	}
	fix := strings.Join(f.Fix, "\n")
	if !strings.Contains(fix, "Terminal.app › Profiles › Basic › Keyboard") || !strings.Contains(fix, "Use Option as Meta key") {
		t.Errorf("the fix is not the tick on the profile that was read: %v", f.Fix)
	}
	if !strings.Contains(fix, "shhh doctor") {
		t.Errorf("the fix does not say how to check it took: %v", f.Fix)
	}

	iterm := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Default", Sends: optionCharacter})
	if fix := strings.Join(iterm.Fix, "\n"); !strings.Contains(fix, "iTerm2 › Profiles › Default › Keys") || !strings.Contains(fix, "Esc+") || strings.Contains(fix, "Terminal.app") {
		t.Errorf("iTerm2 is not sent to its own setting: %v", iterm.Fix)
	}
}

// iTerm2's "Meta" is the setting a reader reaches for first and it is not
// the one: the byte arrives with its eighth bit set, which no decoder reads
// as a chord. The row says so rather than reporting it as delivered.
func TestDoctorOptionKey_MetaIsNotAChord(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Meta", Sends: optionHighBit})
	if f.State != components.DoctorWarned {
		t.Fatalf("Meta passed: %+v", f)
	}
	if !strings.Contains(f.Consequence, "Esc+") || !strings.Contains(strings.Join(f.Fix, "\n"), "Esc+") {
		t.Fatalf("the row does not point at Esc+: %+v", f)
	}
}

// An export that failed is not a pass: the row says what to look for at the
// prompt and where the tick is, so the reader can settle it by hand.
func TestDoctorOptionKey_AnUnreadableSettingStillNamesTheTick(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "Apple_Terminal", Err: errors.New("defaults: not found")})
	if f.State != components.DoctorWarned || f.Outcome != "unread" {
		t.Fatalf("an unreadable setting reads as %+v", f)
	}
	if !strings.Contains(f.Consequence, "types a character") || !strings.Contains(strings.Join(f.Fix, "\n"), "Use Option as Meta key") {
		t.Fatalf("the reader is not told how to settle it by hand: %+v", f)
	}
}

func TestDoctorOptionKey_ATerminalItDoesNotReadIsNotChecked(t *testing.T) {
	f := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "ghostty"})
	if f.State != components.DoctorSkipped || !strings.Contains(f.Subject, "ghostty") {
		t.Fatalf("an unknown terminal is judged: %+v", f)
	}
	if !strings.Contains(f.Detail, "types a character") {
		t.Fatalf("the row does not say how to tell by hand: %q", f.Detail)
	}
	if len(f.Fix) != 0 {
		t.Fatalf("a terminal with a setting of its own is sent to another's: %v", f.Fix)
	}
	// Behind tmux the terminal is nearly always one of the two stock ones,
	// so the row shows both ticks rather than none.
	tmux := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "tmux", Version: "3.4"})
	if tmux.State != components.DoctorSkipped || !strings.Contains(tmux.Detail, "behind") {
		t.Fatalf("tmux is not named as the thing in front: %+v", tmux)
	}
	if fix := strings.Join(tmux.Fix, "\n"); !strings.Contains(fix, "Terminal.app › Profiles › Keyboard") || !strings.Contains(fix, "iTerm2 › Profiles › Keys") {
		t.Fatalf("tmux is not shown both ticks: %v", tmux.Fix)
	}
	elsewhere := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "iTerm.app", Elsewhere: "~/dotfiles/iterm"})
	if elsewhere.State != components.DoctorSkipped || !strings.Contains(elsewhere.Detail, "~/dotfiles/iterm") || len(elsewhere.Fix) == 0 {
		t.Fatalf("preferences kept elsewhere are not named: %+v", elsewhere)
	}
	gone := doctorOptionKey(optionKeyState{GOOS: "darwin", Terminal: "iTerm.app", Profile: "Gone"})
	if gone.State != components.DoctorSkipped || !strings.Contains(gone.Detail, "Gone") {
		t.Fatalf("a missing iTerm2 profile is not named: %+v", gone)
	}
}
