package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func reportsFixtures(now time.Time) []reports.Entry {
	return []reports.Entry{
		{ID: "rp-8f3a11c04b2d9e61", Meta: reports.Meta{
			Title: "suite timing breakdown", Project: "/home/u/proj", Origin: "code",
			Created: now.Add(-90 * time.Second), Size: 11432}},
		{ID: "rp-02b7c44d9aa10e5f", Meta: reports.Meta{
			Title: "token spend by model", Project: "/home/u/proj", Origin: "chat",
			Created: now.Add(-26 * time.Hour), Size: 8032}},
		{ID: "rp-77d1e0a2b93c48f6", Meta: reports.Meta{
			Title: "provider latency", Project: "/home/u/other", Origin: "print",
			Created: now.Add(-3 * 24 * time.Hour), Size: 5120}},
	}
}

func TestReportsReport_Golden(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	assertReportGolden(t, "reports", reportsReport(reportsFixtures(now)[:2], false, now).Render(80))
	assertReportGolden(t, "reports.all", reportsReport(reportsFixtures(now), true, now).Render(80))
	assertReportGolden(t, "reports.empty", reportsReport(nil, false, now).Render(80))
}

// The listing names ids because the id is what `open` takes; when it crosses
// projects the project stands where the origin stood.
func TestReportsReport_Rows(t *testing.T) {
	now := time.Now()
	text := reportsReport(reportsFixtures(now)[:1], false, now).Render(120)
	for _, want := range []string{"rp-8f3a11c04b2d9e61", "suite timing breakdown", "code", "1m ago"} {
		if !strings.Contains(text, want) {
			t.Fatalf("listing missing %q:\n%s", want, text)
		}
	}
	all := reportsReport(reportsFixtures(now), true, now).Render(120)
	if !strings.Contains(all, "/home/u/other") {
		t.Fatalf("--all listing does not name the project:\n%s", all)
	}
}

// --json emits the stored fact, never the listing's presentation shape.
func TestReportsJSON_DomainShape(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(reportsJSON(reportsFixtures(now)[:1]))
	if err != nil {
		t.Fatal(err)
	}
	var back []map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "title", "project", "origin", "created", "size"} {
		if _, ok := back[0][key]; !ok {
			t.Fatalf("json row missing %q: %s", key, data)
		}
	}
	if len(back[0]) != 6 {
		t.Fatalf("json row grew a presentation field: %s", data)
	}
}

func TestFilterProject(t *testing.T) {
	now := time.Now()
	got := filterProject(reportsFixtures(now), "/home/u/proj")
	if len(got) != 2 {
		t.Fatalf("filterProject kept %d of 3, want 2", len(got))
	}
	if len(filterProject(reportsFixtures(now), "/nowhere")) != 0 {
		t.Fatal("filterProject matched a project no report names")
	}
}

// The doctor row and the listing must name the same directory — a check that
// reports a path the reader cannot list is impossible by construction.
func TestTheDoctorNamesTheStoreTheListingOpens(t *testing.T) {
	dir, err := reportsDir()
	if err != nil {
		t.Skipf("no state dir on this machine: %v", err)
	}
	if !strings.HasSuffix(dir, "reports") {
		t.Fatalf("reportsDir = %q, want the reports directory under the state dir", dir)
	}
	f := doctorReports(dir, 2, 19464, nil)
	if !strings.Contains(f.Subject, shortPath(dir)) {
		t.Fatalf("the doctor names %q, the listing opens %q", f.Subject, dir)
	}
}

func TestDoctorReports(t *testing.T) {
	ok := doctorReports("/home/u/.local/share/shhh/reports", 2, 19464, nil)
	if ok.State != components.DoctorPassed {
		t.Fatalf("a readable store did not pass: %+v", ok)
	}
	for _, want := range []string{"2 reports", "19 kB"} {
		if !strings.Contains(ok.Detail, want) {
			t.Fatalf("detail missing %q: %q", want, ok.Detail)
		}
	}

	fresh := doctorReports("/home/u/.local/share/shhh/reports", 0, 0, nil)
	if fresh.State != components.DoctorPassed || fresh.Detail != "nothing recorded yet" {
		t.Fatalf("a fresh install reads as a fault: %+v", fresh)
	}

	broken := doctorReports("/home/u/.local/share/shhh/reports", 0, 0, errors.New("index unreadable"))
	if broken.State != components.DoctorWarned {
		t.Fatalf("an unreadable store did not warn: %+v", broken)
	}
	if !strings.Contains(broken.Consequence, "report tool") {
		t.Fatalf("the consequence does not say what stops working: %q", broken.Consequence)
	}
}
