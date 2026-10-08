package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// safetyText is every section as the reader would read it, heading and all.
func safetyText(m Model) string {
	var b strings.Builder
	for _, sec := range m.safetySections() {
		b.WriteString(strings.ToUpper(sec.Title) + "\n")
		b.WriteString(strings.Join(sec.Lines, "\n") + "\n")
		b.WriteString("changed with " + sec.ChangedBy + "\n")
	}
	return b.String()
}

// sectionNamed is one section by its title.
func sectionNamed(t *testing.T, m Model, title string) components.SafetySection {
	t.Helper()
	for _, sec := range m.safetySections() {
		if sec.Title == title {
			return sec
		}
	}
	t.Fatalf("no section named %q", title)
	return components.SafetySection{}
}

// safetyModel is a coding session with every section in force.
func safetyModel(t *testing.T) Model {
	t.Helper()
	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, nil).
		WithScope(sc).
		WithApprovalMode(agent.ModeManual, nil).
		WithCommandAllowlist([]string{"go test"}).
		WithCommandDenylist([]string{"rm -rf"}).
		WithHostRules([]string{"docs.rs"}, []string{"pastebin.com"}).
		WithGatedTools(fetchPreviews()).
		WithToolDefinitions([]ToolTokens{
			{Name: "read_file"}, {Name: "execute_command"}, {Name: "edit_file"},
			{Name: "web_fetch"}, {Name: "docs__search"},
		}).
		WithContainment(Containment{
			Status: "contained: sandbox-exec (workspace profile)", Mechanism: "sandbox-exec",
			Profile: "workspace", Now: func() string { return "Command containment:\n  masked:    ~/.ssh" },
		}).
		WithMCP(MCP{Has: func(name string) bool { return strings.HasPrefix(name, "docs__") }}).
		WithSafety(Safety{
			Trust:   Trust{Granted: true},
			Servers: func() []SafetyServer { return []SafetyServer{{Name: "docs", ReadOnly: true, Status: "1 tool"}} },
			Secrets: func() []string { return []string{"DEPLOY_TOKEN"} },
			EnvMask: true,
		})
}

// Every section is read from the function its owning command already uses,
// and ends on that command.
func TestSafety_EverySectionIsItsOwnersReading(t *testing.T) {
	m := safetyModel(t)
	text := safetyText(m)
	for _, want := range []string{
		m.modeStatus(), "allowlist  go test", "denylist   rm -rf",
		"Working scope:", "Sensitive — asks whatever the mode",
		"sandbox-exec · workspace", "sandbox.require is off", "masked:    ~/.ssh",
		"docs.rs from web.allow_hosts", "pastebin.com from web.deny_hosts",
		"trusted —",
		"docs — read-only, runs without asking · 1 tool",
		"$DEPLOY_TOKEN", "secrets.env_mask is on",
		"read-only  read_file", "execute    execute_command", "mutating   edit_file",
		"asks       web_fetch", "servers    1 tool",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the reading is missing %q:\n%s", want, text)
		}
	}
	for _, owner := range []string{"/permissions", "/add-dir", "/sandbox", "/trust", "/mcp", "/secret"} {
		if !strings.Contains(text, "changed with "+owner) {
			t.Errorf("no section names %s as its owner", owner)
		}
	}
	// The grant listing is the one /permissions grants prints, word for word.
	if !strings.Contains(strings.Join(sectionNamed(t, m, "mode and policy").Lines, "\n"), m.grantStatus()) {
		t.Error("the grants are not /permissions grants' own listing")
	}
}

// An absent capability is stated as absent, never left off the page.
func TestSafety_AnAbsentCapabilityIsStated(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, nil).
		WithContainment(Containment{
			Status: "unconfined — no mechanism", Detail: "sandbox-exec not found",
		}).
		WithSafety(Safety{Trust: Trust{Withheld: []string{"skills", "quality suites"}}})
	for title, want := range map[string]string{
		"containment":                    "unconfined",
		"what the checkout was let load": "This checkout is not trusted, so its skills and quality suites",
		"mcp":                            "no MCP servers in this session",
		"web":                            "no web tools",
		"secrets":                        "no secrets declared",
	} {
		sec := sectionNamed(t, m, title)
		if !strings.Contains(strings.Join(sec.Lines, "\n"), want) {
			t.Errorf("%s does not say %q: %q", title, want, sec.Lines)
		}
		if title != "secrets" && !sec.Absent {
			t.Errorf("%s is not marked absent", title)
		}
	}
}

