package chat

// The approval paths, pinned through the program as a person drives it.
//
// What a gated call comes to depends on the permission mode, the classifier,
// the lists and the reader's keys, and the code that decides it is going to
// move behind a seam (the one approver assembly). These tests are the
// behaviour that move must pass unchanged. Each one records the two things
// a person and the model can observe of a call: what the screen offered and
// drew, and what reached the agent as the call's result. They press keys and
// send messages and read no function of the policy, so they survive the code
// moving.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// The words each gated call is scripted with, so a case names what it asked
// for and the assertions read the same everywhere.
const (
	approvalCommand = "npm run deploy -- --tag latest"
	approvalBefore  = "const limit = 25"
	approvalAfter   = "const limit = 50"
	approvalClosing = "Carrying on."
)

// approvalOutcome is everything a case observed of one gated call.
type approvalOutcome struct {
	ran   []string // commands the runner was handed
	told  string   // the call's result, as the next request carried it
	card  string   // the frame while the card waited, "" when none did
	frame string   // the frame the session ended on
	file  string   // the edited file as it ended
}

// approvalRig is the session a case runs: one gated call (a command or an
// edit), the mode reached with shift+tab presses, and the standing answers.
type approvalRig struct {
	edit    bool
	presses int
	judge   provider.Provider // the classifier's provider, nil for none
	deny    []string          // the deny list the host hands the screen
	explain provider.Provider // the explainer's provider, nil for none
	command string            // the command asked, approvalCommand when empty
	ranOK   string            // what the runner prints
	follow  []programTurn     // the turns after the call, approvalClosing when empty
}

// approvalDriver is a run in flight: the program, what the runner was
// handed, the provider that saw the requests and the file the edit targets.
type approvalDriver struct {
	t    *testing.T
	tm   *program
	p    *programProvider
	ran  *[]string
	loop string
}

// start builds the session and the program for the rig.
func (r approvalRig) start(t *testing.T) *approvalDriver {
	t.Helper()
	d := &approvalDriver{t: t, ran: new([]string)}
	command := r.command
	if command == "" {
		command = approvalCommand
	}
	turns := append([]programTurn{commandTurn(nil, command)}, r.after()...)
	w := Wiring{CommandDenylist: r.deny}
	if r.edit {
		dir := fixtureDir(t, map[string]string{"loop.go": "package agent\n\n" + approvalBefore + "\n"})
		d.loop = filepath.Join(dir, "loop.go")
		turns[0] = editTurn(dir, "loop.go", approvalBefore, approvalAfter)
		w.Workspace = dir
		w.Executor = subagent.RootedExecutor(dir, tools.Execute)
	}
	m, p := scriptedSessionWith(w, turns...)
	d.p = p
	m.wiring.Runner = legacyRunner(func(_ context.Context, cmd string) (string, int) {
		*d.ran = append(*d.ran, cmd)
		return r.output(), 0
	})
	if r.judge != nil {
		m.classifier.judge = agent.NewClassifier(r.judge, agent.ClassifierConfig{Model: "judge"})
	}
	if r.explain != nil {
		m.wiring.Explainer = agent.NewExplainer(r.explain, agent.ExplainConfig{Model: "small", Prompt: explainWording})
	}
	d.tm = runProgram(t, m)
	for i := 0; i < r.presses; i++ {
		programPress(t, d.tm, "shift+tab")
	}
	return d
}

func (r approvalRig) after() []programTurn {
	if len(r.follow) > 0 {
		return r.follow
	}
	return []programTurn{{text: approvalClosing}}
}

func (r approvalRig) output() string {
	if r.ranOK != "" {
		return r.ranOK
	}
	return "deployed"
}

// told is the result the model was handed for the call: the last message of
// the request that followed it.
func (d *approvalDriver) told() string {
	d.p.mu.Lock()
	defer d.p.mu.Unlock()
	if len(d.p.asked) < 2 {
		d.t.Fatal("the model was never asked again after the call")
	}
	return d.p.asked[len(d.p.asked)-1].Content
}

// finish ends the program and reads what the case observed.
func (d *approvalDriver) finish(card string) approvalOutcome {
	d.t.Helper()
	waitForText(d.t, d.tm, approvalClosing)
	out := approvalOutcome{card: card, ran: *d.ran, told: d.told()}
	out.frame = finalFrame(d.t, d.tm)
	if d.loop != "" {
		body, err := os.ReadFile(d.loop)
		if err != nil {
			d.t.Fatal(err)
		}
		out.file = string(body)
	}
	return out
}

