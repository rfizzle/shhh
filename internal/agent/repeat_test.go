package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/structural"
)

func TestRepeatDetector_CountsIdenticalInteractions(t *testing.T) {
	d := NewRepeatDetector()
	args := json.RawMessage(`{"pattern":"foo"}`)

	if n := d.Note("search", args, "a.go:1: foo"); n != 1 {
		t.Errorf("first call should count once, got %d", n)
	}
	if n := d.Note("search", args, "a.go:1: foo"); n != 2 {
		t.Errorf("the same call again should count twice, got %d", n)
	}
	if n := d.Note("search", args, "a.go:1: foo"); n != 3 {
		t.Errorf("and again three times, got %d", n)
	}
}

func TestRepeatDetector_ChangedOutputIsNotARepeat(t *testing.T) {
	// The whole point of keying on the output too: `go test` run again after
	// a fix is a different interaction, and the one case where running the
	// same command twice is exactly right.
	d := NewRepeatDetector()
	args := json.RawMessage(`{"command":"go test ./..."}`)

	d.Note("execute_command", args, "FAIL")
	if n := d.Note("execute_command", args, "ok"); n != 1 {
		t.Errorf("a different result is a different interaction, got %d", n)
	}
}

func TestRepeatDetector_ArgumentsAreCanonicalised(t *testing.T) {
	d := NewRepeatDetector()
	d.Note("search", json.RawMessage(`{"pattern":"foo","path":"internal"}`), "out")
	n := d.Note("search", json.RawMessage(`{ "path": "internal",  "pattern": "foo" }`), "out")
	if n != 2 {
		t.Errorf("the same call written differently is still the same call, got %d", n)
	}
}

func TestRepeatDetector_WindowForgets(t *testing.T) {
	d := NewRepeatDetector()
	args := json.RawMessage(`{"pattern":"foo"}`)
	d.Note("search", args, "out")
	for i := 0; i < repeatWindow; i++ {
		d.Note("search", json.RawMessage(fmt.Sprintf(`{"pattern":"other%d"}`, i)), "out")
	}
	if n := d.Note("search", args, "out"); n != 1 {
		t.Errorf("a call older than the window should be forgotten, got %d", n)
	}
}

func TestRepeatDetector_WrapExecutorAnnotatesTheRepeat(t *testing.T) {
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "a.go:1: foo", nil
	})
	args := json.RawMessage(`{"pattern":"foo"}`)

	first, err := exec("search", args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "[repeat:") {
		t.Errorf("the first call is not a repeat: %q", first)
	}

	second, err := exec("search", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second, "[repeat:") {
		t.Errorf("the notice should lead the result, got %q", second)
	}
	if !strings.Contains(second, "search") || !strings.Contains(second, "2 times") {
		t.Errorf("the notice should name the tool and the count, got %q", second)
	}
	if !strings.HasSuffix(second, "a.go:1: foo") {
		t.Errorf("the result itself must survive the notice, got %q", second)
	}
}

func TestRepeatDetector_WrapExecutorNotesTheSameFailureTwice(t *testing.T) {
	// A call that fails the same way every time is circling as surely as one
	// that succeeds identically, and it is the shape a stuck turn usually
	// takes: a path that is not there, an argument the tool will not accept.
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) {
		return "", errors.New("no such file or directory")
	})
	args := json.RawMessage(`{"path":"internal/nope.go"}`)

	if _, err := exec("read_file", args); err == nil {
		t.Fatal("expected the underlying error to pass through")
	}
	_, err := exec("read_file", args)
	if err == nil {
		t.Fatal("the second failure is still a failure")
	}
	result := ExecuteWith(exec, provider.ToolCall{Name: "read_file", Arguments: string(args)})
	if !strings.HasPrefix(result, "error: [repeat:") {
		t.Errorf("the notice goes behind the error prefix, got %q", result)
	}
	if !IsRepeatNotice(result) {
		t.Error("a surface counting repeats has to see an errored one")
	}
	if !strings.Contains(result, "no such file") {
		t.Errorf("the failure itself must survive the notice, got %q", result)
	}
	if strings.Contains(result, "widen or narrow the search") {
		t.Errorf("a way out written for an unwanted answer is nonsense for a failure, got %q", result)
	}
}

