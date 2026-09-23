package quality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is a recorded `go test -json` run over four packages: one
// with two containment skips whose details differ, a failing subtest, a
// bare SkipNow and a passing test that logged; one that does not build; one
// replayed from the cache with a skip; and one with no test files.
func TestReadTestJSON_RecordedRun(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "gotest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out strings.Builder
	counts, failed, err := ReadTestJSON(f, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Error("a stream with a failing package and a failed build read as passing")
	}
	got := out.String()
	for _, want := range []string{
		"b/b.go:2:12: undefined: y\n",   // the build error, in full
		"FAIL\tsk/b [build failed]\n",   // and its package line
		"ok  \tsk/c\t(cached)\n",        // a cached package keeps its line
		"?   \tsk/d\t[no test files]\n", // so does one with nothing to run
		"a_test.go:12: boom\n",          // the failing test's own lines
		"--- FAIL: TestFail/sub",
		"FAIL\tsk\t",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	// What a passing test printed is not what a plain run shows.
	for _, not := range []string{"quiet", "=== RUN   TestPass", "--- SKIP", "probing"} {
		if strings.Contains(got, not) {
			t.Errorf("output carries %q, which a plain go test would not show:\n%s", not, got)
		}
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	tail := lines[len(lines)-3:]
	want := []string{
		"skipped 2: no Seatbelt containment here",
		"skipped 1: git not on PATH",
		"skipped 1: no reason given",
	}
	if strings.Join(tail, "\n") != strings.Join(want, "\n") {
		t.Errorf("the output ends on\n%s\nwant\n%s", strings.Join(tail, "\n"), strings.Join(want, "\n"))
	}
	if len(counts) != 3 || !counts[0].Mechanism || counts[1].Mechanism {
		t.Errorf("counts = %+v, want the mechanism reason first and alone marked", counts)
	}
}

func TestReadTestJSON_MechanismBeforeACommonerReason(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"output","Package":"p","Test":"TestA","Output":"    a_test.go:1: git not on PATH\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestA"}`,
		`{"Action":"output","Package":"p","Test":"TestB","Output":"    a_test.go:2: git not on PATH\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestB"}`,
		`{"Action":"output","Package":"p","Test":"TestC","Output":"    a_test.go:3: no bubblewrap containment here: bwrap: no permissions\n"}`,
		`{"Action":"skip","Package":"p","Test":"TestC"}`,
		`{"Action":"output","Package":"p","Output":"ok  \tp\t0.1s\n"}`,
		`{"Action":"pass","Package":"p"}`,
	}, "\n")
	var out strings.Builder
	if _, failed, err := ReadTestJSON(strings.NewReader(stream), &out); err != nil || failed {
		t.Fatalf("failed=%v err=%v", failed, err)
	}
	want := "ok  \tp\t0.1s\nskipped 1: no bubblewrap containment here\nskipped 2: git not on PATH\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// A stream that ends inside a package — the run was killed — prints what
// the package had said and counts as a failure.
func TestReadTestJSON_UnfinishedPackageIsAFailure(t *testing.T) {
	stream := `{"Action":"output","Package":"p","Test":"TestA","Output":"=== RUN   TestA\n"}` + "\n" +
		`{"Action":"output","Package":"p","Output":"panic: test timed out\n"}`
	var out strings.Builder
	_, failed, err := ReadTestJSON(strings.NewReader(stream), &out)
	if err != nil || !failed {
		t.Fatalf("failed=%v err=%v", failed, err)
	}
	if !strings.Contains(out.String(), "panic: test timed out") {
		t.Errorf("the unfinished package's output was lost:\n%s", out.String())
	}
}

func TestRun_SkipLinesReachTheResultOfAPassingCheck(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite(`printf 'ok  \tp\t0.1s\nskipped 9: no Seatbelt containment here\nskipped 1: git not on PATH\n'`))
	r := &Runner{Workspace: ws}
	res := mustRun(t, r, "")
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict = %s", res.Verdict)
	}
	text := res.Format(res.Fingerprint)
	want := "  ✓ check — sh -c "
	i := strings.Index(text, want)
	if i < 0 {
		t.Fatalf("no check row:\n%s", text)
	}
	if !strings.Contains(text[i:], "\n    skipped 9: no Seatbelt containment here\n    skipped 1: git not on PATH") {
		t.Errorf("the skip lines are not under the check's row:\n%s", text)
	}

	s, ok := Summarize(text)
	if !ok {
		t.Fatal("the result did not read back")
	}
	wantSkips := []Skipped{
		{Check: "check", Count: 9, Reason: "no Seatbelt containment here"},
		{Check: "check", Count: 1, Reason: "git not on PATH"},
	}
	if len(s.Skipped) != len(wantSkips) {
		t.Fatalf("Skipped = %+v, want %+v", s.Skipped, wantSkips)
	}
	for i := range wantSkips {
		if s.Skipped[i] != wantSkips[i] {
			t.Errorf("Skipped[%d] = %+v, want %+v", i, s.Skipped[i], wantSkips[i])
		}
	}
}

// A failing check prints its output, and the skip lines are taken out of it
// so they are said once, under the row.
func TestRun_SkipLinesOfAFailingCheckAreSaidOnce(t *testing.T) {
	ws := t.TempDir()
	writeConfig(t, ws, shSuite(`printf 'FAIL\tp\t0.1s\nskipped 2: git not on PATH\n'; exit 1`))
	res := mustRun(t, &Runner{Workspace: ws}, "")
	text := res.Format(res.Fingerprint)
	if n := strings.Count(text, "\n    skipped 2: git not on PATH"); n != 1 {
		t.Errorf("the skip line appears %d times:\n%s", n, text)
	}
	if !strings.Contains(text, "FAIL\tp") {
		t.Errorf("the failing output is gone:\n%s", text)
	}
}