// asked drives a call that puts a card to the reader: it waits for the
// card, records it, hands the keyboard over and answers with key.
func (d *approvalDriver) asked(wait string, key ...string) approvalOutcome {
	d.t.Helper()
	send(d.tm, "go")
	waitForText(d.t, d.tm, wait)
	card := *d.tm.frame.Load()
	d.tm.Send(programHandover)
	programPress(d.t, d.tm, key...)
	return d.finish(card)
}

// unasked drives a call the session answers itself.
func (d *approvalDriver) unasked() approvalOutcome {
	d.t.Helper()
	send(d.tm, "go")
	return d.finish("")
}

// wants fails for every phrase the text does not carry, naming what it is.
func wants(t *testing.T, what, text string, phrases ...string) {
	t.Helper()
	for _, s := range phrases {
		if !strings.Contains(text, s) {
			t.Fatalf("%s does not carry %q:\n%s", what, s, text)
		}
	}
}

// bare fails for every phrase the text carries, naming what it is.
func bare(t *testing.T, what, text string, phrases ...string) {
	t.Helper()
	for _, s := range phrases {
		if strings.Contains(text, s) {
			t.Fatalf("%s carries %q:\n%s", what, s, text)
		}
	}
}

// The offers a command card makes, and an edit card, before anything is
// pressed.
const (
	commandOffers = "[y] run it once · [n] deny"
	editOffers    = "[y] apply the change · [n] deny"
)

const (
	declinedCommand = "error: the user declined to run this command"
	declinedEdit    = "error: the user declined this tool call"
)

// One gated call in each permission mode, recording what the screen offered
// and what the agent was told. Manual asks about everything; accept-edits
// applies an edit and asks about a command; auto lets the classifier answer
// a command, and a no or a failure puts the question to the reader — nothing
// fails open; read-only and plan refuse both without a card.
// See docs/capabilities/approvals-and-safety.md#the-five-modes.
func TestApprovalRouteByMode(t *testing.T) {
	const refusedCmd = "is not an inspection command"
	yes := &judgeProvider{verdicts: [][2]string{{"allow", "the task asked for this"}}}
	no := &judgeProvider{verdicts: [][2]string{{"deny", "the task asked for a release check and this publishes one"}}}
	down := &paragraphProvider{err: fmt.Errorf("down")}

	t.Run("manual asks about a command", func(t *testing.T) {
		got := (approvalRig{}).start(t).asked(commandOffers, "n")
		wants(t, "the card", got.card, "Approve command", "$ "+approvalCommand)
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
		wants(t, "the frame", got.frame, "denied · you")
	})
	t.Run("manual asks about an edit", func(t *testing.T) {
		got := (approvalRig{edit: true}).start(t).asked(editOffers, "y")
		wants(t, "the card", got.card, approvalAfter)
		wants(t, "the file", got.file, approvalAfter)
		wants(t, "the result", got.told, "Edited ", "1 replacement(s)")
	})
	t.Run("accept-edits asks about a command", func(t *testing.T) {
		got := (approvalRig{presses: 1}).start(t).asked(commandOffers, "n")
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
		wants(t, "the frame", got.frame, "⏵⏵ accept edits", "denied · you")
	})
	t.Run("accept-edits applies an edit without a card", func(t *testing.T) {
		got := (approvalRig{edit: true, presses: 1}).start(t).unasked()
		wants(t, "the file", got.file, approvalAfter)
		bare(t, "the file", got.file, approvalBefore)
		wants(t, "the result", got.told, "Edited ", "1 replacement(s)")
	})
	t.Run("auto with a classifier yes runs the command without a card", func(t *testing.T) {
		got := (approvalRig{presses: 2, judge: yes}).start(t).unasked()
		if len(got.ran) != 1 || got.ran[0] != approvalCommand {
			t.Fatalf("ran %v", got.ran)
		}
		if got.told != "exit code: 0\noutput:\ndeployed" {
			t.Fatalf("the model was told %q", got.told)
		}
		wants(t, "the frame", got.frame, "⏵⏵ auto")
		bare(t, "the frame", got.frame, "denied")
	})
	t.Run("auto with a classifier no asks, with the reason", func(t *testing.T) {
		got := (approvalRig{presses: 2, judge: no}).start(t).asked("classifier: the task asked for a release check", "n")
		wants(t, "the card", got.card, "not now — it keeps waiting", commandOffers)
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
	})
	t.Run("auto with a classifier error asks", func(t *testing.T) {
		got := (approvalRig{presses: 2, judge: down}).start(t).asked("classifier unavailable", "n")
		wants(t, "the card", got.card, "asking you instead", commandOffers)
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("a failed classifier did not fail closed: ran %v, told %q", got.ran, got.told)
		}
	})
	t.Run("auto applies an edit without a card", func(t *testing.T) {
		got := (approvalRig{edit: true, presses: 2, judge: yes}).start(t).unasked()
		wants(t, "the file", got.file, approvalAfter)
	})
	for _, mode := range []struct {
		name    string
		presses int
		word    string
	}{{"read-only", 3, "read-only mode"}, {"plan", 4, "plan mode"}} {
		t.Run(mode.name+" refuses a command without a card", func(t *testing.T) {
			got := (approvalRig{presses: mode.presses}).start(t).unasked()
			if len(got.ran) != 0 {
				t.Fatalf("a refused command ran: %v", got.ran)
			}
			wants(t, "the result", got.told, "error: this session is in "+mode.word, refusedCmd)
			wants(t, "the frame", got.frame, "blocked · "+mode.word)
		})
		t.Run(mode.name+" refuses an edit without a card", func(t *testing.T) {
			got := (approvalRig{edit: true, presses: mode.presses}).start(t).unasked()
			wants(t, "the result", got.told, "error: this session is in "+mode.word+"; the call was not executed.")
			wants(t, "the file", got.file, approvalBefore)
			bare(t, "the file", got.file, approvalAfter)
			wants(t, "the frame", got.frame, "blocked · "+mode.word)
		})
	}
}

