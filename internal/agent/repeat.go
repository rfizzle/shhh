package agent

// Noticing a session ask the same question twice.
//
// The failure this exists for looks like work: forty searches over the same
// directory, each returning what the one nine rounds ago returned, until the
// round cap ends the turn with nothing changed. Nothing in the loop was
// broken — the model simply could not tell it was going in a circle, because
// every round looks like progress from inside one.
//
// So the loop says so. The signature is the whole interaction — the tool, its
// arguments, and the output it produced — which is what makes it safe to
// apply to every tool: `go test` run twice is two different interactions the
// moment its output differs, and identical only when nothing has changed,
// which is exactly when running it again is pointless. Crush stops the turn
// on the same signal; we tell the model instead, because the model is the one
// that can pick a different approach, and a turn stopped is a turn the user
// has to restart.
//
// It sits on both tiers, because the calls a session circles on are mostly
// the ones somebody has to answer for: the failing command run again, the
// refused edit issued again. A detector wrapped around the auto chain alone
// would be one that cannot see the example in the paragraph above.
// See docs/capabilities/coding-agent.md#a-call-the-session-has-already-made-is-answered-by-saying-so.
//
// The worked example above is also the shape the signature cannot see. Forty
// searches over one directory are forty *different* patterns far more often
// than one pattern forty times, and each of those is its own interaction:
// different arguments, different output, no key in common. The exact repeat
// is the tail of that failure and not its body, so a second signal sits
// beside it — a sweep, which is many calls of one shape over one place with
// nothing written between them. It is a second signal rather than a looser
// key on purpose: putting the result in the signature is what makes the
// exact repeat safe on every tool, and loosening it to catch the sweep would
// buy the sweep by giving that up.
// See docs/capabilities/coding-agent.md#many-questions-about-one-place-are-a-sweep.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
)

const (
	// repeatWindow is how many recent tool interactions are remembered. Long
	// enough to catch a circling investigation, short enough that a
	// legitimate revisit many rounds later is not called a repeat.
	repeatWindow = 24

	// repeatNoticeAfter is the occurrence within the window that earns a
	// notice. The second identical interaction is already one too many:
	// waiting for a third spends another round to say the same thing.
	repeatNoticeAfter = 2

	// sweepWindow is how many recent calls a sweep is counted over — twice
	// the repeat window, because a sweep accumulates far more slowly than an
	// exact repeat. The same question asked twice shows up within a handful
	// of rounds; twenty different questions about one directory take most of
	// a turn. Wider than this and a sweep would be assembled partly out of
	// work the run has plainly moved on from.
	sweepWindow = 48

	// sweepNoticeAfter is how many calls of one shape over one scope, with
	// nothing written between them, earn the notice.
	//
	// Twelve is past every legitimate shape of the same question. A
	// narrowing sweep — a broad pattern, a narrower one, then the file it
	// pointed at — is three or four calls; a thorough survey of an
	// unfamiliar package is six or seven. It is also short of the twenty the
	// observed failure had reached by the time the first reading called it
	// on target, so the notice arrives with most of the turn still ahead of
	// it. And it is deliberately more than half of that twenty: a run that
	// writes something in the middle of a long investigation starts counting
	// again from zero, and must not be told it is circling on the strength
	// of the reading it did before the write.
	sweepNoticeAfter = 12
)

// RepeatDetector watches a session's tool interactions for the two shapes of
// going in a circle: the same call made again, and a sweep — many calls of
// one shape over one place with nothing written between them. The zero value
// is not usable; call NewRepeatDetector.
type RepeatDetector struct {
	mu     sync.Mutex
	recent []string
	// calls is the sweep's window, newest last: what shape each recent call
	// belonged to, and whether it wrote anything. It is a second window
	// rather than another field on the first because the two are counted
	// over different stretches — the exact repeat over a fixed number of
	// interactions, the sweep back to the last thing the run changed.
	calls []sweepCall
	// told is the count each shape was last noticed at, so a sweep is told
	// again only when it has grown by another threshold's worth.
	//
	// It cannot be derived from the count alone. A sweep that fills the
	// window stops growing — every call after that evicts one of its own and
	// the count pins at sweepWindow — so a rule that fired whenever the count
	// sat on a multiple would fire on every call for the rest of the turn,
	// which is the mechanism becoming the noise it exists to warn about. A
	// write ends every sweep, so it empties this too.
	told map[string]int
}

