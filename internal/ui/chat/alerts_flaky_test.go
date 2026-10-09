package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// flakyNow is the held clock every flaky-alert test reads the ledger against.
var flakyNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// holdFlakyClock pins clock to flakyNow for the test.
func holdFlakyClock(t *testing.T) {
	t.Helper()
	was := clock
	clock = func() time.Time { return flakyNow }
	t.Cleanup(func() { clock = was })
}

// ledgerOf is a gate reading the given rows as the checkout's ledger.
func ledgerOf(count, days int, flakes ...storage.Flake) Gate {
	return Gate{
		Manage:          func([]string) string { return "" },
		Flakes:          func() ([]storage.Flake, error) { return flakes, nil },
		FlakeAlertCount: count, FlakeAlertDays: days,
	}
}

// flake is a ledger row for check, seen times, first and last that long
// before flakyNow.
func flake(check string, seen int, first, last time.Duration) storage.Flake {
	return storage.Flake{Suite: "default", Check: check, Command: "./checks/" + check + ".sh",
		Seen: seen, FirstExit: 1, FirstAt: flakyNow.Add(-first), LastAt: flakyNow.Add(-last)}
}

const day = 24 * time.Hour

// A check that has flaked often enough, recently enough, stands under ALERTS
// as `~` and the word flaked; one below the count, or one whose latest flake
// is a whole window old, is settled and not handed to the block at all. It
// never supersedes a failure: the failing command stays standing beside it,
// a gate that passes leaves it standing (a flake is a pass), and it goes
// ahead of the failures so a failure always takes a drawn row first.
func TestAlerts_AFlakyCheckStands(t *testing.T) {
	holdFlakyClock(t)
	tests := []struct {
		name       string
		gate       Gate
		transcript []entry
		want       []components.InspectorAlert
	}{
		{
			name: "three flakes this week stand",
			gate: ledgerOf(0, 0, flake("go test", 3, 3*day, time.Hour)),
			want: []components.InspectorAlert{{Label: "go test", Note: "flaked 3× this week", Flaky: true}},
		},
		{
			name: "two flakes are a busy machine",
			gate: ledgerOf(0, 0, flake("go test", 2, 3*day, time.Hour)),
		},
		{
			name: "a week without a flake settles it",
			gate: ledgerOf(0, 0, flake("go test", 5, 20*day, 7*day)),
		},
		{
			name: "a flake just inside the week keeps it standing",
			gate: ledgerOf(0, 0, flake("go test", 3, 6*day, 7*day-time.Minute)),
			want: []components.InspectorAlert{{Label: "go test", Note: "flaked 3× this week", Flaky: true}},
		},
		{
			name: "a count older than the week is not said as this week's",
			gate: ledgerOf(0, 0, flake("vet", 9, 40*day, 2*day)),
			want: []components.InspectorAlert{{Label: "vet", Note: "flaked 9× · again this week", Flaky: true}},
		},
		{
			name: "the settings move the count and the window",
			gate: ledgerOf(2, 3, flake("vet", 2, 2*day, day), flake("lint", 4, 5*day, 4*day)),
			want: []components.InspectorAlert{{Label: "vet", Note: "flaked 2× in the last 3 days", Flaky: true}},
		},
		{
			name: "a failure stands beside it and after it",
			gate: ledgerOf(0, 0, flake("vet", 3, 2*day, time.Hour)),
			transcript: []entry{
				{kind: entryCommand, text: "go build ./...", exitCode: 2, turn: 1},
			},
			want: []components.InspectorAlert{
				{Label: "vet", Note: "flaked 3× this week", Flaky: true},
				{Label: "go build", Note: components.OutcomeExit(2), Runs: 1, Turn: 1, Turns: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := frameModel(t, 130, 40)
			m.wiring.Gate, m.alertMemo = tt.gate, &alertMemo{}
			for _, e := range tt.transcript {
				m.appendEntry(e)
			}
			got := m.inspectorAlerts()
			if len(got) != len(tt.want) {
				t.Fatalf("want %d alerts, got %+v", len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("alert %d: want %+v, got %+v", i, tt.want[i], got[i])
				}
			}
		})
	}
}

// The suite passing over the tree answers the failing command and leaves the
// flaky check standing: the pass is the gate's, and every flake was one.
func TestAlerts_AGatePassLeavesAFlakyCheckStanding(t *testing.T) {
	holdFlakyClock(t)
	m := frameModel(t, 130, 40)
	m.wiring.Gate, m.alertMemo = ledgerOf(0, 0, flake("vet", 3, 2*day, time.Hour)), &alertMemo{}
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2, turn: 1})
	appendGateText(&m, gateResult("PASS", 2, 2))

	alerts := m.inspectorAlerts()
	live := alerts.Live()
	if len(live) != 1 || !live[0].Flaky || live[0].Label != "vet" {
		t.Fatalf("the flaky check is the one alert left standing, got %+v", live)
	}
	if len(alerts) != 2 || !alerts[1].Superseded || alerts[1].Flaky {
		t.Fatalf("the failure is answered by the pass and not by the flake, got %+v", alerts)
	}
}

