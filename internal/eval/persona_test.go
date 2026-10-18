package eval

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustPersona(t *testing.T, turns int, lines ...string) *persona {
	t.Helper()
	p, err := parsePersona(lines, turns, CaseFile)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The first rule whose expression matches answers, whatever comes after it,
// and each event is matched only by its own rules.
func TestPersonaFirstMatchWins(t *testing.T) {
	p := mustPersona(t, 3,
		"question (?i)which (file|package) => internal/calc",
		"question .* => no preference, you decide",
		"approval git_write => deny",
		"approval .* => allow",
		"close (?i)\\bdone\\b => also add a test for the empty case",
		"close .* => stop",
	)

	if _, note, scripted := p.answer("Which package holds the Sum?", nil); note != "internal/calc" || !scripted {
		t.Errorf("answer = %q (scripted %v), want the first rule's", note, scripted)
	}
	if _, note, _ := p.answer("Should I add a test?", nil); note != "no preference, you decide" {
		t.Errorf("answer = %q, want the catch-all's", note)
	}
	if d, scripted := p.approve("git_write"); d != "deny" || !scripted {
		t.Errorf("approve(git_write) = %q, %v", d, scripted)
	}
	if d, _ := p.approve("edit_file"); d != "allow" {
		t.Errorf("approve(edit_file) = %q", d)
	}
	if prompt, more := p.next("Done: Sum fixed.", 1); !more || prompt != "also add a test for the empty case" {
		t.Errorf("next = %q, %v", prompt, more)
	}
	if _, more := p.next("I could not find it.", 1); more {
		t.Error("a close matched by stop must end the conversation")
	}
}

// An answer that is one of the offered labels is a pick from the card; any
// other is the person's own words.
func TestPersonaPicksAnOfferedLabelOrAnswersInItsOwnWords(t *testing.T) {
	p := mustPersona(t, 1, "question (?i)which => Calc", "question (?i)name => total")
	picked, note, _ := p.answer("Which one?", []string{"stats", "calc"})
	if !slices.Equal(picked, []string{"calc"}) || note != "" {
		t.Errorf("picked %v, note %q: want the label the answer names", picked, note)
	}
	picked, note, _ = p.answer("What name?", []string{"sum", "add"})
	if picked != nil || note != "total" {
		t.Errorf("picked %v, note %q: want the words, not a label the script never wrote", picked, note)
	}
}

// What nothing matched has a fixed answer: a question is "no preference" and
// counted, an approval is allowed, a close ends the conversation.
func TestPersonaAnswersWhatNoRuleMatches(t *testing.T) {
	p := mustPersona(t, 3, "close never => more")
	if picked, note, scripted := p.answer("Which?", []string{"a", "b"}); picked != nil || note != noPreference || scripted {
		t.Errorf("unmatched question = %v %q scripted %v", picked, note, scripted)
	}
	if d, scripted := p.approve("execute_command"); d != "allow" || scripted {
		t.Errorf("unmatched approval = %q scripted %v", d, scripted)
	}
	if _, more := p.next("done", 1); more {
		t.Error("an unmatched close must stop")
	}
}

// The ceiling counts the first prompt, and ends the conversation whatever the
// script would have said next.
func TestPersonaStopsAtTheTurnCeiling(t *testing.T) {
	p := mustPersona(t, 2, "close .* => keep going")
	if _, more := p.next("done", 1); !more {
		t.Fatal("below the ceiling the script speaks")
	}
	if _, more := p.next("done", 2); more {
		t.Error("at the ceiling the conversation ends")
	}
}

// The persona can be handed the question, the labels, the tool name and the
// final text, and nothing else: every argument of the asking side is a string,
// a list of strings or a count, so a trace, a tool result, the workspace, the
// check and the facts have no type to arrive in.
func TestPersonaSeesOnlyTheWire(t *testing.T) {
	allowed := map[reflect.Type]bool{
		reflect.TypeOf(""):         true,
		reflect.TypeOf([]string{}): true,
		reflect.TypeOf(0):          true,
	}
	iface := reflect.TypeOf((*asker)(nil)).Elem()
	if iface.NumMethod() != 3 {
		t.Fatalf("the asking side has %d methods, want answer, approve and next", iface.NumMethod())
	}
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		for j := 0; j < m.Type.NumIn(); j++ {
			if in := m.Type.In(j); !allowed[in] {
				t.Errorf("%s takes a %s: the persona may read only what the wire shows", m.Name, in)
			}
		}
	}
	// And the script itself holds rules and a ceiling, so nothing it was
	// loaded with can carry a session's content into an answer.
	pt := reflect.TypeOf(persona{})
	for i := 0; i < pt.NumField(); i++ {
		if f := pt.Field(i); f.Name != "rules" && f.Name != "turns" {
			t.Errorf("persona holds %s", f.Name)
		}
	}
	var _ asker = (*persona)(nil)

	// The same line put next to text it would match in a tool result does
	// not fire: the only text a question rule is matched against is the
	// question.
	p := mustPersona(t, 1, "question SECRET => leaked")
	if _, note, _ := p.answer("Which file?", []string{"a.go", "b.go"}); note == "leaked" {
		t.Error("a rule fired on text the question does not hold")
	}
}

func TestLoadRefusesAPersonaLineWithoutAnArrow(t *testing.T) {
	for _, tc := range []struct{ lines, want string }{
		{`"question which file internal/calc"`, "no arrow"},
		{`"ask .* => yes"`, "begins with"},
		{`"question ( => yes"`, "does not compile"},
		{`"approval .* => maybe"`, "allow or deny"},
		{`"question .* => "`, "answer is empty"},
		{`"close  => stop"`, "expression is empty"},
	} {
		dir := writeCase(t, t.TempDir(), "served", goodCase+"persona = ["+tc.lines+"]\nturns = 2\n")
		_, err := loadCase(dir)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "persona line 1") {
			t.Errorf("%s: err = %v, want a sentence naming line 1 and %q", tc.lines, err, tc.want)
		}
	}
}

func TestLoadRefusesTurnsWithoutAPersonaAndAPersonaWithoutTurns(t *testing.T) {
	for body, want := range map[string]string{
		goodCase + "turns = 2\n":                        "turns needs a persona",
		goodCase + "persona = [\"close .* => stop\"]\n": "turns of one or more",
		"kind = \"chat\"\nprompt = \"why?\"\nfacts = [\"x\"]\npersona = [\"close .* => stop\"]\nturns = 1\n": "workspace case",
	} {
		dir := writeCase(t, t.TempDir(), "served", body)
		if _, err := loadCase(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

// A served case is the one place asks can hold, so it is the one place the
// loader takes it.
func TestLoadTakesAsksOnACaseWithAPersona(t *testing.T) {
	dir := writeCase(t, t.TempDir(), "served", goodCase+"persona = [\"question .* => calc\"]\nturns = 1\nassert = [\"asks\"]\n")
	c, err := loadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.persona == nil || c.persona.turns != 1 || len(c.Assertions) != 1 || c.Assertions[0].Word != wordAsks {
		t.Errorf("persona %+v, assertions %+v", c.persona, c.Assertions)
	}
	if got := c.sessionArgv([]string{"--model", "m"}); !slices.Equal(got, []string{"serve", "--stdio", "--model", "m"}) {
		t.Errorf("argv = %v: a served case starts the server with the forwarded flags, and puts its prompt as a turn", got)
	}
}