// sweepCall is one call as the sweep count reads it: the shape it belongs to,
// empty for a call that is part of no sweep, and whether it changed anything,
// which is what ends one.
type sweepCall struct {
	shape string
	wrote bool
}

func NewRepeatDetector() *RepeatDetector {
	return &RepeatDetector{
		recent: make([]string, 0, repeatWindow),
		calls:  make([]sweepCall, 0, sweepWindow),
		told:   map[string]int{},
	}
}

// Note records one completed interaction and returns how many times this
// exact one has occurred inside the window, itself included. Safe on a nil
// detector, which counts nothing.
func (d *RepeatDetector) Note(tool string, args json.RawMessage, result string) int {
	n, _ := d.note(tool, args, result)
	return n
}

// note records one completed interaction against both windows and reports
// what each makes of it. They are written together rather than by two calls
// because they are one history read two ways: a call present in one window
// and missing from the other would leave the two counts disagreeing about
// work that plainly happened.
func (d *RepeatDetector) note(tool string, args json.RawMessage, result string) (repeats int, sw sweep) {
	if d == nil {
		return 0, sweep{}
	}
	key := interactionKey(tool, args, result)
	call := sweepCall{wrote: wroteSomething(tool, args)}
	scope, swept := digest.SearchScope(tool, string(args))
	if swept {
		call.shape = tool + shapeSep + scope
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.recent) == repeatWindow {
		d.recent = d.recent[1:]
	}
	d.recent = append(d.recent, key)
	for _, k := range d.recent {
		if k == key {
			repeats++
		}
	}

	if len(d.calls) == sweepWindow {
		d.calls = d.calls[1:]
	}
	d.calls = append(d.calls, call)
	if call.wrote {
		// Every sweep ends here, so what each of them was last told about
		// ends with it: the next one starts from nothing.
		clear(d.told)
	}
	if call.shape == "" {
		return repeats, sweep{}
	}
	n, span := d.countSweep(call.shape)
	sw = sweep{tool: tool, scope: scope, n: n, span: span}
	switch {
	case n < sweepNoticeAfter:
		// Below the threshold there is nothing to have been told, and the
		// shape drops out rather than being remembered at a count it no
		// longer has.
		delete(d.told, call.shape)
	case n-d.told[call.shape] >= sweepNoticeAfter:
		d.told[call.shape] = n
		sw.due = true
	}
	return repeats, sw
}

// countSweep is how many calls in the window share a shape, counting back
// from the newest and stopping at the last one that wrote something, and how
// many calls that stretch covers.
//
// It is walked rather than tallied as it goes because a running tally is
// invalidated by every write, and a window this size is cheaper to walk than
// to keep correct. The caller holds the lock.
func (d *RepeatDetector) countSweep(shape string) (n, span int) {
	for i := len(d.calls) - 1; i >= 0; i-- {
		c := d.calls[i]
		if c.shape == shape {
			n++
			span = len(d.calls) - i
		}
		if c.wrote {
			break
		}
	}
	return n, span
}

// Notice is one completed interaction as the model should read it: the notice
// leads the result where this exact call has run before, or where it is one
// more pass over ground the run has been over a dozen times, and the result
// is itself otherwise. Every tier goes through it, so the auto executor, the
// gated resolver and a front-end that dispatches a call itself all apply the
// same rules to the same windows. Safe on a nil detector.
//
// Only one notice ever leads a result, and the exact repeat wins it. It is
// the cheaper and clearer thing to say, and a call that came back
// byte-identical has already been told the part of the sweep notice it could
// act on. Two notices on one result would also put the second one where the
// model has stopped reading, which is the whole reason a notice leads.
func (d *RepeatDetector) Notice(tool string, args json.RawMessage, result string) string {
	repeats, sw := d.note(tool, args, result)
	switch {
	case repeats >= repeatNoticeAfter:
		failed := strings.HasPrefix(result, errorPrefix)
		return leadNotice(repeatNotice(tool, repeats, failed), result)
	case sw.due:
		return leadNotice(sweepNotice(sw), result)
	}
	return result
}

