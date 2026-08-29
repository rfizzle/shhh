package chat

// The working scope in a session. A session is scoped to the
// directory it was opened in, and everything the agent does inside it is
// judged by the permission mode alone. Reaching outside it is a second
// question — is this directory part of the work? — and it is asked the same
// way every other permission question is asked: on the approval card, with
// the directory named, answered by a key or by the user typing /add-dir
// before it ever comes up.
//
// Answering yes puts the directory in the scope for the rest of the session,
// which is what makes the answer stick: the sandbox's write grants follow the
// scope, so a contained command can finally write where the user said the
// work is. Two kinds of directory do not come along. One that only a person
// may grant — a home directory, a system root, another tool's credential
// store — is never granted by a mode or the classifier, however permissive
// the session is. One behind the containment deny mask cannot be granted at
// all, and the call is refused rather than asked about, because approving it
// would promise something the sandbox would go on refusing.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/scope"
)

// WithScope wires the session's working scope. A session without one treats
// every path as in scope, which is what sessions did before this existed.
func (m Model) WithScope(sc *scope.Scope) Model {
	m.scope = sc
	return m
}

// scopeReach is what one pending decision reaches outside the working scope.
// A decision that stays inside it resolves to the zero value, and every
// surface reads that as "nothing to say".
type scopeReach struct {
	// dirs are the directories outside the scope, in the order the action
	// named them.
	dirs []string
	// class is the strictest classification among them: an ordinary
	// directory a permissive mode may grant, a sensitive one only a person
	// may, or one that cannot be granted at all.
	class scope.Class
	// reason is why class says what it does, as a phrase printed after a dash.
	reason string
}

func (r scopeReach) any() bool { return len(r.dirs) > 0 }

// first is the directory a one-line surface names — the card's value, the
// grant note — with the rest counted after it.
func (r scopeReach) first() string {
	if len(r.dirs) == 0 {
		return ""
	}
	return r.dirs[0]
}

// scopeReachFor resolves what a decision reaches outside the scope. An edit
// answers for the file it writes; a command answers for the paths the radius
// resolver could account for, and for nothing it could not — a command shhh
// cannot read is not one this check can put a directory name to, and the card
// already says that in the `touches` row.
func (m Model) scopeReachFor(req *approvalRequest) scopeReach {
	if m.scope == nil || req == nil {
		return scopeReach{}
	}
	var paths []string
	switch {
	case req.kind == approvalDiff && req.path != "":
		paths = []string{req.path}
	case req.command != "":
		paths = radius.WritePaths(req.command)
	}
	if len(paths) == 0 {
		return scopeReach{}
	}
	dirs := m.scope.Outside(paths...)
	if len(dirs) == 0 {
		return scopeReach{}
	}
	out := scopeReach{dirs: dirs}
	for _, d := range dirs {
		if class, reason := scope.Classify(d); class > out.class {
			out.class, out.reason = class, reason
		}
	}
	return out
}

// grantScope puts the directories a decision reaches into the working scope
// and returns the note saying so. It is called when the decision is approved
// — by a key or by a mode — because an approval that did not widen the scope
// would be an approval the sandbox goes on refusing: the write grants follow
// the scope, and nothing else.
func (m *Model) grantScope(reach scopeReach) string {
	if !reach.any() || reach.class == scope.Refused {
		return ""
	}
	var added []string
	for _, dir := range reach.dirs {
		if _, err := m.scope.Add(dir); err == nil {
			added = append(added, displayDir(dir))
		}
	}
	if len(added) == 0 {
		return ""
	}
	return "Added to the working scope: " + strings.Join(added, ", ") +
		". Contained commands can write there now; /add-dir drop takes it back."
}

// scopeCommand is /add-dir: the grant made in front of no particular
// decision. Bare, it lists the scope; with a path, it adds one; `drop` takes
// one back.
func (m *Model) scopeCommand(parts []string) string {
	if m.scope == nil {
		return "This session has no working scope."
	}
	if len(parts) < 2 {
		return m.scopeStatus()
	}
	if parts[1] == "drop" {
		if len(parts) != 3 {
			return "Usage: /add-dir drop <path>"
		}
		dir, ok := m.scope.Drop(parts[2])
		if !ok {
			return "Not in the working scope: " + parts[2] + ". /add-dir lists what is."
		}
		return "Dropped " + dir + " from the working scope. Contained commands can no longer write there."
	}
	if len(parts) > 2 {
		return "Usage: /add-dir [<path>|drop <path>]  — one directory at a time; a path with spaces needs no quotes here."
	}
	dir, err := m.scope.Add(parts[1])
	switch {
	case err == scope.ErrAlreadyInScope:
		return dir + " is already in the working scope."
	case err != nil:
		return "Error: " + err.Error()
	}
	class, reason := scope.Classify(dir)
	note := "Added " + dir + " to the working scope: edits there no longer ask about leaving it, and contained commands can write there."
	if class == scope.Sensitive {
		note += "\nThis is a sensitive directory — " + reason + ". Nothing else would have granted it; /add-dir drop " + dir + " takes it back."
	}
	return note
}

// scopeStatus is bare /add-dir: what the session may reach, and the two
// commands that change it.
func (m Model) scopeStatus() string {
	if m.scope == nil {
		return "This session has no working scope."
	}
	var sb strings.Builder
	sb.WriteString("Working scope:\n")
	fmt.Fprintf(&sb, "  session    %s\n", m.scope.Root())
	dirs := m.scope.Dirs()
	for _, d := range dirs {
		class, reason := scope.Classify(d)
		line := "  added      " + d
		if class == scope.Sensitive {
			line += " — sensitive: " + reason
		}
		sb.WriteString(line + "\n")
	}
	if len(dirs) == 0 {
		sb.WriteString("  added      (none)\n")
	}
	sb.WriteString("Anything outside it asks before it is written to, whatever the mode.\n")
	sb.WriteString("/add-dir <path> adds one; /add-dir drop <path> takes it back.")
	return sb.String()
}
