package ask

import (
	"encoding/json"
	"strings"
	"testing"
)

func parse(t *testing.T, raw string) Question {
	t.Helper()
	qs, err := Parse(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Parse(%s): %v", raw, err)
	}
	if len(qs) != 1 {
		t.Fatalf("one call carries one question today, got %d", len(qs))
	}
	return qs[0]
}

func refused(t *testing.T, raw string, want ...string) {
	t.Helper()
	_, err := Parse(json.RawMessage(raw))
	if err == nil {
		t.Fatalf("Parse(%s) should have been refused", raw)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not name %q: %v", w, err)
		}
	}
}

func TestParse_ReadsAChoice(t *testing.T) {
	q := parse(t, `{"question":"Which store?","shape":"choose","options":[
		{"label":"Postgres","detail":"one more service","field":"3 files"},
		{"label":"SQLite","recommended":true},
		{"label":"Redis","unavailable":"no client in this project"}]}`)
	if q.Shape != ShapeChoose {
		t.Errorf("shape = %q", q.Shape)
	}
	if q.Note != NoteOptional {
		t.Errorf("a question that said nothing about a note offers one: %q", q.Note)
	}
	if len(q.Options) != 3 {
		t.Fatalf("options = %d", len(q.Options))
	}
	if q.Options[0].Detail != "one more service" || q.Options[0].Field != "3 files" {
		t.Errorf("the option's own fields did not survive: %+v", q.Options[0])
	}
	if !q.Options[1].Recommended {
		t.Error("the recommendation did not survive")
	}
	if q.Options[2].Unavailable != "no client in this project" {
		t.Errorf("the reason a row cannot be taken did not survive: %+v", q.Options[2])
	}
}

// An unknown shape names the four, the way an unknown memory kind names its
// own set: a model told only "invalid" writes the same call again.
func TestParse_RefusesAnUnknownShape(t *testing.T) {
	refused(t, `{"question":"?","shape":"rank","options":[{"label":"a"},{"label":"b"}]}`,
		`unknown shape "rank"`, "choose", "choose_many", "confirm", "text")
}

func TestParse_RefusesWhatACardCannotDraw(t *testing.T) {
	refused(t, `{"shape":"text"}`, "question is required")
	refused(t, `{"question":"?","shape":"choose","options":[{"label":"only one"}]}`,
		"at least two options")
	refused(t, `{"question":"?","shape":"confirm","options":[{"label":"a"},{"label":"b"}]}`,
		"takes no options")
	refused(t, `{"question":"?","shape":"choose","options":[{"label":"a"},{"label":"a"}]}`,
		"share the label")
	refused(t, `{"question":"?","shape":"choose","options":[{"label":"a"},{"label":""}]}`,
		"needs a label")
	refused(t, `{"question":"?","shape":"choose","note":"maybe","options":[{"label":"a"},{"label":"b"}]}`,
		`unknown note "maybe"`)
	refused(t, `{"question":"?","shape":"choose","options":[
		{"label":"a","recommended":true},{"label":"b","recommended":true}]}`,
		"a recommendation is one answer")
	refused(t, `{"question":"?","shape":"choose","options":[
		{"label":"a","recommended":true,"unavailable":"no client here"},{"label":"b"}]}`,
		"recommended and unavailable")
	refused(t, `{"question":"`+strings.Repeat("x", MaxQuestionLen+1)+`","shape":"text"}`,
		"too long")
	long := `{"question":"?","shape":"choose","options":[`
	for i := 0; i <= MaxOptions; i++ {
		if i > 0 {
			long += ","
		}
		long += `{"label":"opt` + string(rune('a'+i)) + `"}`
	}
	refused(t, long+`]}`, "too many options")
}

// The caps are about what fits on a card, so they count characters. Counted
// in bytes, a question in a script that is not Latin would be refused at a
// third of its stated length, and the number the model is handed back would
// be in units the message does not name.
func TestParse_TheCapsAreCountedInCharacters(t *testing.T) {
	q := parse(t, `{"question":"`+strings.Repeat("問", MaxQuestionLen)+`","shape":"text"}`)
	if got := len([]rune(q.Question)); got != MaxQuestionLen {
		t.Fatalf("a question of exactly the cap was refused or trimmed: %d runes", got)
	}
	refused(t, `{"question":"`+strings.Repeat("問", MaxQuestionLen+1)+`","shape":"text"}`, "too long")
}