// Sweeps are the sweeps standing now, widest first, as rows for the reading
// that judges whether a run is getting anywhere.
//
// The reader is handed this as a fact rather than left to infer it. A
// search's row leads with its pattern, so twelve searches of one directory
// are twelve legible and genuinely different rows — and a reading looking at
// twelve different questions still has to work out for itself that they were
// all put to the same place and that nothing came of any of them. That is the
// judgement this spares it. Safe on a nil detector, which is sweeping
// nothing.
func (d *RepeatDetector) Sweeps() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	tally := map[string]*sweep{}
	for i := len(d.calls) - 1; i >= 0; i-- {
		c := d.calls[i]
		if c.shape != "" {
			if s := tally[c.shape]; s != nil {
				s.n++
			} else {
				tool, scope, _ := strings.Cut(c.shape, shapeSep)
				tally[c.shape] = &sweep{tool: tool, scope: scope, n: 1}
			}
		}
		if c.wrote {
			break
		}
	}

	out := make([]sweep, 0, len(tally))
	for _, s := range tally {
		if s.n >= sweepNoticeAfter {
			out = append(out, *s)
		}
	}
	// Widest first, and by shape where two are equal, so one window renders
	// the same rows in the same order twice: a digest that reshuffles itself
	// between readings reads as a run that has changed course.
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].tool+out[i].scope < out[j].tool+out[j].scope
	})
	rows := make([]string, 0, len(out))
	for _, s := range out {
		rows = append(rows, SummarySweep(s.tool, s.scope, s.n))
	}
	return rows
}

// WrapExecutor wraps a tool executor so an interaction the session has
// already had comes back saying so. The notice leads the result, where the
// reduction pipeline puts its own: a notice below a hundred lines of output
// is one the model has already stopped reading by the time it arrives.
//
// A call that failed is counted too, keyed on the error the model reads. A
// path that does not exist, an argument the tool will never accept and a
// refusal that stands are circling as surely as a search that keeps returning
// the same hits, and a failure repeated is the shape a stuck turn most often
// takes.
func (d *RepeatDetector) WrapExecutor(next ToolExecutor) ToolExecutor {
	if d == nil {
		return next
	}
	return func(name string, args json.RawMessage) (string, error) {
		out, err := next(name, args)
		if err != nil {
			// The caller puts errorPrefix back in front of either notice, so
			// what the model reads is what leadNotice builds for a failure.
			// The error is wrapped rather than replaced: a caller that tests
			// it with errors.Is is asking about the failure and not about how
			// often it has happened.
			repeats, sw := d.note(name, args, errorPrefix+err.Error())
			switch {
			case repeats >= repeatNoticeAfter:
				return out, fmt.Errorf("%s\n%w", repeatNotice(name, repeats, true), err)
			case sw.due:
				// A search that keeps failing over one directory is sweeping
				// it as surely as one that keeps succeeding, and a run that
				// has spent twelve calls on a path that does not exist has
				// the same thing to be told.
				return out, fmt.Errorf("%s\n%w", sweepNotice(sw), err)
			}
			return out, err
		}
		return d.Notice(name, args, out), nil
	}
}

// WrapResolver wraps the gated tier the way WrapExecutor wraps the auto one.
// A command and a file modification are resolved rather than dispatched — the
// decision and the call are one function — so the tier the detector was
// written for is reachable only here, and a run that wraps one chain and not
// the other cannot see the case its own comment names.
func (d *RepeatDetector) WrapResolver(next func(provider.ToolCall) string) func(provider.ToolCall) string {
	if d == nil {
		return next
	}
	return func(tc provider.ToolCall) string {
		return d.Notice(tc.Name, json.RawMessage(tc.Arguments), next(tc))
	}
}

// IsRepeatNotice reports whether a result leads with the detector's notice,
// so a front-end can count how often the session was told it was circling
// without knowing how the notice is worded.
func IsRepeatNotice(result string) bool {
	return strings.HasPrefix(result, repeatNoticePrefix) ||
		strings.HasPrefix(result, errorPrefix+repeatNoticePrefix)
}

const repeatNoticePrefix = "[repeat:"

// errorPrefix is the error convention every executor and every resolver
// follows, and the one thing allowed in front of the notice.
const errorPrefix = "error: "

// leadNotice puts the notice at the head of the result, behind the error
// prefix where there is one. Everything that reads a result tells a failure
// by that prefix — the digest's outcome word, the record's failure class, the
// transcript row — so a notice written in front of it would turn every
// repeated refusal into a success in all three.
func leadNotice(notice, result string) string {
	if rest, ok := strings.CutPrefix(result, errorPrefix); ok {
		return errorPrefix + notice + "\n" + rest
	}
	return notice + "\n" + result
}

