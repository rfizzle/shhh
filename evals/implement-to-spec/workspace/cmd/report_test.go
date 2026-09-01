package cmd

import (
	"strings"
	"testing"

	"example.com/ledger/store"
)

// The flag has to reach the budget: entries past the ceiling are left out of
// the report rather than printed anyway.
func TestReportHonoursTheBudgetFlag(t *testing.T) {
	entries := []store.Entry{{Label: "coffee", Cents: 350}, {Label: "lunch", Cents: 900}, {Label: "bun", Cents: 275}}

	var out strings.Builder
	if err := Report(&out, []string{"-budget", "700"}, entries); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "coffee") {
		t.Errorf("an entry inside the budget should be reported:\n%s", got)
	}
	if strings.Contains(got, "lunch") {
		t.Errorf("an entry that breaches the budget should be left out:\n%s", got)
	}
	if !strings.Contains(got, "bun") {
		t.Errorf("a later entry that still fits should be reported:\n%s", got)
	}
}

func TestReportWithoutABudgetPrintsEverything(t *testing.T) {
	entries := []store.Entry{{Label: "a", Cents: 1}, {Label: "b", Cents: 1 << 40}}
	var out strings.Builder
	if err := Report(&out, nil, entries); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a", "b"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no budget means no filtering, %q missing:\n%s", want, out.String())
		}
	}
}