// The result is what the model reads, so an empty note is an empty string and
// a pick that was not made is an empty list: neither is a field the model has
// to work out the absence of.
func TestAnswer_ResultStatesEveryField(t *testing.T) {
	var got struct {
		Answered    string   `json:"answered"`
		Picked      []string `json:"picked"`
		Note        string   `json:"note"`
		Instruction string   `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(Answer{Answered: AnsweredOnCard, Picked: []string{"SQLite"}}.Result()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Answered != "on the card" || len(got.Picked) != 1 || got.Picked[0] != "SQLite" {
		t.Errorf("the pick did not travel: %+v", got)
	}
	if got.Note != "" || got.Instruction != "" {
		t.Errorf("an answered question carries no instruction and an empty note: %+v", got)
	}
	if !strings.Contains(Answer{Answered: AnsweredOnCard}.Result(), `"note":""`) {
		t.Error("an empty note must be an empty string and not a missing field")
	}
	if !strings.Contains(Answer{Answered: AnsweredOnCard}.Result(), `"picked":[]`) {
		t.Error("an empty pick must be an empty list and not null")
	}
}

// Nothing was chosen, so the model is told what to do instead of being left
// waiting on a decision that is not coming.
func TestAnswer_AnUnansweredQuestionSaysCarryOn(t *testing.T) {
	for _, a := range []Answered{AnsweredSkipped, AnsweredNobody} {
		res := Answer{Answered: a}.Result()
		if !strings.Contains(res, string(a)) {
			t.Errorf("%q: the result does not say how it was answered: %s", a, res)
		}
		if !strings.Contains(res, "state the assumption") {
			t.Errorf("%q: the result does not say to carry on: %s", a, res)
		}
	}
	if strings.Contains(Answer{Answered: AnsweredTyped, Note: "use SQLite"}.Result(), "state the assumption") {
		t.Error("a question that was answered needs no instruction")
	}
}

// The tool the model is handed offers exactly the four shapes the parse
// accepts, so a schema and a refusal can never disagree about what may be
// written.
func TestToolDefinition_OffersTheShapesTheParseTakes(t *testing.T) {
	def := ToolDefinition()
	if def.Name != ToolName {
		t.Errorf("name = %q", def.Name)
	}
	var schema struct {
		Properties struct {
			Shape struct {
				Enum []string `json:"enum"`
			} `json:"shape"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties.Shape.Enum) != len(Shapes) {
		t.Fatalf("the schema offers %d shapes and the parse takes %d", len(schema.Properties.Shape.Enum), len(Shapes))
	}
	for i, s := range Shapes {
		if schema.Properties.Shape.Enum[i] != string(s) {
			t.Errorf("shape %d: schema says %q, the parse takes %q", i, schema.Properties.Shape.Enum[i], s)
		}
	}
}

// The rules the card enforces as it collects an answer, held to by a surface
// that did not draw the card: the vocabulary is the reader's three, a pick is
// a pick, the words are the whole of a typed answer, and a note the model said
// it needs is not optional.
func TestAnswer_ValidateHoldsAnAnswerToTheCardsOwnRules(t *testing.T) {
	optional := Question{Question: "Which?", Shape: ShapeChoose, Note: NoteOptional}
	required := Question{Question: "Which?", Shape: ShapeChoose, Note: NoteRequired}
	cases := []struct {
		name string
		a    Answer
		q    Question
		want string
	}{
		{name: "a pick", a: Answer{Answered: AnsweredOnCard, Picked: []string{"one"}}, q: optional},
		{name: "words", a: Answer{Answered: AnsweredTyped, Note: "neither, really"}, q: required},
		{name: "nothing chosen", a: Answer{Answered: AnsweredSkipped}, q: required},
		{name: "an empty pick", a: Answer{Answered: AnsweredOnCard}, q: optional, want: "picked nothing"},
		{name: "words that are not there", a: Answer{Answered: AnsweredTyped, Note: "  "}, q: optional, want: "carries none"},
		{name: "a required note left off", a: Answer{Answered: AnsweredOnCard, Picked: []string{"one"}}, q: required, want: "required note"},
		{name: "the reader's absence claimed", a: Answer{Answered: AnsweredNobody}, q: optional, want: "unknown answer"},
		{name: "a word from nowhere", a: Answer{Answered: "maybe"}, q: optional, want: "unknown answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.a.Validate(tc.q)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("an answer the card would have taken was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("an answer the card would have refused stood")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
	// An unknown answer is told what it may have said instead, the way an
	// unknown shape is — and `nobody to ask` is not among the three, because
	// nothing outside the surface holding the question can report the
	// reader's absence.
	err := Answer{Answered: AnsweredNobody}.Validate(optional)
	for _, a := range ReaderAnswers {
		if !strings.Contains(err.Error(), string(a)) {
			t.Errorf("the refusal does not offer %q: %v", a, err)
		}
	}
	if strings.Contains(err.Error(), "(valid: "+string(AnsweredNobody)) ||
		strings.Contains(err.Error(), ", "+string(AnsweredNobody)) {
		t.Errorf("a client was offered the surface's own answer: %v", err)
	}
}
