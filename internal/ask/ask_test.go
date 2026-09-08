package ask

import (
	"encoding/json"
	"fmt"
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

// The description is where the discipline is stated, because it is what is
// read at the moment of the call: a rewrite that drops when a question is
// worth stopping for, or what to do instead where it is not, fails here.
func TestToolDefinition_SaysWhenAQuestionIsWorthStoppingFor(t *testing.T) {
	got := ToolDefinition().Description
	for _, want := range []string{
		// When one is worth stopping for.
		"materially different work",
		// And what to do instead everywhere else.
		"already answer, do not ask",
		"state the assumption you would have asked about and carry on",
		// That the asking is bounded, so a run is not left to discover the
		// budget by spending its rounds against it.
		"only a few questions",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the description does not say %q:\n%s", want, got)
		}
	}
}

// A question the budget answered is a skip like any other in its vocabulary,
// and says why in the one field that talks about the asking rather than the
// answer — so a model that has run out of questions carries on instead of
// spending its remaining rounds asking again.
func TestOverBudget_SaysWhyNothingWasChosen(t *testing.T) {
	a := OverBudget()
	if a.Answered != AnsweredSkipped {
		t.Fatalf("answered = %q, want %q", a.Answered, AnsweredSkipped)
	}
	var got struct {
		Answered    string `json:"answered"`
		Instruction string `json:"instruction"`
		Notice      string `json:"notice"`
	}
	if err := json.Unmarshal([]byte(a.Result()), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Instruction, "state the assumption") {
		t.Errorf("instruction = %q", got.Instruction)
	}
	for _, want := range []string{fmt.Sprintf("%d questions", PerTurnBudget), "not put to anybody"} {
		if !strings.Contains(got.Notice, want) {
			t.Errorf("the notice does not say %q: %q", want, got.Notice)
		}
	}
	// An ordinary skip carries no notice: nothing about the asking went
	// wrong, and a field that was always there would say nothing.
	if n := (Answer{Answered: AnsweredSkipped}).Result(); strings.Contains(n, "notice") {
		t.Errorf("a reader's own skip should carry no notice: %s", n)
	}
}

// Two calls are the same question when they put the same words, whatever else
// they carry, and an unreadable call is no question at all.
func TestQuestionText_IsTheWholeOfWhatMakesTwoCallsOneQuestion(t *testing.T) {
	one := QuestionText(json.RawMessage(`{"question":"  Which store? ","shape":"choose","options":[{"label":"A"}]}`))
	two := QuestionText(json.RawMessage(`{"question":"Which store?","shape":"text"}`))
	if one != "Which store?" || one != two {
		t.Errorf("got %q and %q", one, two)
	}
	if got := QuestionText(json.RawMessage(`not json`)); got != "" {
		t.Errorf("an unreadable call is no question, got %q", got)
	}
}