// A conversation says it has no mode and no fetch cards rather than showing
// one it does not run under.
func TestSafety_AConversationHasNoMode(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, nil).
		WithConversation().
		WithGatedTools(fetchPreviews()).
		WithToolDefinitions([]ToolTokens{{Name: "read_file"}, {Name: "web_fetch"}})
	text := safetyText(m)
	for _, want := range []string{
		conversationModeNote, "No card is drawn",
		"a conversation fetches without a card",
		"a conversation runs no commands",
		"read-only  read_file, web_fetch",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("a conversation's reading is missing %q:\n%s", want, text)
		}
	}
	if got := m.safetySubject(); got != "conversation · "+conversationModeWord {
		t.Errorf("subject = %q", got)
	}
}

// A host granted on a card is on the next reading, with when it ends.
func TestSafety_AHostGrantIsReadLive(t *testing.T) {
	m := safetyModel(t)
	if strings.Contains(safetyText(m), "pkg.go.dev") {
		t.Fatal("the host is on the reading before it was granted")
	}
	m.grantHost("pkg.go.dev", grantOffer{length: forThisSession})
	m.grantHost("proxy.golang.org", grantOffer{length: forThisTurn})
	web := strings.Join(sectionNamed(t, m, "web").Lines, "\n")
	for _, want := range []string{
		"pkg.go.dev, that host alone — " + endsWithSession,
		"proxy.golang.org, that host alone — " + endsWithTurn,
	} {
		if !strings.Contains(web, want) {
			t.Errorf("the web section does not say %q:\n%s", want, web)
		}
	}
}

// The workspace repository's store is on the sensitive list, as the working
// scope classifies it, and only where there is a repository to have one.
func TestSafety_TheRepositoryStoreIsSensitiveWhereThereIsOne(t *testing.T) {
	const words = "the repository's store and hooks"
	// A session's own temporary directory sits under the home directory and
	// inside shhh's state directory, which would make the repository a child of
	// both: its store would be classified as shhh's own state before it is read
	// as a repository's, and drawn as ~/… where the test spells it out. Home and
	// shhh's directories move off it, the way the scope command's test moves them.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	if where := strings.Join(sectionNamed(t, safetyModel(t), "where it may write").Lines, "\n"); strings.Contains(where, words) {
		t.Errorf("a workspace in no repository names a store:\n%s", where)
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sc, errs := scope.New(repo)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	store, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if class, _ := scope.Classify(store); class != scope.Sensitive {
		t.Fatalf("the working scope does not classify %s sensitive", store)
	}
	where := strings.Join(sectionNamed(t, safetyModel(t).WithScope(sc), "where it may write").Lines, "\n")
	if !strings.Contains(where, store+" — "+words) {
		t.Errorf("the sensitive list does not name %s:\n%s", store, where)
	}
}

// The route, through the real program: /safety reaches the screen, and a
// directory added and a mode changed while it was closed are on it when it
// is opened again — the proof that it reads the session, not a copy.
func TestProgram_TheSafetyReadingReadsTheLiveSession(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing"})
	sc, errs := scope.New(t.TempDir())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	extra := filepath.Join(t.TempDir(), "shared-assets")
	m = m.WithScope(sc).WithApprovalMode(agent.ModeManual, nil).
		WithContainment(Containment{Status: "unconfined — none here", Detail: "none here"})
	tm := runProgramAt(t, m, 130, 140)

	send(tm, "/safety")
	waitForText(t, tm, "/safety · manual · unconfined")
	waitForText(t, tm, "changed with /add-dir")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})

	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	send(tm, "/add-dir "+extra)
	waitForText(t, tm, "Added ")
	send(tm, "/permissions accept-edits")
	waitForText(t, tm, "mode set to accept-edits")

	send(tm, "/security")
	waitForText(t, tm, "/safety · accept-edits · unconfined")
	waitForText(t, tm, "shared-assets")
	frameHas(t, finalFrame(t, tm), "accept-edits", "shared-assets", "[q] back")
}

// The same route in a conversation: the screen opens, and says the session
// has one mode rather than drawing a mode picker's word.
func TestProgram_AConversationsSafetyReadingHasNoMode(t *testing.T) {
	m, _ := scriptedSession(programTurn{text: "nothing"})
	m = m.WithConversation()
	tm := runProgramAt(t, m, 130, 140)

	send(tm, "/safety")
	waitForText(t, tm, "/safety · conversation · read-only")
	waitForText(t, tm, "A conversation has one mode")
	frameHas(t, finalFrame(t, tm), "a conversation runs no commands")
}
