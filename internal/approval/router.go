// Package approval is the standing rules every door a gated call comes
// through asks before it answers: the person's deny lists, what a command
// destroys that no session may, the containment requirement, and what a call
// reaches outside the working scope.
//
// There are two doors. The screen puts a call it cannot answer to a person on
// a card; a run with nobody in front of it — a scripted run, a served session
// — answers from its flags, its judge, or a refusal. What each door answers
// once the rules have had their say is its own: grants, the mode and the card
// on the screen; --yes, --allow, the judge and the flat refusal on the other.
// What the rules answer is not, and it is written here once so a rule added
// for one door cannot be missing at the other.
// See docs/capabilities/approvals-and-safety.md#a-deny-list-is-answered-before-anything-can-allow.
package approval

import (
	"os"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/tools"
)

// Router is the standing rules of one session, built from what the session
// was given: the two deny lists from its configuration, and its containment —
// the working scope, the directory it runs in, and the refusal it gives every
// command where it was told to contain them and cannot. The zero value is a
// session with no rules, which refuses nothing.
//
// It holds no grant and no mode. Those answer a question the rules have left
// open, and only the screen has anybody to make one.
type Router struct {
	// Denylist is behavior.command_denylist: command prefixes refused in
	// every mode, under every flag.
	Denylist []string
	// DenyHosts is web.deny_hosts: hosts no fetch reaches.
	DenyHosts []string
	// Scope is the session's working scope; nil is a surface with none.
	Scope *scope.Scope
	// Workspace is the directory the session's commands run in where it is
	// not the scope's root. Empty is the scope's root.
	Workspace string
	// Containment is the refusal every command gets where the session was
	// told to contain its commands and nothing can; empty where it can.
	Containment string
}

// Call is one gated call as the rules read it. Each door builds it with
// CallOf from the call's classification; nothing here reads a tool's
// arguments.
type Call struct {
	// Command is the line the deny list is matched against: the command a
	// command runs, a process start's, and the line a tool with a closed
	// verb set stands for (a git write's `git commit`).
	Command string
	// Host is the host a fetch leaves for, matched against the host deny
	// list. A call with a host is a fetch, and the command lists do not
	// answer it.
	Host string
	// Write marks a call at the write tier: its line is matched against the
	// deny list, and what it destroys is not read, because it is not a
	// command the model wrote.
	Write bool
	// InDir marks a command run in the session's own directory. A process
	// start names a directory of its own, so its relative paths prove
	// nothing and only its absolute ones are read.
	InDir bool
	// Runs marks a call that runs a command — the command tool and a
	// process start — which is the whole of what the containment
	// requirement is about.
	Runs bool
}

// CallOf is a classified call as the rules read it, and the one place a Call
// is filled in. Each door classifies the call it holds, adds what only it
// can read — the host a fetch leaves for — and asks here, so a field added
// to Call is filled at both doors or at neither.
//
// A command typed for the command tool runs in the session's own directory;
// a process start names one of its own; a call at the write tier carries its
// line for the deny list alone.
func CallOf(name string, c agent.Classified) Call {
	runs := c.Tier == agent.TierCommand && c.Action.Kind == agent.ActionCommand
	return Call{
		Command: c.Action.Command,
		Host:    c.Action.Host,
		Write:   c.Tier == agent.TierWrite,
		InDir:   runs && name == tools.ExecCommandName,
		Runs:    runs,
	}
}

// Refusal is a rule's answer: what the model is told, the rule name the row
// carries, and the code the record files it under.
type Refusal struct {
	Result string
	Reason string
	Code   string
}

// Contained is the refusal a call gets because the session cannot contain
// the command it runs, or "" where it runs. A git write carries a line only
// for the deny list to match, and it is not a command the assistant wrote, so
// it is never refused here.
// See docs/capabilities/containment.md#a-git-write-is-not-a-command.
func (r Router) Contained(c Call) string {
	if r.Containment == "" || c.Command == "" || !c.Runs {
		return ""
	}
	return r.Containment
}

