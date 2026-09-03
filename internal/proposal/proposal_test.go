package proposal

// The generation response the one-shot reads. The parse is total —
// every response is at least one command — and both sections that follow it,
// the sentence about the command and the alternatives to it, are optional in
// every direction: absent, empty, malformed, or spelled slightly wrong.

import (
	"strings"
	"testing"
)

func TestParse_ABareCommandIsOneChoice(t *testing.T) {
	got := Parse("lsof -nP -iTCP -sTCP:LISTEN")
	if len(got.Choices) != 1 {
		t.Fatalf("a response with no section parsed to %d choices: %+v", len(got.Choices), got)
	}
	if got.Choices[0].Command != "lsof -nP -iTCP -sTCP:LISTEN" {
		t.Errorf("the command came back as %q", got.Choices[0].Command)
	}
	if got.Choices[0].Tradeoff != "" {
		t.Errorf("a command nobody characterised carries %q", got.Choices[0].Tradeoff)
	}
	// A model that answered with the command and nothing else is the reason
	// the surface still knows how to ask for the sentence on its own.
	if got.Explanation != "" {
		t.Errorf("a response that explained nothing carries %q", got.Explanation)
	}
}

func TestParse_MultiCommandAnswersKeepTheirLines(t *testing.T) {
	got := Parse("cd /tmp\nrm -rf build\n--- alternatives\nrm -rf /tmp/build")
	if got.Choices[0].Command != "cd /tmp\nrm -rf build" {
		t.Errorf("the multi-command answer parsed to %q", got.Choices[0].Command)
	}
	if len(got.Choices) != 2 || got.Choices[1].Command != "rm -rf /tmp/build" {
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

	if len(got.Choices) != 3 {
		t.Fatalf("wanted the command and two alternatives, got %+v", got)
	}
	if got.Choices[1].Command != "netstat -anv -p tcp | grep LISTEN" {
		t.Errorf("the first alternative is %q", got.Choices[1].Command)
	}
	if got.Choices[1].Tradeoff != "faster · no process names" {
		t.Errorf("the tradeoff is %q", got.Choices[1].Tradeoff)
	}
	if got.Choices[2].Tradeoff != "sees other users' processes · needs sudo" {
		t.Errorf("the second tradeoff is %q", got.Choices[2].Tradeoff)
	}
}

func TestParse_ATradeoffUnderTheSentinelIsThePrimarys(t *testing.T) {
	got := Parse("lsof -nP\n--- alternatives\n# works everywhere lsof exists\nss -ltn\n# faster · Linux only")
	if got.Choices[0].Tradeoff != "works everywhere lsof exists" {
		t.Errorf("the command with nothing above it did not take the phrase: %+v", got)
	}
	if len(got.Choices) != 2 || got.Choices[1].Tradeoff != "faster · Linux only" {
		t.Errorf("the alternative lost its own phrase: %+v", got)
	}
}

func TestParse_AnEmptySectionChangesNothing(t *testing.T) {
	got := Parse("ls -la\n--- alternatives\n")
	if len(got.Choices) != 1 || got.Choices[0].Command != "ls -la" {
		t.Errorf("a sentinel with nothing under it should leave one choice: %+v", got)
	}
}

func TestParse_TheSectionIsCapped(t *testing.T) {
	got := Parse("a\n--- alternatives\nb\nc\nd\ne\nf")
	if len(got.Choices) != MaxAlternatives+1 {
		t.Fatalf("wanted %d choices, got %d: %+v", MaxAlternatives+1, len(got.Choices), got)
	}
	if got.Choices[len(got.Choices)-1].Command != "d" {
		t.Errorf("the cap kept the wrong rows: %+v", got)
	}
}

func TestParse_TheCommandIsNotOfferedAsItsOwnAlternative(t *testing.T) {
	got := Parse("ls -la\n--- alternatives\nls -la\nls -lah")
	if len(got.Choices) != 2 {
		t.Fatalf("the repeat was kept: %+v", got)
	}
	if got.Choices[1].Command != "ls -lah" {
		t.Errorf("the wrong row survived: %+v", got)
	}
}

func TestParse_ARepeatedSentinelIsNotACommand(t *testing.T) {
	// A model that opens the same section twice has said nothing new. Read
	// as a command, the label becomes a row in the picker that runs a line
	// of dashes.
	got := Parse("ls\n--- alternatives\nls -la\n--- alternatives\nls -lah")
	if len(got.Choices) != 3 {
		t.Fatalf("the repeated label was kept as a choice: %+v", got)
	}
	for _, c := range got.Choices {
		if strings.HasPrefix(c.Command, "---") {
			t.Errorf("a sentinel reached the picker as a command: %q", c.Command)
		}
	}
}

func TestParse_ARepeatedSentinelIsNotPartOfTheSentence(t *testing.T) {
	got := Parse("ls\n--- explanation\nLists the directory.\n--- explanation\nHidden entries included.")
	if got.Explanation != "Lists the directory. Hidden entries included." {
		t.Errorf("the repeated label was read as part of the sentence: %q", got.Explanation)
	}
}

func TestParse_TheSentinelIsReadForgivingly(t *testing.T) {
	for _, line := range []string{"--- alternatives", "---alternatives", "--- Alternatives", "  --- alternatives  ", "----- alternatives"} {
		got := Parse("ls\n" + line + "\nls -la")
		if len(got.Choices) != 2 {
			t.Errorf("%q was not read as the sentinel: %+v", line, got)
		}
		if got.Choices[0].Command != "ls" {
			t.Errorf("%q leaked into the command: %q", line, got.Choices[0].Command)
		}
	}
}

func TestParse_ALineThatMerelyStartsWithDashesIsACommand(t *testing.T) {
	got := Parse("--- not the sentinel\nls")
	if got.Choices[0].Command != "--- not the sentinel\nls" {
		t.Errorf("a dashed line that is not the sentinel was cut: %q", got.Choices[0].Command)
	}
}

func TestParse_TheExplanationComesBackWithTheCommand(t *testing.T) {
	got := Parse(`lsof -nP -iTCP -sTCP:LISTEN
--- explanation
Lists every TCP socket in the listening state with the process holding it, without resolving names.
--- alternatives
ss -ltn
# faster · Linux only`)

	if got.Choices[0].Command != "lsof -nP -iTCP -sTCP:LISTEN" {
		t.Errorf("the explanation leaked into the command: %q", got.Choices[0].Command)
	}
	if !strings.HasPrefix(got.Explanation, "Lists every TCP socket") {
		t.Errorf("the explanation came back as %q", got.Explanation)
	}
	if strings.Contains(got.Explanation, "ss -ltn") {
		t.Errorf("the alternatives section ran into the explanation: %q", got.Explanation)
	}
	if len(got.Choices) != 2 || got.Choices[1].Tradeoff != "faster · Linux only" {
		t.Errorf("the section after the explanation was not read: %+v", got)
	}
}

func TestParse_AnExplanationNeedsNoAlternatives(t *testing.T) {
	got := Parse("ls -la\n--- explanation\nLists the working directory, hidden entries included.")
	if len(got.Choices) != 1 || got.Choices[0].Command != "ls -la" {
		t.Fatalf("the command did not survive on its own: %+v", got)
	}
	if got.Explanation != "Lists the working directory, hidden entries included." {
		t.Errorf("the explanation came back as %q", got.Explanation)
	}
}

func TestParse_TheExplanationIsFoldedOntoOneLine(t *testing.T) {
	// The surface has one line for it, between the command and the keys. A
	// model that answered with a paragraph still gets read; it just does not
	// get to push the decision off the screen.
	got := Parse("ls -la\n--- explanation\nLists the directory.\n\nHidden entries included.")
	if got.Explanation != "Lists the directory. Hidden entries included." {
		t.Errorf("the paragraph was not folded onto one line: %q", got.Explanation)
	}
}

func TestParse_TheExplanationSentinelIsReadForgivingly(t *testing.T) {
	for _, line := range []string{"--- explanation", "---explanation", "--- Explanation", "  --- explanation  "} {
		got := Parse("ls\n" + line + "\nLists things.")
		if got.Choices[0].Command != "ls" {
			t.Errorf("%q leaked into the command: %q", line, got.Choices[0].Command)
		}
		if got.Explanation != "Lists things." {
			t.Errorf("%q was not read as the sentinel: %q", line, got.Explanation)
		}
	}
}

func TestParse_TheSectionsAreReadInWhicheverOrderTheyArrive(t *testing.T) {
	// The prompt asks for the explanation first. A model that swaps them has
	// still said both things, and reading either section as a command is the
	// mistake worth avoiding.
	got := Parse("ls -la\n--- alternatives\nls -lah\n--- explanation\nLists the directory.")
	if got.Choices[0].Command != "ls -la" {
		t.Errorf("the command came back as %q", got.Choices[0].Command)
	}
	if got.Explanation != "Lists the directory." {
		t.Errorf("the explanation came back as %q", got.Explanation)
	}
	if len(got.Choices) != 2 || got.Choices[1].Command != "ls -lah" {
		t.Errorf("the alternative was swallowed by the explanation: %+v", got)
	}
}

func TestParse_AnEmptyExplanationChangesNothing(t *testing.T) {
	got := Parse("ls -la\n--- explanation\n")
	if len(got.Choices) != 1 || got.Choices[0].Command != "ls -la" {
		t.Errorf("a sentinel with nothing under it should leave one choice: %+v", got)
	}
	if got.Explanation != "" {
		t.Errorf("an empty section explained %q", got.Explanation)
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

func TestCommandPart_StopsAtTheExplanation(t *testing.T) {
	// The sentence is the model's own prose and it is never command text.
	// The stream is where that matters: it arrives before the alternatives
	// do, so it is the first thing that could be shown as something to run.
	if got := CommandPart("ls -la\n--- explanation\nLists the directory."); got != "ls -la" {
		t.Errorf("the streaming view would have shown %q", got)
	}
}

func TestCommandPart_HidesEitherSentinelBeingTyped(t *testing.T) {
	// Both sections arrive token by token; a half-written sentinel must not
	// flicker under the command on its way to where it belongs.
	for _, partial := range []string{"ls -la\n-", "ls -la\n--", "ls -la\n---", "ls -la\n--- ex", "ls -la\n--- explanat"} {
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
	if !strings.Contains(got, ExplainSentinel) {
		t.Errorf("the prompt does not ask for the explanation the parser looks for:\n%s", got)
	}
	// The one thing the bundled sentence has to be is short: it sits between
	// the command and the keys, and the request is where that is enforced.
	if !strings.Contains(got, "160 characters") {
		t.Errorf("the prompt does not bound the explanation:\n%s", got)
	}
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