// A call may carry several questions, and they come back as a list in the
// order they were sent.
func TestParse_ReadsSeveralQuestionsInSendOrder(t *testing.T) {
	qs, err := Parse(json.RawMessage(`{"questions":[
		{"question":"Which store?","shape":"choose","options":[{"label":"A"},{"label":"B"}]},
		{"question":"Reversible?","shape":"confirm"},
		{"question":"Call it what?","shape":"text","note":"required"}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"Which store?", "Reversible?", "Call it what?"}
	if len(qs) != len(want) {
		t.Fatalf("questions = %d, want %d", len(qs), len(want))
	}
	for i, w := range want {
		if qs[i].Question != w {
			t.Errorf("question %d = %q, want %q", i+1, qs[i].Question, w)
		}
	}
	if qs[0].Shape != ShapeChoose || qs[1].Shape != ShapeConfirm || qs[2].Note != NoteRequired {
		t.Errorf("every question keeps its own shape and note: %+v", qs)
	}
}

// The limit is the card's, and the refusal names it rather than leaving the
// model to guess how many it may have.
func TestParse_RefusesAFifthQuestion(t *testing.T) {
	var call strings.Builder
	call.WriteString(`{"questions":[`)
	for i := 0; i < MaxQuestions+1; i++ {
		if i > 0 {
			call.WriteString(",")
		}
		fmt.Fprintf(&call, `{"question":"q%d","shape":"confirm"}`, i)
	}
	call.WriteString(`]}`)
	refused(t, call.String(), "too many questions", fmt.Sprintf("max %d", MaxQuestions))
}

// One question or a list of them, never both: a call that wrote both would
// have to be told which half the reader saw.
func TestParse_RefusesOneQuestionAndAListAtOnce(t *testing.T) {
	refused(t, `{"question":"Which store?","shape":"confirm","questions":[{"question":"Reversible?","shape":"confirm"}]}`,
		"not both", "questions")
	// A question inside the list is held to every rule one on its own is,
	// and the refusal says which of them it was.
	refused(t, `{"questions":[{"question":"ok","shape":"confirm"},{"question":"bad","shape":"rank"}]}`,
		"question 2", `unknown shape "rank"`)
}

// The answers to a call that asked several name their own question, so a
// model reading them back never has to match on position alone.
func TestReply_NamesEachQuestionInSendOrder(t *testing.T) {
	qs := []Question{{Question: "Which store?"}, {Question: "Reversible?"}, {Question: "Call it what?"}}
	as := []Answer{
		{Answered: AnsweredOnCard, Picked: []string{"SQLite"}},
		{Answered: AnsweredSkipped},
		{Answered: AnsweredTyped, Note: "cacheStore"},
	}
	var got []struct {
		Ask         string   `json:"ask"`
		Answered    string   `json:"answered"`
		Picked      []string `json:"picked"`
		Note        string   `json:"note"`
		Instruction string   `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(Reply(qs, as)), &got); err != nil {
		t.Fatalf("the reply to a call that asked several is a list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("answers = %d, want 3", len(got))
	}
	for i, q := range qs {
		if got[i].Ask != q.Question {
			t.Errorf("answer %d names %q, want %q", i+1, got[i].Ask, q.Question)
		}
	}
	if got[0].Picked[0] != "SQLite" || got[2].Note != "cacheStore" {
		t.Errorf("the answers did not survive in order: %+v", got)
	}
	if got[1].Instruction == "" {
		t.Error("a skipped question still says what to do about it")
	}
	// A question the run never reached is the reader's absence rather than a
	// silence to interpret.
	short := Reply(qs, as[:1])
	if !strings.Contains(short, string(AnsweredNobody)) {
		t.Errorf("a run that collected two answers short says so: %s", short)
	}
}

// A call that asked one question keeps the shape it has always had, byte for
// byte: the result's shape is a fact about the call.
func TestReply_LeavesOneQuestionAlone(t *testing.T) {
	a := Answer{Answered: AnsweredOnCard, Picked: []string{"SQLite"}}
	if got, want := Reply([]Question{{Question: "Which store?"}}, []Answer{a}), a.Result(); got != want {
		t.Errorf("Reply = %s, want %s", got, want)
	}
	if strings.Contains(a.Result(), `"ask"`) {
		t.Errorf("a lone answer names no question: %s", a.Result())
	}
}

// The repeat detector keys an ask on what it asked, and a call that asked two
// things is the same asking only when both match.
func TestQuestionText_ReadsAWholeListOfQuestions(t *testing.T) {
	const two = `{"questions":[{"question":"Which store?","shape":"confirm"},{"question":"Reversible?","shape":"confirm"}]}`
	const twoAgain = `{"questions":[{"question":" Which store? ","shape":"text"},{"question":"Reversible?","shape":"text"}]}`
	const shared = `{"questions":[{"question":"Which store?","shape":"confirm"},{"question":"Call it what?","shape":"confirm"}]}`
	if QuestionText(json.RawMessage(two)) != QuestionText(json.RawMessage(twoAgain)) {
		t.Error("the same two questions asked again are the same asking")
	}
	if QuestionText(json.RawMessage(two)) == QuestionText(json.RawMessage(shared)) {
		t.Error("two calls that share one question out of two are not one asking")
	}
	if QuestionText(json.RawMessage(two)) == "" {
		t.Error("a call that asked a list is still an asking")
	}
}