// Rule is the refusal no mode, grant, flag or judge reaches: the host deny
// list for a fetch; for a command line, the deny list and then a destroying
// command pointed at something this session may not destroy. ok is false
// where none of them answers.
//
// A refused host and a refused command are the same act and told apart only
// in what the model reads: a host is refused whatever the URL, and a retry
// with another path is the loop that wording exists to stop.
// See docs/capabilities/approvals-and-safety.md#some-targets-are-never-destroyed.
func (r Router) Rule(c Call) (Refusal, bool) {
	if c.Host != "" {
		if agent.HostMatches(r.DenyHosts, c.Host) {
			return Refusal{Result: agent.DeniedHostResult, Reason: agent.DenyReasonHost, Code: observe.ReasonDenylist}, true
		}
		return Refusal{}, false
	}
	if c.Command == "" {
		return Refusal{}, false
	}
	reason, ok := agent.RuleRefusal(r.Denylist, agent.Action{Command: c.Command, Irreplaceable: r.Irreplaceable(c)})
	if !ok {
		return Refusal{}, false
	}
	code := observe.ReasonDenylist
	if agent.IsIrreplaceable(reason) {
		// Filed with the safety table's refusals: it is that table's
		// destroying rows, read against where they point.
		code = observe.ReasonSafety
	}
	return Refusal{Result: agent.RuleRefusedResult(reason), Reason: reason, Code: code}, true
}

// Irreplaceable is what a call's command destroys that this session may not,
// in the words the refusal names it with, or "".
func (r Router) Irreplaceable(c Call) string {
	if c.Command == "" || c.Write || c.Host != "" {
		return ""
	}
	return radius.Destroys(c.Command, r.where(c.InDir)).Refusal()
}

// Scratch reports whether a call's command is a delete of untracked scratch
// inside the workspace, read against the same place Irreplaceable reads it.
// See docs/capabilities/approvals-and-safety.md#severity-moves-the-default.
func (r Router) Scratch(c Call) bool {
	if c.Command == "" || c.Write || c.Host != "" {
		return false
	}
	return radius.ScratchDelete(c.Command, r.where(c.InDir))
}

// where is where a command is read for what it destroys: the working scope,
// its root, the directory the command runs in where the call says, and the
// home directory. A session with no scope and no workspace reads from the
// directory shhh runs in.
func (r Router) where(inDir bool) radius.Where {
	w := radius.Where{Scope: r.Scope, Root: r.Workspace}
	if r.Scope != nil {
		w.Root = r.Scope.Root()
	}
	if w.Root == "" {
		w.Root, _ = os.Getwd()
	}
	if inDir {
		w.Dir = w.Root
		if r.Workspace != "" {
			w.Dir = r.Workspace
		}
	}
	w.Home, _ = os.UserHomeDir()
	return w
}

// Reached is one directory a call reaches outside the working scope, and what
// it is: an ordinary directory, one only a person may grant, or one behind the
// containment deny mask that nothing grants.
type Reached struct {
	Dir    string
	Class  scope.Class
	Reason string
}

// Outside is what these paths reach outside the working scope, in the order
// they named it, each with its class. Each door reads it at its own moment —
// the screen before its card, an unattended run after its flags — and the
// refusal of a directory behind the deny mask is the same refusal either way.
// See docs/capabilities/containment.md#two-classes-of-directory-never-come-along.
func (r Router) Outside(paths ...string) []Reached {
	if r.Scope == nil || len(paths) == 0 {
		return nil
	}
	dirs := r.Scope.Outside(paths...)
	if len(dirs) == 0 {
		return nil
	}
	out := make([]Reached, len(dirs))
	for i, d := range dirs {
		class, reason := scope.Classify(d)
		out[i] = Reached{Dir: d, Class: class, Reason: reason}
	}
	return out
}

// Policy is a mode policy with these rules in it: the two deny lists the
// policy reads before its mode, filled from here rather than by each caller,
// so a policy built for a door cannot be missing a list the door holds.
func (r Router) Policy(p agent.ModePolicy) agent.ModePolicy {
	p.CommandDenylist = r.Denylist
	p.DenyHosts = r.DenyHosts
	return p
}
