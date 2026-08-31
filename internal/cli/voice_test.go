package cli

// One voice for the listings: every empty state names what is absent and one
// way out you can type, and every write confirms itself as a verb and the
// thing it happened to (docs/interface/surfaces.md#outside-the-tui).

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/mcp"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/skill"
	"github.com/rfizzle/shhh/internal/todo"
)

// emptyPattern is the shape: the skipped glyph, what is absent, the
// separator, and one way out. No exclamation marks and no full stop — a way
// out is something you type, not a sentence you read.
var emptyPattern = regexp.MustCompile(`^ {2}⊘ [^·!]+ · [^!]+$`)

func TestEveryEmptyStateSpeaksTheSameWay(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		command string
		text    string
	}{
		{"shhh history", historyReport(nil, "", now).Render(200)},
		{"shhh snippets", snippetsReport(nil, now).Render(200)},
		{"shhh metrics", metricsReport(metricsData{Window: "all time"}).Render(200)},
		{"shhh observe", observeReport(observeData{Window: "30d"}).Render(200)},
		{"shhh memory", memoryReport(memory.NewStore(nil, "/repo"), nil, memoryWayOut, now).Render(200)},
		{"shhh skills", skillsReport(nil).Render(200)},
		{"shhh todo", todoReport(todo.Load(t.TempDir())).Render(200)},
		{"shhh mcp", mcpListingReport(nil, &mcp.Catalog{}, "").Render(200)},
		{"shhh chats", chatsReport(nil, now).Render(200)},
		{"shhh logs", logsEmpty("/state/shhh.log").Render(200)},
		{"shhh reports", reportsReport(nil, false, now).Render(200)},
		{"shhh rate", rateReport(nil, now).Render(200)},
		{"/secret", secretsListing(secret.New())},
		{"/sandbox scope", report.Report{Sections: []report.Section{{Rows: []report.Row{
			report.Empty("nothing added to the scope", "/add-dir <path> puts a directory in it")}}}}.Render(200)},
	} {
		line, ok := emptyLine(c.text)
		if !ok {
			t.Errorf("%s: nothing in the output is an empty state:\n%s", c.command, c.text)
			continue
		}
		if !emptyPattern.MatchString(line) {
			t.Errorf("%s: the empty state does not read `⊘ <absent> · <one way out>`: %q", c.command, line)
		}
	}
}

// A confirmation is `✓ <verb> <thing>` and says nothing else: `Configuration
// saved to …`, `Saved memory …`, `Wrote …` were four sentences for one act.
func TestEveryConfirmationSpeaksTheSameWay(t *testing.T) {
	confirmation := regexp.MustCompile(`^ {2}✓ [a-z][^·!]*$`)
	for _, row := range []report.Row{
		report.Done("saved snippet", "ports"),
		report.Done("wrote", "~/.config/shhh/config.toml"),
		report.Done("set", "provider.default = openai"),
		report.Done("forgot", "m3"),
		report.Done("deleted chat", "alpha"),
		memoryAdded(memory.Entry{ID: 3, Scope: "/repo", Kind: "preference"}).Sections[0].Rows[0],
	} {
		line := report.Report{Sections: []report.Section{{Rows: []report.Row{row}}}}.Render(200)
		if !confirmation.MatchString(strings.SplitN(line, " · ", 2)[0]) {
			t.Errorf("a confirmation does not read `✓ <verb> <thing>`: %q", line)
		}
	}
}

// skillsReport takes a catalog and must survive a nil one, which is what
// loadSkills returns on a machine with no skills at all.
func TestSkillsReport_NilCatalogIsTheEmptyState(t *testing.T) {
	if got := skillsReport(nil).Render(80); !strings.Contains(got, "⊘ no skills found") {
		t.Fatalf("nil catalog = %q", got)
	}
	if got := skillsReport(&skill.Catalog{}).Render(80); !strings.Contains(got, "⊘ no skills found") {
		t.Fatalf("empty catalog = %q", got)
	}
}

// emptyLine is the ⊘ row of a render — the empty state itself, not whatever
// a command chose to put on the lines under it.
func emptyLine(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "  ⊘ ") {
			return line, true
		}
	}
	return "", false
}