// raised sends the keyboard to the waiting card and presses keys on it.
func (d *approvalDriver) raised(keys ...string) {
	d.t.Helper()
	d.tm.Send(programHandover)
	programPress(d.t, d.tm, keys...)
}

// request is the n-th message the model was asked with at its last place:
// what each round put in front of it.
func (d *approvalDriver) request(n int) string {
	d.t.Helper()
	d.p.mu.Lock()
	defer d.p.mu.Unlock()
	if n >= len(d.p.asked) {
		d.t.Fatalf("the model was asked %d times, not %d", len(d.p.asked), n+1)
	}
	return d.p.asked[n].Content
}

func (d *approvalDriver) requests() int {
	d.p.mu.Lock()
	defer d.p.mu.Unlock()
	return len(d.p.asked)
}

// The answers the protocol does not carry today, which the screen gives and
// the model hears of only as a result or a sentence: an always-allow grant
// and the next call it answers, an amended command, a note on a yes or a no,
// the dry run and the explanation, which decide nothing.
// See docs/capabilities/approvals-and-safety.md#a-no-can-say-why-and-a-yes-can-say-what-next.
func TestApprovalRouteBeyondAllowDeny(t *testing.T) {
	const (
		first   = "npm test --watch"
		second  = "npm test --coverage"
		other   = "npm publish --tag next"
		granted = "exit code: 0\noutput:\ndeployed"
	)
	// Three calls in one turn: the one the grant is made on, one a grant
	// over the same leading words would answer, and one it would not.
	three := approvalRig{command: first, follow: []programTurn{
		commandTurn(nil, second), commandTurn(nil, other), {text: approvalClosing},
	}}
	for _, tc := range []struct {
		name       string
		down       int
		offer      string
		note       string
		secondAsks bool
		rail       string
	}{
		{"for the turn", 0, "until this turn ends", `Commands starting "npm test" will run without asking until this turn ends.`, false, ""},
		{"for the session", 1, "until you revoke it", `Commands starting "npm test" will run without asking.`, false, "granted: 1 command"},
		{"for the exact line", 2, "until you revoke it", "", true, "granted: 1 command"},
	} {
		t.Run("a grant "+tc.name+" answers the next matching call", func(t *testing.T) {
			d := three.start(t)
			send(d.tm, "go")
			waitForText(t, d.tm, "$ "+first)
			d.raised("a")
			waitForAll(t, d.tm, "allow without asking", tc.offer)
			for range tc.down {
				programPress(t, d.tm, "down")
			}
			programPress(t, d.tm, "enter")
			if tc.secondAsks {
				waitForText(t, d.tm, "$ "+second)
				d.raised("n")
			}
			waitForText(t, d.tm, "$ "+other)
			card := *d.tm.frame.Load()
			d.raised("n")
			got := d.finish(card)

			wantRan := []string{first, second}
			if tc.secondAsks {
				wantRan = []string{first}
			}
			if strings.Join(got.ran, "|") != strings.Join(wantRan, "|") {
				t.Fatalf("the runner was handed %v, want %v", got.ran, wantRan)
			}
			if r := d.request(1); r != granted {
				t.Fatalf("the model was told %q of the call it granted on", r)
			}
			if tc.secondAsks {
				if r := d.request(2); r != declinedCommand {
					t.Fatalf("the exact-line grant answered a different line: told %q", r)
				}
			} else if r := d.request(2); r != granted {
				t.Fatalf("the matching call was not answered by the grant: told %q", r)
			}
			if r := d.request(3); r != declinedCommand {
				t.Fatalf("a call the grant does not cover was not asked about: told %q", r)
			}
			if tc.rail != "" {
				wants(t, "the frame", got.frame, tc.rail)
			} else {
				bare(t, "the frame", got.frame, "granted: ")
			}
			if tc.note != "" {
				wants(t, "the frame", got.frame, tc.note)
			}
		})
	}

	t.Run("leaving the grant list grants nothing", func(t *testing.T) {
		got := (approvalRig{command: first}).start(t).asked("$ "+first, "a", "esc", "n")
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
		bare(t, "the frame", got.frame, "granted: ")
	})

	t.Run("an amended command runs in place of the one asked", func(t *testing.T) {
		d := (approvalRig{command: first}).start(t)
		send(d.tm, "go")
		waitForText(t, d.tm, "$ "+first)
		d.tm.Send(programHandover)
		waitForText(t, d.tm, "[e] edit the command")
		card := *d.tm.frame.Load()
		programPress(t, d.tm, "e")
		waitForGone(t, d.tm, "[e] edit the command")
		d.tm.Type(" --runInBand")
		programPress(t, d.tm, "enter")
		got := d.finish(card)
		if len(got.ran) != 1 || got.ran[0] != first+" --runInBand" {
			t.Fatalf("the runner was handed %v", got.ran)
		}
		wants(t, "the frame", got.frame, "--runInBand")
		if want := "The user edited this command before it ran: " + first + " --runInBand ran in place of " + first +
			".\nexit code: 0\noutput:\ndeployed"; got.told != want {
			t.Fatalf("the model was told %q, want %q", got.told, want)
		}
	})

	// The note is the draft: esc gives the keyboard back with the request
	// waiting, the sentence is typed where every sentence is, and the answer
	// takes it.
	t.Run("a note on a no is the whole of what the model is told", func(t *testing.T) {
		d := (approvalRig{command: first}).start(t)
		send(d.tm, "go")
		waitForText(t, d.tm, "$ "+first)
		programPress(t, d.tm, "esc")
		d.tm.Type("use make test instead")
		waitForText(t, d.tm, "use make test instead")
		d.raised("n")
		got := d.finish("")
		if len(got.ran) != 0 || got.told != "error: use make test instead" {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
		wants(t, "the frame", got.frame, "use make test instead")
	})

	t.Run("a note on a yes joins the conversation after the result", func(t *testing.T) {
		d := (approvalRig{command: first}).start(t)
		send(d.tm, "go")
		waitForText(t, d.tm, "$ "+first)
		programPress(t, d.tm, "esc")
		d.tm.Type("and keep the output short")
		waitForText(t, d.tm, "and keep the output short")
		d.raised("y")
		got := d.finish("")
		if len(got.ran) != 1 || got.ran[0] != first {
			t.Fatalf("ran %v", got.ran)
		}
		d.p.mu.Lock()
		note := d.p.asked[1]
		d.p.mu.Unlock()
		if note.Role != provider.RoleUser || note.Content != "and keep the output short" {
			t.Fatalf("the note reached the model as %q %q", note.Role, note.Content)
		}
		if got.told != note.Content {
			t.Fatalf("the last thing the model read was %q", got.told)
		}
	})

	// A dry run and an explanation each answer the reader and nobody else:
	// the card stays, the decision is still to be made, and nothing the
	// screen showed reached the model.
	const risky = "rsync -a --delete src/ dst/"
	t.Run("a dry run runs the harmless form, decides nothing and tells the model nothing", func(t *testing.T) {
		d := (approvalRig{command: risky, ranOK: "would delete dst/old.go"}).start(t)
		send(d.tm, "go")
		waitForText(t, d.tm, "$ "+risky)
		d.tm.Send(programHandover)
		waitForText(t, d.tm, "[t] dry run — see what it would do")
		programPress(t, d.tm, "t")
		waitForText(t, d.tm, "would delete dst/old.go")
		screen := *d.tm.frame.Load()
		wants(t, "the dry run's screen", screen, "dry run — ")
		if n := d.requests(); n != 1 {
			t.Fatalf("the model was asked %d times while the dry run was read", n)
		}
		programPress(t, d.tm, "esc")
		waitForText(t, d.tm, "[y] run it once")
		d.raised("n")
		got := d.finish("")
		if len(got.ran) != 1 || got.ran[0] == risky || !strings.Contains(got.ran[0], "dry-run") {
			t.Fatalf("the runner was handed %v, want one dry form of the line", got.ran)
		}
		if got.told != declinedCommand {
			t.Fatalf("the model was told %q", got.told)
		}
		bare(t, "the result", got.told, "would delete")
	})
	t.Run("an explanation is read, decides nothing and tells the model nothing", func(t *testing.T) {
		const paragraph = "rsync copies src/ into dst/ and removes anything in dst/ that src/ lacks."
		d := (approvalRig{command: risky, explain: &paragraphProvider{text: paragraph}}).start(t)
		send(d.tm, "go")
		waitForText(t, d.tm, "$ "+risky)
		d.tm.Send(programHandover)
		waitForText(t, d.tm, "[enter] full view, and what it does")
		programPress(t, d.tm, "enter")
		waitForText(t, d.tm, paragraph)
		wants(t, "the full view", *d.tm.frame.Load(), "Approve command", risky,
			"Nothing on this screen was sent to the model waiting on your answer.")
		if n := d.requests(); n != 1 {
			t.Fatalf("the model was asked %d times while the explanation was read", n)
		}
		programPress(t, d.tm, "esc")
		waitForText(t, d.tm, "[y] run it once")
		d.raised("n")
		got := d.finish("")
		if len(got.ran) != 0 || got.told != declinedCommand {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
		bare(t, "the result", got.told, "rsync copies")
	})
}

// denyListFromSettings is the deny list a session is handed when the person
// refuses "npm publish" everywhere and the checkout's settings add
// "terraform apply": the union, built by the loader the host uses.
func denyListFromSettings(t *testing.T, checkout string) []string {
	t.Helper()
	dir := fixtureDir(t, map[string]string{".shhh/config.toml": checkout})
	user := config.Config{}
	user.Behavior.CommandDenylist = []string{"npm publish"}
	cfg, _, err := config.LayerProject(user, filepath.Join(dir, ".shhh", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Behavior.CommandDenylist
}

// A command the deny list names is answered before the mode is asked, in
// every mode: no card, no classifier round, nothing run, and the model told
// the rule refused it rather than the mode. A checkout's settings can add a
// refusal and never take one away, so the person's own still holds beside
// the checkout's.
// See docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow.
func TestDenyAnsweredBeforeMode(t *testing.T) {
	// The classifier would say yes to anything; a deny list that is read
	// first never asks it.
	yes := &judgeProvider{verdicts: [][2]string{{"allow", "the task asked for this"}}}
	for _, checkout := range []struct {
		name    string
		toml    string
		refused []string
	}{
		{"a checkout that adds a refusal", "[behavior]\ncommand_denylist = [\"terraform apply\"]\n",
			[]string{"npm publish --tag next", "terraform apply -auto-approve"}},
		{"a checkout that tries to empty the list", "[behavior]\ncommand_denylist = []\n",
			[]string{"npm publish --tag next"}},
	} {
		deny := denyListFromSettings(t, checkout.toml)
		for _, mode := range []struct {
			name    string
			presses int
			judge   provider.Provider
		}{
			{"manual", 0, nil},
			{"accept-edits", 1, nil},
			{"auto", 2, yes},
			{"read-only", 3, nil},
			{"plan", 4, nil},
		} {
			for _, command := range checkout.refused {
				t.Run(checkout.name+", "+mode.name+": "+command, func(t *testing.T) {
					got := (approvalRig{command: command, presses: mode.presses, judge: mode.judge, deny: deny}).start(t).unasked()
					if len(got.ran) != 0 {
						t.Fatalf("a command on the deny list ran: %v", got.ran)
					}
					wants(t, "the result", got.told, "error: this command is on the deny list for this session. "+
						"It is refused in every permission mode and no approval can allow it")
					wants(t, "the frame", got.frame, "run "+command, "blocked · deny list")
					bare(t, "the frame", got.frame, "read-only mode", "plan mode", "denied · you")
					bare(t, "the result", got.told, "this session is in")
				})
			}
		}
	}
	t.Run("a command off the list still meets its mode", func(t *testing.T) {
		deny := denyListFromSettings(t, "[behavior]\ncommand_denylist = [\"terraform apply\"]\n")
		got := (approvalRig{command: "npm test", deny: deny}).start(t).asked(commandOffers, "n")
		if got.told != declinedCommand || len(got.ran) != 0 {
			t.Fatalf("ran %v, told %q", got.ran, got.told)
		}
	})
}
