package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// safetySections is a coding session with every section in force: a mode
// with a grant standing, a directory added to the scope, a mechanism holding
// the commands to a host list, a granted host, a trusted checkout, two
// servers, a secret and the toolset in its tiers.
func safetySections() []SafetySection {
	return []SafetySection{
		{Title: "mode and policy", ChangedBy: "/permissions", Lines: []string{
			"Mode: manual — every consequential call asks.",
			"",
			"Grants:",
			`  commands   "go test", that line alone — until this turn ends`,
			"  config     2 command patterns from behavior.command_allowlist — not this session's to revoke",
			"  allowlist  go test, make lint",
			"  denylist   rm -rf",
		}},
		{Title: "where it may write", ChangedBy: "/add-dir", Lines: []string{
			"Working scope:",
			"  session    /work/shhh",
			"  added      /work/shared-assets",
			"Sensitive — asks whatever the mode, and only /add-dir grants it:",
			"  ~/.kube",
			"  ~/.config/shhh",
			"  /work/shhh/.git — the repository's store and hooks",
		}},
		{Title: "containment", ChangedBy: "/sandbox", Lines: []string{
			"Containment",
			"sandbox-exec · workspace",
			"sandbox.require is off",
			"",
			"Command containment:",
			"  profile:   workspace (network: 1 host — proxy.golang.org)",
			"  masked:    ~/.ssh, ~/.aws, ~/.config/gh, ~/.netrc",
		}},
		{Title: "web", ChangedBy: "/permissions revoke hosts", Lines: []string{
			"  allowed    docs.rs from web.allow_hosts — not this session's to revoke",
			"  granted    pkg.go.dev, that host alone — until you revoke it",
			"  refused    pastebin.com from web.deny_hosts — before anything can allow it",
		}},
		{Title: "what the checkout was let load", ChangedBy: "/trust", Lines: []string{
			"trusted — its skills, agent profiles, suites, hooks and servers load",
		}},
		{Title: "mcp", ChangedBy: "/mcp", Lines: []string{
			"  docs — read-only, runs without asking · 3 tools",
			"  tracker — asks before acting · 12 tools",
		}},
		{Title: "secrets", ChangedBy: "/secret", Lines: []string{
			"  $DEPLOY_TOKEN — in every command, masked everywhere else",
			"secrets.env_mask is on — an inherited *_KEY, *_SECRET or *_TOKEN never reaches a command",
		}},
		{Title: "tools", ChangedBy: "/permissions, which decides which tiers ask", Lines: []string{
			"  read-only  read_file, list_directory, search, glob_files",
			"  execute    execute_command, process",
			"  mutating   write_file, edit_file",
			"  asks       fetch, spawn_agent",
		}},
	}
}

// safetyAbsent is the same session with containment unavailable, the
// checkout's trust withheld and no servers: each of those is stated as
// absent rather than left off the page.
func safetyAbsent() []SafetySection {
	out := safetySections()
	// A workspace in no repository: the store's line is not drawn at all.
	out[1].Lines = out[1].Lines[:len(out[1].Lines)-1]
	out[2] = SafetySection{Title: "containment", ChangedBy: "/sandbox", Absent: true, Lines: []string{
		"Containment",
		"unconfined — sandbox-exec not found at /usr/bin/sandbox-exec",
		"sandbox.require is off",
	}}
	out[4] = SafetySection{Title: "what the checkout was let load", ChangedBy: "/trust", Absent: true, Lines: []string{
		"not trusted",
		"Withheld",
		"This checkout is not trusted, so its skills and quality suites are not in this session.",
		"/trust loads them from the next session on.",
	}}
	out[5] = SafetySection{Title: "mcp", ChangedBy: "/mcp", Absent: true, Lines: []string{"no MCP servers in this session"}}
	return out
}

// Every section is on the page with the command that changes it, and the
// only keys offered are the ones that read.
func TestSafetyScreen_EverySectionNamesItsOwner(t *testing.T) {
	s := &SafetyScreen{Sections: safetySections(), Subject: "manual · sandbox-exec"}
	view := ansi.Strip(s.View(110))
	for _, want := range []string{
		"/safety", "manual · sandbox-exec",
		"MODE AND POLICY", "WHERE IT MAY WRITE", "CONTAINMENT", "WEB",
		"WHAT THE CHECKOUT WAS LET LOAD", "MCP", "SECRETS", "TOOLS",
		"changed with /permissions", "changed with /add-dir", "changed with /sandbox",
		"changed with /trust", "changed with /mcp", "changed with /secret",
		"until this turn ends", "until you revoke it",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the reading is missing %q:\n%s", want, view)
		}
	}
}