// The ledger is read once, and again only when a gate verdict lands — the one
// thing in a session that writes to it — however many rows land between.
func TestAlerts_TheLedgerIsReadAgainWhenAVerdictLands(t *testing.T) {
	holdFlakyClock(t)
	reads := 0
	seen := 2
	m := frameModel(t, 130, 40)
	m.wiring.Gate, m.alertMemo = Gate{
		Manage: func([]string) string { return "" },
		Flakes: func() ([]storage.Flake, error) {
			reads++
			return []storage.Flake{flake("vet", seen, 2*day, time.Hour)}, nil
		},
	}, &alertMemo{}
	if live := m.inspectorAlerts().Live(); len(live) != 0 {
		t.Fatalf("two flakes stand nothing, got %+v", live)
	}
	m.appendEntry(entry{kind: entryCommand, text: "ls", turn: 1})
	m.inspectorAlerts()
	if reads != 1 {
		t.Fatalf("a row that is not a verdict read the ledger again: %d reads", reads)
	}
	seen = 3
	appendGateText(&m, gateResult("PASS", 2, 2))
	live := m.inspectorAlerts().Live()
	if reads != 2 {
		t.Fatalf("a landed verdict should read the ledger again: %d reads", reads)
	}
	if len(live) != 1 || live[0].Note != "flaked 3× this week" {
		t.Fatalf("the third flake stands the check, got %+v", live)
	}
}

// A first read of an empty session still reads the ledger: a check that keeps
// flaking is news before this session has run anything.
func TestAlerts_AnEmptySessionStillStandsAFlakyCheck(t *testing.T) {
	holdFlakyClock(t)
	m := New(nil, mockStream, Wiring{Gate: ledgerOf(0, 0, flake("vet", 4, 2*day, time.Hour))})
	if live := m.inspectorAlerts().Live(); len(live) != 1 || !live[0].Flaky {
		t.Fatalf("an empty session should stand the flaky check, got %+v", live)
	}
}

// The model is told nothing by the rail: the session's reading of its own
// bad news leaves the flaky check out, because the gate's result already said
// the check flaked and how often.
func TestSummaryAlerts_LeavesAFlakyCheckOut(t *testing.T) {
	holdFlakyClock(t)
	m := frameModel(t, 130, 40)
	m.wiring.Gate, m.alertMemo = ledgerOf(0, 0, flake("vet", 3, 2*day, time.Hour)), &alertMemo{}
	if got := m.summaryAlerts(); got != nil {
		t.Fatalf("a flaky check alone is nothing for the reading, got %q", got)
	}
	m.appendEntry(entry{kind: entryCommand, text: "go build ./...", exitCode: 2, turn: 1})
	got := m.summaryAlerts()
	if len(got) != 1 || !strings.HasPrefix(got[0], "go build") {
		t.Fatalf("the reading carries the failure alone, got %q", got)
	}
}

// The alerts screen reads the same walk: the flaky check is there, marked `~`
// and `flaky`, with the ledger's account and no runs.
func TestAlertsScreen_ListsAFlakyCheck(t *testing.T) {
	holdFlakyClock(t)
	m := frameModel(t, 130, 40)
	m.wiring.Gate, m.alertMemo = ledgerOf(0, 0, flake("vet", 3, 2*day, time.Hour)), &alertMemo{}
	screen := m.alertsScreenData()
	if len(screen.Alerts) != 1 || !screen.Alerts[0].Alert.Flaky || len(screen.Alerts[0].Runs) != 0 {
		t.Fatalf("the screen should list the flaky check alone, got %+v", screen.Alerts)
	}
	view := stripANSI(screen.View(130))
	for _, want := range []string{"~ vet", "flaky", "flaked 3× this week"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen is missing %q:\n%s", want, view)
		}
	}
}
