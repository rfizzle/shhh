package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/project"
)

// agentsProject is a repository carrying one profile of its own.
func agentsProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, ".shhh", "agents"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".shhh", "agents", "auditor.toml"),
		[]byte("description = \"reads a change for the licences it pulls in\"\npermissions = [\"read\"]\n"), 0o644))
	return root
}

// A trusted checkout's profile is listed beside the three built-in roles,
// each row naming where it came from.
func TestAgentsListsTheProjectProfileBesideTheBuiltIns(t *testing.T) {
	root := agentsProject(t)
	withProjectTrust(t, project.Trust{Root: root, Granted: true, Present: []project.Kind{project.KindAgents}})

	r, err := agentsListing(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sections) != 1 {
		t.Fatalf("sections = %+v", r.Sections)
	}
	want := map[string]string{
		"auditor":    "project",
		"researcher": "built-in",
		"reviewer":   "built-in",
		"writer":     "built-in",
	}
	rows := r.Sections[0].Rows
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want the four roles", rows)
	}
	for _, row := range rows {
		if scope, ok := want[row.Name]; !ok || row.Outcome != scope {
			t.Errorf("row %q scope %q, want %q", row.Name, row.Outcome, scope)
		}
		if row.Subject == "" {
			t.Errorf("row %q has no description", row.Name)
		}
	}
	if len(r.Notes) != 0 {
		t.Errorf("a trusted checkout carries no note: %+v", r.Notes)
	}
	assertReportGolden(t, "agents", r.String())
}

// An untrusted checkout's profile is withheld the way its skills are, and the
// listing says so rather than looking as though the repository wrote none.
func TestAgentsWithholdsAnUntrustedProjectProfile(t *testing.T) {
	root := agentsProject(t)
	withProjectTrust(t, project.Trust{Root: root, Present: []project.Kind{project.KindAgents}})

	r, err := agentsListing(root)
	if err != nil {
		t.Fatal(err)
	}
	got := r.String()
	if strings.Contains(got, "auditor") {
		t.Errorf("an untrusted checkout's profile was listed:\n%s", got)
	}
	if !strings.Contains(got, "withheld until you trust it: `shhh trust`") {
		t.Errorf("the listing does not say what was withheld:\n%s", got)
	}
	if !strings.Contains(got, "3 roles") {
		t.Errorf("want the three built-ins:\n%s", got)
	}
	assertReportGolden(t, "agents.withheld", got)
}

func TestAgentsEmptyState(t *testing.T) {
	got := agentsReport(nil, false).String()
	if !strings.Contains(got, "⊘ no roles found") {
		t.Errorf("empty listing: %q", got)
	}
}
