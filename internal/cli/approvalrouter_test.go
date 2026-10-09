package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/approval"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/web"
)

// The screen and an unattended run answer a gated call from one set of
// standing rules — the deny lists, what a command destroys, the containment
// requirement and the working scope's refusal — so a call one of those rules
// answers is answered alike at both doors: refused, filed under the same
// code, and, where the rule's own words reach the model on both, in the same
// words. A call no rule answers is left to each door, and neither refuses it
// by a rule.
//
// The unattended run is given --yes, the widest answer it has, so a rule it
// reads after its flags — the working scope — is reached; the one fetch row
// is a conversation's, the run whose host deny list is read through a policy
// rather than behind a flag.
func TestApprovalRouter_ScreenAndHeadlessAgreeOnEveryRule(t *testing.T) {
	base := t.TempDir()
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{filepath.Join(home, ".ssh"), ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init failed (%v): %s", err, out)
	}
	st := structural.NewToolset(ws)
	if st == nil {
		t.Skip("no workspace root")
	}
	st.AllowWrites(structural.Writes{Files: func() []string { return nil }})
	if !st.Has(structural.GitWriteToolName) {
		t.Skip("git is not available here")
	}
	webTools := web.NewToolset(web.NewFetcher(web.Policy{AllowPrivate: true}), nil)
	procSup := newTestProcessSupervisor(t)
	denylist := []string{"npm publish", "git commit"}
	denyHosts := []string{"evil.test"}
	refusal := "error: this session requires containment and none is available"

	gitCommit := provider.ToolCall{ID: "c1", Name: structural.GitWriteToolName, Arguments: `{"verb":"commit","message":"feat: do it"}`}
	gitBranch := provider.ToolCall{ID: "c1", Name: structural.GitWriteToolName, Arguments: `{"verb":"branch","name":"topic"}`}
	write := func(path string) provider.ToolCall {
		args, _ := json.Marshal(map[string]string{"path": path, "content": "x\n"})
		return provider.ToolCall{ID: "c1", Name: tools.WriteFileName, Arguments: string(args)}
	}
	fetch := func(url string) provider.ToolCall {
		return provider.ToolCall{ID: "c1", Name: web.FetchToolName, Arguments: `{"url":"` + url + `"}`}
	}

	const none = ""
	for _, c := range []struct {
		name string
		call provider.ToolCall
		// rule is the rule that answers, none where no rule does.
		rule string
		// code is what both doors file the refusal under.
		code string
		// sameWords is a refusal whose words are the rule's on both doors;
		// the scope's are each door's own.
		sameWords    bool
		contained    bool
		conversation bool
	}{
		{name: "a command on the deny list", call: execCall("npm publish --tag latest"),
			rule: agent.DenyReasonDenylist, code: observe.ReasonDenylist, sameWords: true},
		{name: "a process start on the deny list", call: processStartCall("ship", "npm publish"),
			rule: agent.DenyReasonDenylist, code: observe.ReasonDenylist, sameWords: true},
		{name: "a git write the deny list names", call: gitCommit,
			rule: agent.DenyReasonDenylist, code: observe.ReasonDenylist, sameWords: true},
		{name: "a command destroying the home directory", call: execCall(`rm -rf "$HOME"`),
			rule: agent.DenyReasonIrreplaceable, code: observe.ReasonSafety, sameWords: true},
		{name: "a command destroying the workspace root", call: execCall("rm -rf ."),
			rule: agent.DenyReasonIrreplaceable, code: observe.ReasonSafety, sameWords: true},
		{name: "a process start destroying the filesystem root", call: processStartCall("wipe", "rm -rf /"),
			rule: agent.DenyReasonIrreplaceable, code: observe.ReasonSafety, sameWords: true},
		{name: "a fetch to a refused host", call: fetch("https://evil.test/page"), conversation: true,
			rule: agent.DenyReasonHost, code: observe.ReasonDenylist, sameWords: true},
		{name: "a command where nothing contains it", call: execCall("go version"), contained: true,
			rule: "containment", sameWords: true},
		{name: "a process start where nothing contains it", call: processStartCall("srv", "go version"), contained: true,
			rule: "containment", sameWords: true},
		{name: "an edit behind the deny mask", call: write(filepath.Join(home, ".ssh", "config")),
			rule: "scope", code: observe.ReasonOutOfScope},
		{name: "a command no rule names", call: execCall("go version"), rule: none},
		{name: "an edit inside the scope", call: write(filepath.Join(ws, "notes.txt")), rule: none},
		{name: "a git write the deny list does not name", call: gitBranch, rule: none},
		{name: "a git write where nothing contains commands", call: gitBranch, contained: true, rule: none},
	} {
		t.Run(c.name, func(t *testing.T) {
			containment := ""
			if c.contained {
				containment = refusal
			}

			// The screen, in its default mode, wired with the same lists.
			screenScope, errs := scope.New(ws)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			w := chat.Wiring{
				CommandDenylist: denylist,
				DenyHosts:       denyHosts,
				Scope:           screenScope,
				Workspace:       ws,
				Containment:     chat.Containment{Refusal: containment},
				Runner:          func(context.Context, string) tools.ExecResult { return tools.ExecResult{} },
				Processes:       chat.Processes{Manage: func([]string) string { return "" }},
				GatedTools: map[string]chat.GatedPreviewFunc{
					structural.GitWriteToolName: func(args json.RawMessage) (chat.GatedPreview, error) {
						return gitWriteGatedPreview(st, args)
					},
					web.FetchToolName: func(args json.RawMessage) (chat.GatedPreview, error) {
						plan, err := webTools.FetchPlan(args)
						if err != nil {
							return chat.GatedPreview{}, err
						}
						return chat.GatedPreview{Action: "fetch", Summary: plan.Host, Host: plan.Host}, nil
					},
				},
			}
			screen := chat.New(nil, nil, w)
			sDecision, sResult, sCode := screen.StandingAnswer(c.call)

			// The unattended run, built on the same rules.
			runScope, errs := scope.New(ws)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			var hDecision, hCode string
			record := func(decision, reason string) { hDecision, hCode = decision, reason }
			rules := approval.Router{Denylist: denylist, DenyHosts: denyHosts, Scope: runScope, Containment: containment}
			un := unattended{}
			if c.conversation {
				un.conversation = conversationReads(true, rules.DenyHosts)
			}
			var ran []string
			resolve := headlessApprover(context.Background(), headlessApproval{
				opts: printOpts{yes: true}, rules: rules, run: fakeRun(&ran), record: record,
				webTools: webTools, procSup: procSup, structTools: st, un: un,
			})
			hResult := resolve(c.call)

			if c.rule == none {
				if sDecision == agent.Deny {
					t.Fatalf("the screen refused a call no rule names: %q (%s)", sResult, sCode)
				}
				if hDecision == observe.DecisionDeny {
					t.Fatalf("the unattended run refused a call no rule names: %q (%s)", hResult, hCode)
				}
				return
			}
			if sDecision != agent.Deny {
				t.Fatalf("the screen did not refuse it: %v", sDecision)
			}
			if len(ran) != 0 {
				t.Fatalf("the unattended run ran a refused command: %v", ran)
			}
			if c.rule == "containment" {
				if hDecision != "" || sCode != "" {
					t.Fatalf("a containment refusal was recorded: screen %q, run %q", sCode, hCode)
				}
			} else if hDecision != observe.DecisionDeny || hCode != c.code || sCode != c.code {
				t.Fatalf("filed as screen %q, run %s/%q; want both %q", sCode, hDecision, hCode, c.code)
			}
			if c.sameWords && sResult != hResult {
				t.Fatalf("the doors told the model different things:\nscreen: %s\nrun:    %s", sResult, hResult)
			}
		})
	}
}
