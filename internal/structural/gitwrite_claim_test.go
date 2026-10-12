package structural

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A claim is a question and never a key: the stager admits a claimed path
// only once the record holds it, which is what an answer on the card does.
// A claim on a path the tree shows unchanged is refused before anybody is
// asked, and the receipt counts what it staged in the record's own words.
func TestGitWriteClaimIsAdmittedOnlyThroughTheRecord(t *testing.T) {
	if _, ok := lookPath("git"); !ok {
		t.Skip("git is not on PATH")
	}
	root := writeRepo(t)
	gen := filepath.Join(root, "gen.pb.go")
	if err := os.WriteFile(gen, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held := []string{}
	ts := writeToolset(t, root, Writes{
		Files:     func() []string { return held },
		Origins:   func() map[string]string { return map[string]string{gen: "generated"} },
		Generated: func(paths []string) []string { return paths },
	})
	claim := json.RawMessage(`{"verb":"add","claim":[{"path":"gen.pb.go","why":"written by make gen this turn"}]}`)

	claims, err := ts.Claims(claim)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %v, %v", claims, err)
	}
	if got, want := claims[0].Row(), `claims gen.pb.go · "written by make gen this turn" · generated · dirty`; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	if err := ts.CheckClaims(claim); err != nil {
		t.Fatalf("a dirty path's claim is put to the card: %v", err)
	}

	// Nobody has answered it, so the record does not hold it, and the
	// stager refuses it by name exactly as it refuses any other path.
	if _, err := ts.Execute(context.Background(), GitWriteToolName, claim); err == nil ||
		!strings.Contains(err.Error(), "gen.pb.go is not in this session's record") {
		t.Fatalf("an unanswered claim staged: %v", err)
	}

	// The answer records it; now it stages, and says what kind of work it is.
	held = []string{gen}
	out, err := ts.Execute(context.Background(), GitWriteToolName, claim)
	if err != nil {
		t.Fatalf("an answered claim: %v", err)
	}
	if out != "staged 1 file · 1 generated" {
		t.Fatalf("add answered %q", out)
	}

	// A path exactly as HEAD has it has nothing in it to claim.
	clean := json.RawMessage(`{"verb":"add","claim":[{"path":"main.go","why":"make gen"}]}`)
	if err := ts.CheckClaims(clean); err == nil || !strings.Contains(err.Error(), "main.go shows no change") {
		t.Fatalf("a clean path's claim must be refused before the card: %v", err)
	}
	// A claim with no reason is no question at all.
	if _, err := ts.Claims(json.RawMessage(`{"verb":"add","claim":[{"path":"gen.pb.go","why":" "}]}`)); err == nil {
		t.Fatal("a claim with no why must be refused")
	}
	// And only add takes one.
	if _, err := ts.Execute(context.Background(), GitWriteToolName,
		json.RawMessage(`{"verb":"commit","message":"x","claim":[{"path":"gen.pb.go","why":"x"}]}`)); err == nil ||
		!strings.Contains(err.Error(), "commit does not take claim") {
		t.Fatalf("commit with a claim: %v", err)
	}
}