func TestRepeatDetector_WrapResolverAnnotatesTheRepeat(t *testing.T) {
	// The gated tier: a command is resolved rather than dispatched, so this
	// is the only place the detector can see the call its own package
	// comment is written about.
	d := NewRepeatDetector()
	resolve := d.WrapResolver(func(provider.ToolCall) string {
		return "exit code: 1\noutput:\nFAIL\tinternal/calc"
	})
	call := provider.ToolCall{Name: "execute_command", Arguments: `{"command":"go test ./internal/calc"}`}

	if first := resolve(call); strings.Contains(first, "[repeat:") {
		t.Errorf("the first run is not a repeat: %q", first)
	}
	second := resolve(call)
	if !strings.HasPrefix(second, "[repeat:") {
		t.Errorf("the notice should lead the result, got %q", second)
	}
	if !strings.Contains(second, "execute_command") || !strings.Contains(second, "2 times") {
		t.Errorf("the notice should name the tool and the count, got %q", second)
	}
	if !strings.HasSuffix(second, "FAIL\tinternal/calc") {
		t.Errorf("the output itself must survive the notice, got %q", second)
	}
}

func TestRepeatDetector_ARepeatedRefusalIsStillAFailure(t *testing.T) {
	// Everything that reads a result tells a failure by its "error:" head —
	// the digest's outcome word, the record's class, the transcript row — so
	// the notice goes behind it rather than in front of it.
	d := NewRepeatDetector()
	resolve := d.WrapResolver(func(provider.ToolCall) string {
		return "error: file modification not approved: headless mode denies edits by default (run with --yes)"
	})
	call := provider.ToolCall{Name: "edit_file", Arguments: `{"path":"a.go","old":"x","new":"y"}`}

	_ = resolve(call)
	second := resolve(call)
	if !strings.HasPrefix(second, "error: ") {
		t.Errorf("a refused call stays a refusal, got %q", second)
	}
	if digest.Outcome(second) != digest.OutcomeError {
		t.Errorf("the digest must still read it as a failure, got %q", digest.Outcome(second))
	}
	if !IsRepeatNotice(second) {
		t.Errorf("the notice should be in there, got %q", second)
	}
}

func TestRepeatDetector_OneDetectorIsOneHistoryAcrossBothTiers(t *testing.T) {
	// The auto chain and the gated one share a window on purpose: a session
	// that re-runs a command it once had approved is the same circle whether
	// the second attempt was answered by a policy or by a person.
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) { return "same output", nil })
	resolve := d.WrapResolver(func(provider.ToolCall) string { return "same output" })
	args := `{"command":"ls"}`

	_, _ = exec("execute_command", json.RawMessage(args))
	if got := resolve(provider.ToolCall{Name: "execute_command", Arguments: args}); !IsRepeatNotice(got) {
		t.Errorf("the second tier should see the first tier's call, got %q", got)
	}
}

func TestRepeatDetector_NilIsSafe(t *testing.T) {
	var d *RepeatDetector
	if n := d.Note("search", json.RawMessage(`{}`), "out"); n != 0 {
		t.Errorf("a nil detector counts nothing, got %d", n)
	}
	exec := d.WrapExecutor(func(string, json.RawMessage) (string, error) { return "out", nil })
	if got, _ := exec("search", json.RawMessage(`{}`)); got != "out" {
		t.Errorf("a nil detector wraps nothing, got %q", got)
	}
	resolve := d.WrapResolver(func(provider.ToolCall) string { return "out" })
	if got := resolve(provider.ToolCall{Name: "execute_command"}); got != "out" {
		t.Errorf("a nil detector resolves nothing, got %q", got)
	}
	if got := d.Notice("search", json.RawMessage(`{}`), "out"); got != "out" {
		t.Errorf("a nil detector notices nothing, got %q", got)
	}
	if got := d.Sweeps(); got != nil {
		t.Errorf("a nil detector sweeps nothing, got %v", got)
	}
}

