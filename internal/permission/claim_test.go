package permission

import "testing"

// A claim is answered by somebody looking at it: a mode that applies edits
// asks about one instead of letting it through, and a refusal still stands.
func TestDecideAsksAboutAClaimItWouldHaveAllowed(t *testing.T) {
	edit := Action{Kind: ActionEdit, Command: "git add"}
	claimed := edit
	claimed.Claims = []string{`claims gen.pb.go · "make gen" · dirty`}
	for _, mode := range []Mode{ModeAcceptEdits, ModeAuto} {
		p := ModePolicy{Mode: mode}
		if d, _ := p.Decide(edit); d != Allow {
			t.Fatalf("%v: the plain add should be allowed, got %v", mode, d)
		}
		if d, why := p.Decide(claimed); d != Ask || why != ClaimAsksReason {
			t.Fatalf("%v: a claim was answered by the mode: %v %q", mode, d, why)
		}
	}
	if d, _ := (ModePolicy{Mode: ModePlan}).Decide(claimed); d != Deny {
		t.Fatalf("plan mode must still refuse a claimed add, got %v", d)
	}
}