// repeatNotice is what the model reads. A call that keeps working and a call
// that keeps failing are the same circle and take the same first sentence,
// but not the same way out: "widen the search" is advice for an answer that
// is not the one wanted, and nonsense for a command that will not run or an
// edit a policy has refused twice.
func repeatNotice(tool string, n int, failed bool) string {
	if failed {
		return fmt.Sprintf(
			repeatNoticePrefix+" this exact %s call has now come back the same way %d times — "+
				"the earlier result is still above, unchanged. Making it again will not change it. "+
				"Answer what the result says, or reach the same end another way.]",
			tool, n)
	}
	return fmt.Sprintf(
		repeatNoticePrefix+" this exact %s call has now run %d times and returned exactly this each time — "+
			"the earlier result is still above, unchanged. Running it again will not answer the question. "+
			"Read one of the files it names, widen or narrow the search, or use a different tool.]",
		tool, n)
}

// interactionKey identifies one tool interaction. Arguments are canonicalised
// through a decode/encode round trip so the same call written two ways — a
// different key order, different spacing — is recognised as the same call.
func interactionKey(tool string, args json.RawMessage, result string) string {
	canonical := string(args)
	var v any
	if err := json.Unmarshal(args, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = string(b)
		}
	}
	return tool + "\x00" + canonical + "\x00" + result
}

// shapeSep joins a sweep's two halves. It is a byte no tool name and no path
// contains, for the reason interactionKey uses one: a separator a value can
// carry is a separator two different shapes can collide on.
const shapeSep = "\x00"

// sweep is what one call amounts to as part of a sweep: which tool over which
// place, how many calls of that shape the window holds since the run last
// wrote anything, how many calls that stretch covers, and whether this call
// is the one that earns the notice. A zero n is a call that belongs to no
// sweep.
//
// The shape is the tool and the scope, and the pattern is deliberately no
// part of it — the pattern varying while the place does not is the failure
// itself, and a shape that included the pattern would be the exact repeat
// again under another name. The tool is part of it because a run that
// searches a directory and then globs it is asking two kinds of question, and
// the case observed was one tool over one place with the pattern changing.
//
// due is decided where the count is taken and not from the count afterwards,
// because it is a question about growth: the notice is owed at the threshold
// and again at every threshold's worth after it, and a sweep that has filled
// the window has stopped growing while still standing at a large number.
type sweep struct {
	tool  string
	scope string
	n     int
	span  int
	due   bool
}

// sweepNotice is what the model reads when it has been over one place a dozen
// times. It says what has been swept, how much of the run's recent work that
// has been, and that none of it has changed anything — because a session that
// believes each of its searches is a new question will answer "you are
// repeating yourself" by searching again. The count is in calls rather than
// rounds: the detector sits under the executor, where a round is not
// something it can see, and one round can dispatch several of these at once.
func sweepNotice(s sweep) string {
	return fmt.Sprintf(
		sweepNoticePrefix+" %d %s calls over %s in the last %d tool calls, and nothing "+
			"has been written in that time. The patterns differ; the ground does not. "+
			"Say what these have established, then act on it — read one of the files they "+
			"named, or make the change they were for. Another pattern over the same place "+
			"will not answer the question.]",
		s.n, s.tool, s.scope, s.span)
}

const sweepNoticePrefix = "[sweep:"

// wroteSomething reports whether a call changed anything, which is what ends a
// sweep: a run that has written something is acting on what it found,
// whatever it searched for before that.
//
// The file writes are read the way every surface that keeps no changeset
// reads them, so a third one of those ends a sweep the day it is registered
// rather than the day somebody remembers this line. The other two are named
// because they are registered elsewhere and no shared reading covers them: a
// command, because shhh cannot know whether a command wrote anything and
// assumes it did, and the writing half of git, whose four verbs all change
// the repository. Missing either one would let a run that searched a dozen
// times, committed, and searched a dozen more be told it had been going in
// one circle of twenty-four.
// See docs/interface/principles.md#weight-tracks-risk.
func wroteSomething(tool string, args json.RawMessage) bool {
	return tool == tools.ExecCommandName ||
		tool == structural.GitWriteToolName ||
		tools.WrittenPath(tool, string(args)) != ""
}