// searchOf is one search as the model writes it: a pattern, and the place it
// was put. The pattern is what varies in a sweep and the place is what does
// not, so every test below builds its calls this way.
func searchOf(pattern, path string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"pattern":%q,"path":%q}`, pattern, path))
}

func swept(result string) bool { return strings.HasPrefix(result, sweepNoticePrefix) }

func TestRepeatDetector_ManyPatternsOverOnePlaceAreASweep(t *testing.T) {
	// The failure the exact-repeat key cannot see: twenty different
	// questions, all put to one directory, none of them followed by a change.
	d := NewRepeatDetector()
	var notice string
	for i := 1; i <= 20; i++ {
		out := d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/ui/chat"),
			fmt.Sprintf("a.go:%d: needle%d", i, i))
		if i < sweepNoticeAfter && swept(out) {
			t.Fatalf("call %d: a sweep was called before the threshold:\n%s", i, out)
		}
		if i == sweepNoticeAfter {
			if !swept(out) {
				t.Fatalf("call %d: %d patterns over one directory is a sweep, got %q",
					i, sweepNoticeAfter, out)
			}
			notice = out
		}
	}
	for _, want := range []string{"12 search calls", "./internal/ui/chat", "nothing has been written"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice does not say %q:\n%s", want, notice)
		}
	}
	// The result is left standing under the notice, as a repeat's is: it is
	// still the answer, and what has changed is only that asking again is not
	// the way forward.
	if !strings.HasSuffix(notice, "a.go:12: needle12") {
		t.Errorf("the result should still be under the notice:\n%s", notice)
	}
}

func TestRepeatDetector_AWriteBetweenTheSearchesIsNotASweep(t *testing.T) {
	// The same twenty searches with one edit in the middle of them. A run
	// that has changed something is acting on what it found, so the count
	// starts again there and neither half reaches the threshold.
	d := NewRepeatDetector()
	for i := 1; i <= 20; i++ {
		if i == 11 {
			d.Notice("edit_file",
				json.RawMessage(`{"path":"internal/ui/chat/model.go","old_string":"a","new_string":"b"}`),
				"edited internal/ui/chat/model.go")
		}
		out := d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/ui/chat"),
			fmt.Sprintf("a.go:%d: needle%d", i, i))
		if swept(out) {
			t.Fatalf("call %d: a run that is writing is not circling:\n%s", i, out)
		}
	}
}

func TestRepeatDetector_ACommandEndsASweep(t *testing.T) {
	// shhh cannot know whether a command wrote anything and assumes it did,
	// so a run that searched, ran the tests and searched again is not one
	// that has been reading without acting.
	d := NewRepeatDetector()
	for i := 1; i <= 20; i++ {
		if i == 11 {
			d.Notice("execute_command", json.RawMessage(`{"command":"go test ./..."}`), "ok")
		}
		if out := d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
			fmt.Sprintf("out%d", i)); swept(out) {
			t.Fatalf("call %d: a command starts the count again:\n%s", i, out)
		}
	}
}

func TestRepeatDetector_ANarrowingSweepIsNotCircling(t *testing.T) {
	// The legitimate shape: a broad pattern, a narrower one, narrower again,
	// then the file they pointed at. Four calls is not a circle, and a
	// mechanism that called it one would cost every investigation a round.
	d := NewRepeatDetector()
	steps := []struct {
		tool string
		args json.RawMessage
	}{
		{"search", searchOf("Steering", "internal/agent")},
		{"search", searchOf("Steering.CheckIn", "internal/agent")},
		{"search", searchOf("CheckInInterval", "internal/agent")},
		{"read_file", json.RawMessage(`{"path":"internal/agent/checkin.go"}`)},
	}
	for i, s := range steps {
		if out := d.Notice(s.tool, s.args, fmt.Sprintf("out%d", i)); swept(out) {
			t.Fatalf("step %d (%s): a narrowing sweep is not circling:\n%s", i, s.tool, out)
		}
	}
}

func TestRepeatDetector_TwoPlacesAreTwoShapes(t *testing.T) {
	// Ten questions about each of two directories is twenty searches and no
	// sweep. The place is half the shape, because a run working through a
	// tree package by package is going somewhere and one asking a single
	// package twenty questions is not.
	d := NewRepeatDetector()
	for i := 1; i <= 10; i++ {
		for _, path := range []string{"internal/agent", "internal/ui/chat"} {
			if out := d.Notice("search",
				searchOf(fmt.Sprintf("needle%d", i), path),
				fmt.Sprintf("%s:%d", path, i)); swept(out) {
				t.Fatalf("%s call %d: two places are two shapes:\n%s", path, i, out)
			}
		}
	}
	// And one of the two, carried past the threshold on its own, is a sweep.
	var notice string
	for i := 11; i <= sweepNoticeAfter && notice == ""; i++ {
		if out := d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
			fmt.Sprintf("out%d", i)); swept(out) {
			notice = out
		}
	}
	if notice == "" {
		t.Fatalf("one place carried past the threshold is a sweep")
	}
	if !strings.Contains(notice, "./internal/agent") {
		t.Errorf("the notice names the place that was swept:\n%s", notice)
	}
}

func TestRepeatDetector_OneToolOverOnePlaceIsTheShape(t *testing.T) {
	// A run that searches a package and then lists its files is asking two
	// kinds of question, so the two are counted apart.
	d := NewRepeatDetector()
	for i := 1; i <= 10; i++ {
		for _, tool := range []string{"search", "glob"} {
			if out := d.Notice(tool,
				searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
				fmt.Sprintf("%s:%d", tool, i)); swept(out) {
				t.Fatalf("%s call %d: two tools are two shapes:\n%s", tool, i, out)
			}
		}
	}
}

func TestRepeatDetector_TheExactRepeatStillLeadsTheResult(t *testing.T) {
	// A call that comes back identical is the cheapest and clearest thing to
	// say, and it is still what a result leads with — one notice, not two,
	// because the second would land where the model has stopped reading.
	d := NewRepeatDetector()
	args := searchOf("needle", "internal/agent")
	if first := d.Notice("search", args, "a.go:1: needle"); IsRepeatNotice(first) || swept(first) {
		t.Fatalf("the first call carries no notice, got %q", first)
	}
	if second := d.Notice("search", args, "a.go:1: needle"); !IsRepeatNotice(second) {
		t.Fatalf("the second identical call still fires the repeat notice, got %q", second)
	}
	var last string
	for i := 0; i < sweepNoticeAfter; i++ {
		last = d.Notice("search", args, "a.go:1: needle")
	}
	if !IsRepeatNotice(last) {
		t.Errorf("the exact repeat wins the head of the result, got %q", last)
	}
	if strings.Contains(last, sweepNoticePrefix) {
		t.Errorf("only one notice leads a result:\n%s", last)
	}
}

func TestRepeatDetector_ASweepIsToldAgainOnlyAtTheNextThreshold(t *testing.T) {
	// Once past the threshold every remaining call would carry the notice,
	// which is the mechanism becoming the noise it warns about. It repeats at
	// the multiples instead.
	d := NewRepeatDetector()
	var at []int
	for i := 1; i <= 2*sweepNoticeAfter+2; i++ {
		if swept(d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
			fmt.Sprintf("out%d", i))) {
			at = append(at, i)
		}
	}
	want := []int{sweepNoticeAfter, 2 * sweepNoticeAfter}
	if fmt.Sprint(at) != fmt.Sprint(want) {
		t.Errorf("the notice should fire at %v, fired at %v", want, at)
	}
}

func TestRepeatDetector_ASweepThatFillsTheWindowStopsBeingTold(t *testing.T) {
	// A sweep long enough to fill the window stops growing: every call after
	// that evicts one of its own, so the count sits at the window's size for
	// the rest of the turn. Told on the count alone, the notice would fire on
	// every one of those calls — the mechanism becoming the noise it warns
	// about, in the run that needs it least noisy.
	d := NewRepeatDetector()
	var at []int
	for i := 1; i <= 2*sweepWindow; i++ {
		if swept(d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
			fmt.Sprintf("out%d", i))) {
			at = append(at, i)
		}
	}
	want := []int{12, 24, 36, 48}
	if fmt.Sprint(at) != fmt.Sprint(want) {
		t.Errorf("the notice should fire at %v over %d calls, fired at %v",
			want, 2*sweepWindow, at)
	}
	// And a write starts it over: the run acted, so the next dozen is a new
	// sweep and earns the notice again.
	d.Notice("write_file", json.RawMessage(`{"path":"internal/agent/repeat.go","content":"x"}`), "wrote")
	var again bool
	for i := 1; i <= sweepNoticeAfter; i++ {
		if swept(d.Notice("search",
			searchOf(fmt.Sprintf("after%d", i), "internal/agent"),
			fmt.Sprintf("after%d", i))) {
			again = true
		}
	}
	if !again {
		t.Error("a write starts the sweep over, notice and all")
	}
}

func TestRepeatDetector_AGitWriteEndsASweep(t *testing.T) {
	// The four verbs of git's writing half all change the repository, and it
	// is registered on every surface that reads a sweep. A run that searched
	// a dozen times, committed, and searched a dozen more has not been going
	// in one circle of twenty-four.
	d := NewRepeatDetector()
	for i := 1; i <= 20; i++ {
		if i == 11 {
			d.Notice(structural.GitWriteToolName,
				json.RawMessage(`{"verb":"commit","message":"fix the thing"}`), "committed")
		}
		if out := d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/agent"),
			fmt.Sprintf("out%d", i)); swept(out) {
			t.Fatalf("call %d: a commit starts the count again:\n%s", i, out)
		}
	}
}

func TestRepeatDetector_AFailingSweepIsStillASweep(t *testing.T) {
	// A dozen calls against a path that does not exist is the same circle as
	// a dozen that keep returning hits, and the failure is wrapped rather
	// than replaced so a caller testing it still finds it.
	d := NewRepeatDetector()
	exec := d.WrapExecutor(func(_ string, args json.RawMessage) (string, error) {
		return "", fmt.Errorf("no such directory: internal/nope (%s)", args)
	})
	var last error
	for i := 1; i <= sweepNoticeAfter; i++ {
		_, last = exec("search", searchOf(fmt.Sprintf("needle%d", i), "internal/nope"))
	}
	if last == nil || !swept(last.Error()) {
		t.Fatalf("a failing sweep is still a sweep, got %v", last)
	}
	if !strings.Contains(last.Error(), "no such directory") {
		t.Errorf("the failure is wrapped, not replaced: %v", last)
	}
}

func TestRepeatDetector_SweepsAreAFactTheReadingIsGiven(t *testing.T) {
	d := NewRepeatDetector()
	if got := d.Sweeps(); len(got) != 0 {
		t.Fatalf("nothing has been swept yet, got %v", got)
	}
	for i := 1; i < sweepNoticeAfter; i++ {
		d.Notice("search", searchOf(fmt.Sprintf("needle%d", i), "internal/agent"), fmt.Sprintf("out%d", i))
	}
	if got := d.Sweeps(); len(got) != 0 {
		t.Fatalf("below the threshold there is no sweep to report, got %v", got)
	}
	for i := sweepNoticeAfter; i <= 14; i++ {
		d.Notice("search", searchOf(fmt.Sprintf("needle%d", i), "internal/agent"), fmt.Sprintf("out%d", i))
	}
	want := []string{"search · ./internal/agent · 14 calls, nothing written"}
	if got := d.Sweeps(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sweeps\n got %v\nwant %v", got, want)
	}
	// A write is the run acting on what it found, and the fact goes with it.
	d.Notice("write_file", json.RawMessage(`{"path":"internal/agent/repeat.go","content":"x"}`), "wrote")
	if got := d.Sweeps(); len(got) != 0 {
		t.Errorf("a write ends the sweep, got %v", got)
	}
}

// A question is the one interaction whose answer is left out of its
// signature: the person may answer differently on the second asking, and a
// key that let that pull the two askings apart would never see the failure it
// is here for.
func TestRepeatDetector_OneQuestionAskedTwiceIsOneQuestion(t *testing.T) {
	d := NewRepeatDetector()
	q := json.RawMessage(`{"question":"Which store should the cache use?","shape":"choose",` +
		`"options":[{"label":"Postgres"},{"label":"SQLite"}]}`)

	if got := d.AskedBefore(q); got != "" {
		t.Fatalf("the first asking is not a repeat, got %q", got)
	}
	notice := d.AskedBefore(q)
	if notice == "" {
		t.Fatal("the same question put twice should say so")
	}
	for _, want := range []string{"2 times", "answer you have"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice does not say %q:\n%s", want, notice)
		}
	}
	// Under the key every other tool is held to these were two interactions
	// the moment the answers differed, which is the reasoning this inverts.
	if n := d.Note(ask.ToolName, q, `{"answered":"on the card","picked":["Redis"]}`); n != 3 {
		t.Errorf("what came back is no part of a question's key, got %d", n)
	}
}

// Only the question's own words are in the key, so a re-ask with the options
// reworded is the same question — and a genuinely different one is not.
func TestRepeatDetector_ADifferentQuestionIsNotARepeat(t *testing.T) {
	d := NewRepeatDetector()
	d.AskedBefore(json.RawMessage(`{"question":"Which store?","shape":"choose","options":[{"label":"A"}]}`))

	reworded := json.RawMessage(`{"question":"Which store?","shape":"choose","options":[{"label":"B"},{"label":"C"}]}`)
	if got := d.AskedBefore(reworded); got == "" {
		t.Error("one question with its options reworded is still one question")
	}
	other := json.RawMessage(`{"question":"Should the migration be reversible?","shape":"confirm"}`)
	if got := d.AskedBefore(other); got != "" {
		t.Errorf("a different question is a different question, got %q", got)
	}
}

// A question changes nothing on the machine, so it neither ends a sweep of
// searches nor starts one: a run that stopped to ask in the middle of an
// investigation is still on the same investigation.
func TestRepeatDetector_AQuestionNeitherEndsASweepNorStartsOne(t *testing.T) {
	if wroteSomething(ask.ToolName, json.RawMessage(`{"question":"Which store?","shape":"text"}`)) {
		t.Fatal("a question writes nothing")
	}
	d := NewRepeatDetector()
	var notice string
	for i := 1; i <= sweepNoticeAfter; i++ {
		if i == 6 {
			d.AskedBefore(json.RawMessage(`{"question":"Which store?","shape":"text"}`))
		}
		notice = d.Notice("search",
			searchOf(fmt.Sprintf("needle%d", i), "internal/ui/chat"),
			fmt.Sprintf("a.go:%d: needle%d", i, i))
	}
	if !swept(notice) {
		t.Errorf("a question in the middle of a sweep does not end it:\n%s", notice)
	}
	if got := d.Sweeps(); len(got) != 1 {
		t.Errorf("the question is part of no sweep of its own, got %v", got)
	}
}
