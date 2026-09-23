package quality

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A skipped test is a test that did not run, and a suite that skipped the
// tests which put a boundary to the kernel is not a suite that proved the
// boundary. The test check therefore ends on one line per distinct skip
// reason with its count, and the gate carries those lines on the check's own
// row whatever its verdict — so a green run inside containment says, in the
// result a person reads, which evidence it did not gather.
// See docs/capabilities/testing.md#a-skipped-test-is-counted.

// SkipLine is the one spelling of a skip-reason line: written by the test
// reader at the end of its output, pulled out of a check's capture by the
// runner, and read back by Summarize.
func SkipLine(count int, reason string) string {
	return fmt.Sprintf("skipped %d: %s", count, reason)
}

var skipLinePattern = regexp.MustCompile(`^skipped (\d+): (.+)$`)

// SkipCount is one distinct reason tests skipped for, and how many did.
type SkipCount struct {
	Reason string
	Count  int
	// Mechanism marks a reason that names a missing containment mechanism:
	// the tests behind it are the ones only a prepared host can run.
	Mechanism bool
}

// mechanismWords are what a skip message says when the host lacks the
// containment mechanism a test needed. Matched case-folded.
var mechanismWords = []string{"seatbelt", "bubblewrap", "sandbox-exec", "containment", "container engine"}

func namesMechanism(reason string) bool {
	r := strings.ToLower(reason)
	for _, w := range mechanismWords {
		if strings.Contains(r, w) {
			return true
		}
	}
	return false
}

// skipReason is the distinct reason a skip message is counted under: its
// first line, cut at the first ": ". What follows that colon is the detail a
// %v or %s put there — an error, a platform name — and counting on it would
// split one reason into as many lines as there were errors.
func skipReason(msg string) string {
	msg = strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	if i := strings.Index(msg, ": "); i > 0 {
		msg = msg[:i]
	}
	if msg == "" {
		return "no reason given"
	}
	return msg
}

// messagePattern is the prefix the testing package puts on a logged line:
// indentation, the file and line, a colon.
var messagePattern = regexp.MustCompile(`^\s+\S+\.go:\d+: (.*)$`)

// testEvent is the subset of `go test -json`'s event this reader needs.
type testEvent struct {
	Action     string
	Package    string
	Test       string
	Output     string
	ImportPath string
}

type pkgLine struct {
	test, text string
}

// ReadTestJSON reads a `go test -json` stream from r and writes to w what a
// plain `go test` over the same packages would have shown — each package's
// summary line, every build error, and the whole output of a package that
// failed — and then one SkipLine per distinct skip reason, the reasons
// naming a missing mechanism first. It returns the counts it wrote and
// whether any package or build failed.
//
// It reads a stream and adds nothing to the run: `go test -json` is a
// cacheable flag, so the suite it wraps stays as cacheable as it was.
func ReadTestJSON(r io.Reader, w io.Writer) ([]SkipCount, bool, error) {
	counts := map[string]int{}
	failed := false
	lines := map[string][]pkgLine{}
	failedTests := map[string]map[string]bool{}
	lastMessage := map[string]string{} // package + "\x00" + test → the last logged message

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		raw := sc.Bytes()
		var ev testEvent
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &ev) != nil {
			// Not an event: something wrote to the stream directly. Pass
			// it on rather than hide it.
			fmt.Fprintf(w, "%s\n", raw)
			continue
		}
		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "build-output":
			fmt.Fprint(w, ev.Output)
		case "build-fail":
			failed = true
		case "output":
			lines[ev.Package] = append(lines[ev.Package], pkgLine{ev.Test, ev.Output})
			if ev.Test != "" {
				if m := messagePattern.FindStringSubmatch(strings.TrimRight(ev.Output, "\n")); m != nil {
					lastMessage[key] = m[1]
				}
			}
		case "skip":
			if ev.Test != "" {
				counts[skipReason(lastMessage[key])]++
			} else {
				flushPackage(w, lines[ev.Package], nil, false)
				delete(lines, ev.Package)
			}
		case "fail":
			if ev.Test != "" {
				if failedTests[ev.Package] == nil {
					failedTests[ev.Package] = map[string]bool{}
				}
				failedTests[ev.Package][ev.Test] = true
				continue
			}
			failed = true
			flushPackage(w, lines[ev.Package], failedTests[ev.Package], true)
			delete(lines, ev.Package)
		case "pass":
			if ev.Test == "" {
				flushPackage(w, lines[ev.Package], nil, false)
				delete(lines, ev.Package)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, failed, err
	}
	// A package the stream ended inside — the run was killed, or timed out —
	// never reached the event that flushes it; what it printed is the only
	// account of why, so all of it is written.
	pending := make([]string, 0, len(lines))
	for pkg := range lines {
		pending = append(pending, pkg)
	}
	sort.Strings(pending)
	for _, pkg := range pending {
		failed = true
		for _, l := range lines[pkg] {
			fmt.Fprint(w, l.text)
		}
	}

	out := make([]SkipCount, 0, len(counts))
	for reason, n := range counts {
		out = append(out, SkipCount{Reason: reason, Count: n, Mechanism: namesMechanism(reason)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Mechanism != b.Mechanism {
			return a.Mechanism
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Reason < b.Reason
	})
	for _, s := range out {
		fmt.Fprintln(w, SkipLine(s.Count, s.Reason))
	}
	return out, failed, nil
}

// flushPackage writes a finished package the way `go test` without -v
// does: a package that passed is its summary line, and one that failed is
// everything its failed tests and the package itself printed, in order.
func flushPackage(w io.Writer, lines []pkgLine, failedTests map[string]bool, failed bool) {
	for _, l := range lines {
		switch {
		case l.test == "" && !failed && strings.TrimSpace(l.text) == "PASS":
		case l.test == "", failed && failedTests[l.test]:
			fmt.Fprint(w, l.text)
		}
	}
}

// Skipped is one skip line as a formatted result carries it: the check it
// was reported under, the count and the reason.
type Skipped struct {
	Check  string
	Count  int
	Reason string
}

// splitSkips pulls the skip lines out of a check's capture: the lines
// themselves, and the capture without them, which is what the excerpt is
// cut from so a failing check does not print them twice.
func splitSkips(captured string) ([]string, string) {
	if !strings.Contains(captured, "skipped ") {
		return nil, captured
	}
	var skips []string
	var rest strings.Builder
	for _, line := range strings.SplitAfter(captured, "\n") {
		if skipLinePattern.MatchString(strings.TrimRight(line, "\n")) {
			skips = append(skips, strings.TrimRight(line, "\n"))
			continue
		}
		rest.WriteString(line)
	}
	return skips, rest.String()
}

func parseSkipLine(check, line string) (Skipped, bool) {
	m := skipLinePattern.FindStringSubmatch(line)
	if m == nil {
		return Skipped{}, false
	}
	n, _ := strconv.Atoi(m[1])
	return Skipped{Check: check, Count: n, Reason: m[2]}, true
}