// An absent capability is a section that says so, not a section that is
// missing.
func TestSafetyScreen_AnAbsentCapabilityIsStated(t *testing.T) {
	view := ansi.Strip((&SafetyScreen{Sections: safetyAbsent()}).View(80))
	for _, want := range []string{"unconfined", "not trusted", "no MCP servers in this session", "MCP", "CONTAINMENT"} {
		if !strings.Contains(view, want) {
			t.Errorf("an absent capability was left out, %q:\n%s", want, view)
		}
	}
}

// A body longer than its pane scrolls, and each end names the sections it is
// sitting on rather than only counting rows.
func TestSafetyScreen_ScrollsAndNamesWhatIsFolded(t *testing.T) {
	s := &SafetyScreen{Sections: safetySections(), maxLines: 20}
	top := ansi.Strip(s.View(60))
	if !strings.Contains(top, "↓ 6 more · containment · web") {
		t.Fatalf("the foot of a long reading does not name what is below it:\n%s", top)
	}
	if !strings.Contains(top, "scroll") {
		t.Errorf("a reading that scrolls does not offer the key:\n%s", top)
	}
	for range 200 {
		s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		s.View(60)
	}
	bottom := ansi.Strip(s.View(60))
	if !strings.Contains(bottom, "↑") || !strings.Contains(bottom, "changed with /permissions, which decides") {
		t.Fatalf("scrolled to the end, the reading does not show its last section:\n%s", bottom)
	}
	if n := strings.Count(bottom, "\n") + 1; n > 20 {
		t.Errorf("the screen drew %d rows in a 20-row budget", n)
	}
	for _, line := range strings.Split(bottom, "\n") {
		if lipgloss.Width(line) > 60 {
			t.Errorf("a row ran past the terminal: %q", line)
		}
	}
}

// A body that fits offers no scroll key: a key that cannot act is not an
// offer (invariant 5).
func TestSafetyScreen_AShortReadingOffersNoScroll(t *testing.T) {
	s := &SafetyScreen{Sections: safetySections()[:1], maxLines: 40}
	if view := ansi.Strip(s.View(110)); strings.Contains(view, "scroll") {
		t.Errorf("a reading that fits offers a scroll:\n%s", view)
	}
}

// Nothing on the screen answers but leaving: the letters every other screen
// in the family acts on do nothing here.
func TestSafetyScreen_OnlyTheWayOutCloses(t *testing.T) {
	s := &SafetyScreen{Sections: safetySections()}
	for _, k := range []string{"a", "r", "w", "x", "enter", "d", "q"} {
		if done, _ := s.Update(key(k)); done {
			t.Errorf("%q closed a reading", k)
		}
	}
	if done, result := s.Update(key("esc")); !done || !result.canceled {
		t.Error("esc did not leave the reading")
	}
}

// TestGolden_SafetyScreen captures `/safety` with every section in force, the
// same session scrolled to its end, and one where containment, trust and the
// servers are absent and say so.
func TestGolden_SafetyScreen(t *testing.T) {
	captureGolden(t, "safety-screen", "the safety reading", goldenWidths, func(width int) []golden.Panel {
		scrolled := &SafetyScreen{Sections: safetySections(), Subject: "manual · sandbox-exec", maxLines: 24}
		for range 12 {
			scrolled.View(width)
			scrolled.Update(key("down"))
		}
		return []golden.Panel{
			{Label: "every section in force · each ends on the command that changes it",
				View: (&SafetyScreen{Sections: safetySections(), Subject: "manual · sandbox-exec"}).View(width)},
			{Label: "a pane shorter than the reading · the ends name the sections they fold",
				View: scrolled.View(width)},
			{Label: "containment unavailable, trust withheld, no servers · stated, not left out",
				View: (&SafetyScreen{Sections: safetyAbsent(), Subject: "manual · unconfined"}).View(width)},
		}
	})
}
