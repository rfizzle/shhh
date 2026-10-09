package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/approval"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/scope"
)

// An unattended run refuses a destroying command pointed at something it may
// not destroy through the rule a session refuses it with, ahead of --yes and
// the allowlist, and files it with the safety table's refusals.
func TestHeadlessApprover_AnIrreplaceableTargetHoldsUnderYes(t *testing.T) {
	base := t.TempDir()
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	sc, errs := scope.New(ws)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var ran, codes []string
	record := func(decision, reason string) { codes = append(codes, decision+"/"+reason) }
	resolve := headlessApprover(context.Background(), headlessApproval{opts: printOpts{yes: true}, allowlist: []string{"rm"}, run: fakeRun(&ran), record: record, procSup: newTestProcessSupervisor(t), rules: approval.Router{Scope: sc}})

	for _, tc := range []struct {
		name, want string
		result     string
	}{
		{"a command", "your home directory", resolve(execCall(`rm -rf "$HOME"`))},
		{"a command at the workspace root", "the workspace root", resolve(execCall("rm -rf ."))},
		// A process start names a directory of its own, so only what it
		// names absolutely is proved.
		{"a process start", "the filesystem root", resolve(processStartCall("wipe", "rm -rf /"))},
	} {
		if !strings.Contains(tc.result, tc.want) || !strings.Contains(tc.result, "outside what this session may destroy") {
			t.Errorf("%s answered %q, want the irreplaceable-target refusal naming %q", tc.name, tc.result, tc.want)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("a refused command ran: %v", ran)
	}
	for _, c := range codes {
		if c != observe.DecisionDeny+"/"+observe.ReasonSafety {
			t.Fatalf("a rule refusal was filed as %q", c)
		}
	}
	// The deny list is the person's own answer and still names itself first.
	denied := headlessApprover(context.Background(), headlessApproval{opts: printOpts{yes: true}, rules: approval.Router{Denylist: []string{"rm"}, Scope: sc}, run: fakeRun(&ran)})
	if got := denied(execCall("rm -rf ~")); got != agent.DenylistResult {
		t.Fatalf("the deny list answered second: %q", got)
	}
}
