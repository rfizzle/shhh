package proposal

// S-114: the generation response the one-shot reads. The parse is total —
// every response is at least one command — and the section that carries the
// alternatives is optional in every direction: absent, empty, malformed, or
// spelled slightly wrong.

import (
	"strings"
	"testing"
)

func TestParse_ABareCommandIsOneChoice(t *testing.T) {
	got := Parse("lsof -nP -iTCP -sTCP:LISTEN")
	if len(got) != 1 {
		t.Fatalf("a response with no section parsed to %d choices: %+v", len(got), got)
	}
	if got[0].Command != "lsof -nP -iTCP -sTCP:LISTEN" {
		t.Errorf("the command came back as %q", got[0].Command)
	}
	if got[0].Tradeoff != "" {
		t.Errorf("a command nobody characterised carries %q", got[0].Tradeoff)
	}
}

func TestParse_MultiCommandAnswersKeepTheirLines(t *testing.T) {
	got := Parse("cd /tmp\nrm -rf build\n--- alternatives\nrm -rf /tmp/build")
	if got[0].Command != "cd /tmp\nrm -rf build" {
		t.Errorf("the multi-command answer parsed to %q", got[0].Command)
	}
	if len(got) != 2 || got[1].Command != "rm -rf /tmp/build" {
		t.Errorf("the alternative did not survive the split: %+v", got)
	}
}

func TestParse_AlternativesCarryTheirTradeoff(t *testing.T) {
	got := Parse(`lsof -nP -iTCP -sTCP:LISTEN
--- alternatives
netstat -anv -p tcp | grep LISTEN
# faster · no process names
sudo lsof -nP -iTCP -sTCP:LISTEN
# sees other users' processes · needs sudo`)

	if len(got) != 3 {
		t.Fatalf("wanted the command and two alternatives, got %+v", got)
	}
	if got[1].Command != "netstat -anv -p tcp | grep LISTEN" {
		t.Errorf("the first alternative is %q", got[1].Command)
	}
	if got[1].Tradeoff != "faster · no process names" {
		t.Errorf("the tradeoff is %q", got[1].Tradeoff)
	}
	if got[2].Tradeoff != "sees other users' processes · needs sudo" {
		t.Errorf("the second tradeoff is %q", got[2].Tradeoff)
	}
}

func TestParse_ATradeoffUnderTheSentinelIsThePrimarys(t *testing.T) {
	got := Parse("lsof -nP\n--- alternatives\n# works everywhere lsof exists\nss -ltn\n# faster · Linux only")
	if got[0].Tradeoff != "works everywhere lsof exists" {
		t.Errorf("the command with nothing above it did not take the phrase: %+v", got)
	}
	if len(got) != 2 || got[1].Tradeoff != "faster · Linux only" {
		t.Errorf("the alternative lost its own phrase: %+v", got)
	}
}

func TestParse_AnEmptySectionChangesNothing(t *testing.T) {
	got := Parse("ls -la\n--- alternatives\n")
	if len(got) != 1 || got[0].Command != "ls -la" {
		t.Errorf("a sentinel with nothing under it should leave one choice: %+v", got)
	}
}

func TestParse_TheSectionIsCapped(t *testing.T) {
	got := Parse("a\n--- alternatives\nb\nc\nd\ne\nf")
	if len(got) != MaxAlternatives+1 {
		t.Fatalf("wanted %d choices, got %d: %+v", MaxAlternatives+1, len(got), got)
	}
	if got[len(got)-1].Command != "d" {
		t.Errorf("the cap kept the wrong rows: %+v", got)
	}
}

func TestParse_TheCommandIsNotOfferedAsItsOwnAlternative(t *testing.T) {
	got := Parse("ls -la\n--- alternatives\nls -la\nls -lah")
	if len(got) != 2 {
		t.Fatalf("the repeat was kept: %+v", got)
	}
	if got[1].Command != "ls -lah" {
		t.Errorf("the wrong row survived: %+v", got)
	}
}

func TestParse_TheSentinelIsReadForgivingly(t *testing.T) {
	for _, line := range []string{"--- alternatives", "---alternatives", "--- Alternatives", "  --- alternatives  ", "----- alternatives"} {
		got := Parse("ls\n" + line + "\nls -la")
		if len(got) != 2 {
			t.Errorf("%q was not read as the sentinel: %+v", line, got)
		}
		if got[0].Command != "ls" {
			t.Errorf("%q leaked into the command: %q", line, got[0].Command)
		}
	}
}

func TestParse_ALineThatMerelyStartsWithDashesIsACommand(t *testing.T) {
	got := Parse("--- not the sentinel\nls")
	if got[0].Command != "--- not the sentinel\nls" {
		t.Errorf("a dashed line that is not the sentinel was cut: %q", got[0].Command)
	}
}

func TestCommandPart_StopsAtTheSentinel(t *testing.T) {
	if got := CommandPart("ls -la\n--- alternatives\nls -lah"); got != "ls -la" {
		t.Errorf("the streaming view would have shown %q", got)
	}
}

func TestCommandPart_HidesTheSentinelBeingTyped(t *testing.T) {
	// The section arrives token by token; a half-written sentinel must not
	// flicker under the command on its way to the picker.
	for _, partial := range []string{"ls -la\n-", "ls -la\n--", "ls -la\n---", "ls -la\n--- al"} {
		if got := CommandPart(partial); got != "ls -la" {
			t.Errorf("mid-sentinel %q rendered as %q", partial, got)
		}
	}
}

func TestCommandPart_LeavesARealCommandAlone(t *testing.T) {
	for _, cmd := range []string{"ls -la", "docker run \\\n  -it ubuntu", "cd /tmp\nrm -rf build"} {
		if got := CommandPart(cmd); got != cmd {
			t.Errorf("CommandPart(%q) = %q", cmd, got)
		}
	}
}

func TestInstructions_AskForTheFormatTheParserReads(t *testing.T) {
	got := Instructions()
	if !strings.Contains(got, Sentinel) {
		t.Errorf("the prompt does not ask for the sentinel the parser looks for:\n%s", got)
	}
	if !strings.Contains(got, tradeoffPrefix+" faster") {
		t.Errorf("the prompt does not show the tradeoff prefix:\n%s", got)
	}
	// The section has to be optional in the prompt as well as in the parser,
	// or the model pads every answer with alternatives it had to invent.
	if !strings.Contains(got, "optional") || !strings.Contains(got, "Omit it entirely") {
		t.Errorf("the prompt does not say the section may be left out:\n%s", got)
	}
}
