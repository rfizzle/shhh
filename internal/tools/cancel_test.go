package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// stopSoon is a call's context the turn stops a moment after the call
// starts, and how long the stop is allowed to take to reach it: well inside
// the runner's kill grace, and far short of any of the tools' own limits.
func stopSoon(t *testing.T) (context.Context, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stop := time.AfterFunc(100*time.Millisecond, cancel)
	t.Cleanup(func() { stop.Stop(); cancel() })
	return ctx, 5 * time.Second
}

// A stopped call answers with the stop itself, so the executor chain reads it
// as stopped rather than as a failure the model is invited to try again.
func assertStopped(t *testing.T, out string, err error, took, within time.Duration) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v (out %q), want the stop", err, out)
	}
	if took > within {
		t.Errorf("the stop took %s to reach the call", took)
	}
}

func TestTools_EscStopsARunningSearch(t *testing.T) {
	t.Run("ripgrep", func(t *testing.T) {
		// A ripgrep that never answers: only the stop ends it, and the
		// walker is not then sent to search the tree in its place.
		fakeRg(t, "exec sleep 30\n")
		ctx, within := stopSoon(t)
		args, _ := json.Marshal(searchArgs{Pattern: "x", Path: t.TempDir()})
		start := time.Now()
		out, err := Execute(ctx, SearchName, args)
		assertStopped(t, out, err, time.Since(start), within)
	})
	t.Run("only matching", func(t *testing.T) {
		fakeRg(t, "exec sleep 30\n")
		ctx, within := stopSoon(t)
		args, _ := json.Marshal(searchArgs{Pattern: "x", Path: t.TempDir(), OnlyMatching: true})
		start := time.Now()
		out, err := Execute(ctx, SearchName, args)
		assertStopped(t, out, err, time.Since(start), within)
	})
	t.Run("walker", func(t *testing.T) {
		forceWalker(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		args, _ := json.Marshal(searchArgs{Pattern: "x", Path: t.TempDir()})
		start := time.Now()
		out, err := Execute(ctx, SearchName, args)
		assertStopped(t, out, err, time.Since(start), time.Second)
	})
}

func TestTools_EscStopsAQuery(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		p := writeQueryFile(t, t.TempDir(), "a.json", `1`)
		raw, _ := json.Marshal(map[string]any{"paths": []string{p}, "expression": "def f: f; f"})
		ctx, within := stopSoon(t)
		start := time.Now()
		out, err := NewRecorder().Execute(ctx, QueryName, raw)
		assertStopped(t, out, err, time.Since(start), within)
		if err != nil && strings.Contains(err.Error(), "narrow") {
			t.Errorf("a stopped query was told to narrow itself: %v", err)
		}
	})
	t.Run("sqlite", func(t *testing.T) {
		p := makeSqliteDB(t, t.TempDir(), "a.db", `CREATE TABLE t (x)`)
		const forever = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c`
		for _, sql := range [][]string{{forever}, {forever, `SELECT 1`}} {
			raw, _ := json.Marshal(map[string]any{"path": p, "sql": sql})
			ctx, within := stopSoon(t)
			start := time.Now()
			out, err := NewRecorder().Execute(ctx, SqliteName, raw)
			assertStopped(t, out, err, time.Since(start), within)
		}
	})
}
