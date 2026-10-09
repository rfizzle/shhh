package chat

// The session's grants: what [a] and /permissions allow have recorded, the
// one value they are read as, the listing that names them and the way back
// that takes them away. Which grant the card offers is grant.go's; what the
// policy decides with them is policy.go's.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
)

// grants is the session's grants as one value, for the surfaces that carry
// all of them: /permissions revoke and the status listing. The turn's own are
// deliberately not in it — what they answer is the same, but how long they
// answer it for is not, and every reader of this value has to say so.
func (m Model) grants() agent.Grants {
	return agent.Grants{
		AllEdits:      m.policy.allEdits,
		AllCommands:   m.policy.allCommands,
		EditDirs:      m.policy.editDirs,
		Commands:      m.policy.commands,
		EditPaths:     m.policy.editPaths,
		ExactCommands: m.policy.exactCommands,
		Hosts:         m.policy.hosts,
	}
}

// liveGrants is every grant standing right now, the turn's and the session's
// in one value, for the surfaces that decide on a grant's strength rather
// than on how long it lasts: a child under this session, and the fetcher that
// answers a redirect. They lose a turn grant when the parent pushes its
// grants again at the turn's close, which is the same seam that expires it
// here (subagents.go, close.go).
func (m Model) liveGrants() agent.Grants {
	g, t := m.grants(), m.policy.turn
	if !t.Any() {
		return g
	}
	g.AllEdits, g.AllCommands = g.AllEdits || t.AllEdits, g.AllCommands || t.AllCommands
	g.EditDirs = concatGrants(g.EditDirs, t.EditDirs)
	g.Commands = concatGrants(g.Commands, t.Commands)
	g.EditPaths = concatGrants(g.EditPaths, t.EditPaths)
	g.ExactCommands = concatGrants(g.ExactCommands, t.ExactCommands)
	g.Hosts = concatGrants(g.Hosts, t.Hosts)
	return g
}

// concatGrants joins two grant lists without writing into either: the
// session's own slice is handed out by grants(), and appending to it in place
// would extend the session's grants with the turn's the first time the
// capacity allowed it.
func concatGrants(session, turn []string) []string {
	if len(turn) == 0 {
		return session
	}
	out := make([]string, 0, len(session)+len(turn))
	return append(append(out, session...), turn...)
}

// grantHost records a fetch grant of the length the reader chose: the host
// the card named, exactly. A host already reachable — from the config list or
// an earlier grant — adds nothing, so choosing the same row twice on the same
// site records it once.
//
// It takes no narrow width because a host grant is already the narrowest
// there is: this host and not its parent domain, and not a sibling under it
// (docs/capabilities/approvals-and-safety.md#a-host-is-granted-once).
func (m *Model) grantHost(host string, o grantOffer) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if o.length == forThisTurn {
		if !agent.HostMatches(m.hostAllowlist(), host) && !m.policy.turn.CoversHost(host) {
			m.policy.turn.Hosts = append(m.policy.turn.Hosts, host)
		}
		return host
	}
	// A host the turn already reaches is still recorded for the session:
	// this grant is longer than that one, and skipping it would take the
	// length the reader chose away because a shorter grant happened to
	// cover the same host today.
	if !agent.HostMatches(m.hostAllowlist(), host) {
		m.policy.hosts = append(m.policy.hosts, host)
	}
	return host
}

// grantRole records a spawn grant: this role starts without a card for the
// rest of the session, exactly as a granted host fetches without one
// (docs/capabilities/approvals-and-safety.md#a-read-only-role-is-granted-once).
// A role already granted adds nothing, so pressing the same row twice records
// it once.
//
// It takes no length. The card offers the session and no turn beside it —
// grantOffers says why — so there is nothing here to choose between.
func (m *Model) grantRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	if !m.roleGranted(role) {
		m.policy.roles = append(m.policy.roles, role)
	}
	return role
}

// roleGranted reports whether spawning this role has been waved through for
// the session.
func (m Model) roleGranted(role string) bool {
	return slices.Contains(m.policy.roles, role)
}

// grantCommand records a command grant of the length and width the reader
// chose: the command's leading words, which pre-approve the shape of it
// rather than every command there is, or the line exactly as it stands. A
// pattern already covered adds nothing, so choosing the same row twice on the
// same shape of command records it once.
//
// It reports what was granted in the words the row printed, which is what the
// transcript then keeps — quoted for the prefix and the exact line, because a
// multi-word grant read unquoted in a sentence is two grants.
func (m *Model) grantCommand(command string, o grantOffer) string {
	if o.exact {
		line := strings.TrimSpace(command)
		if line == "" {
			return ""
		}
		if o.length == forThisTurn {
			if !m.commandGranted(line) && !m.policy.turn.CoversCommand(line) {
				m.policy.turn.ExactCommands = append(m.policy.turn.ExactCommands, line)
			}
			return strconv.Quote(line)
		}
		if !m.commandGranted(line) {
			m.policy.exactCommands = append(m.policy.exactCommands, line)
		}
		return strconv.Quote(line)
	}
	prefix := agent.GrantPrefix(command)
	if prefix == "" {
		return ""
	}
	if o.length == forThisTurn {
		if !m.commandGranted(prefix) && !m.policy.turn.CoversCommand(prefix) {
			m.policy.turn.Commands = append(m.policy.turn.Commands, prefix)
		}
		return strconv.Quote(prefix)
	}
	if !m.commandGranted(prefix) {
		m.policy.commands = append(m.policy.commands, prefix)
	}
	return strconv.Quote(prefix)
}

// commandGranted reports whether the session already runs this line without
// asking — by config, by a prefix grant, or by an exact one. It is the one
// question every command grant asks before recording itself, so the two
// widths cannot disagree about what is already covered.
//
// It is the session's and not the turn's, and the asymmetry is the point. A
// grant the session already covers is nothing to record at either length. A
// grant the *turn* covers still records at session length, because that grant
// is the longer of the two and dropping it would take away the length the
// reader chose. So the turn's recorders ask both sets and the session's ask
// only this one.
func (m Model) commandGranted(command string) bool {
	return agent.AllowlistMatches(m.allowlist(), command) ||
		agent.ExactMatches(m.policy.exactCommands, command)
}

// grantEdit records an edit grant of the length and width the reader chose:
// the directory the file lives in, which is the scope a reader approving a
// file in it has actually looked at, or that one file alone.
func (m *Model) grantEdit(path string, o grantOffer) string {
	if o.exact {
		file := strings.TrimSpace(path)
		if file == "" {
			return ""
		}
		if o.length == forThisTurn {
			if !m.editGranted(file) && !m.policy.turn.CoversEdit(file) {
				m.policy.turn.EditPaths = append(m.policy.turn.EditPaths, file)
			}
			return file
		}
		if !m.editGranted(file) {
			m.policy.editPaths = append(m.policy.editPaths, file)
		}
		return file
	}
	dir := filepath.Dir(path)
	if dir == "" {
		return ""
	}
	// A directory is tested by a path inside it, because the grant covers
	// what is under a directory rather than the directory itself.
	inside := filepath.Join(dir, "x")
	if o.length == forThisTurn {
		if !m.editGranted(inside) && !m.policy.turn.CoversEdit(inside) {
			m.policy.turn.EditDirs = append(m.policy.turn.EditDirs, dir)
		}
		return displayDir(dir)
	}
	if !agent.PathUnder(m.policy.editDirs, inside) {
		m.policy.editDirs = append(m.policy.editDirs, dir)
	}
	return displayDir(dir)
}

// editGranted reports whether this file already applies without asking, by a
// directory grant or by a grant of the file itself. It reads the session's
// grants alone, for the reason commandGranted does.
func (m Model) editGranted(path string) bool {
	return agent.PathUnder(m.policy.editDirs, path) || agent.PathIs(m.policy.editPaths, path)
}

// revokeGrants drops every grant this session has made — the standing ones
// and the ones that were going to end with the turn — and reports what went,
// in the order the status lines name them. A turn grant goes with the rest
// because /permissions revoke is the way back for every grant, and one that
// survived it on the grounds that it was going to expire anyway would be a
// grant the reader could not take back. Config's own allowlist is untouched:
// it is not this session's to take back.
func (m *Model) revokeGrants() []string {
	var gone []string
	if m.policy.allEdits {
		gone = append(gone, "every edit")
	}
	if m.policy.allCommands {
		gone = append(gone, "every command")
	}
	for _, d := range m.policy.editDirs {
		gone = append(gone, "edits in "+displayDir(d))
	}
	for _, p := range m.policy.editPaths {
		gone = append(gone, "edits to "+p)
	}
	gone = append(gone, quoteAll(m.policy.commands)...)
	gone = append(gone, quoteAll(m.policy.exactCommands)...)
	for _, h := range m.policy.hosts {
		gone = append(gone, "fetches from "+h)
	}
	gone = append(gone, m.revokeRoles()...)
	m.policy.allEdits, m.policy.allCommands = false, false
	m.policy.editDirs, m.policy.commands, m.policy.hosts = nil, nil, nil
	m.policy.editPaths, m.policy.exactCommands = nil, nil
	return append(gone, m.revokeTurnGrants()...)
}

// revokeRoles drops the spawn grants and names them. There is no turn-length
// half to take with them: a role is granted for the session or not at all
// (policyState).
func (m *Model) revokeRoles() []string {
	var gone []string
	for _, r := range m.policy.roles {
		gone = append(gone, rolePlural(r)+" starting without a card")
	}
	m.policy.roles = nil
	return gone
}

// revokeTurnGrants drops what this turn granted and names it, for the two
// callers that take a grant back on purpose. The expiry at the turn's close
// is the other route in and says nothing (expireTurnGrants).
func (m *Model) revokeTurnGrants() []string {
	t := m.policy.turn
	if !t.Any() {
		return nil
	}
	var gone []string
	for _, d := range t.EditDirs {
		gone = append(gone, "edits in "+displayDir(d)+" this turn")
	}
	for _, p := range t.EditPaths {
		gone = append(gone, "edits to "+p+" this turn")
	}
	for _, c := range append(append([]string(nil), t.Commands...), t.ExactCommands...) {
		gone = append(gone, strconv.Quote(c)+" this turn")
	}
	for _, h := range t.Hosts {
		gone = append(gone, "fetches from "+h+" this turn")
	}
	m.policy.turn = agent.Grants{}
	return gone
}

// expireTurnGrants ends every grant made for the length of this turn.
//
// It says nothing. The grant named its end when it was made — that is the
// whole point of the list the key opens — so a row announcing the expiry
// would be the session telling the reader something they were told at the
// moment they chose it, in the middle of the block that closes the turn.
// See docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends.
func (m *Model) expireTurnGrants() {
	if !m.policy.turn.Any() {
		return
	}
	m.policy.turn = agent.Grants{}
	// The children hold their own copy, so the expiry has to reach them the
	// way the grant did (subagents.go).
	m.syncGrants()
}

// noteGrant puts what [a] just granted into the transcript. A grant that is
// not said is a grant nobody can revoke: the card is gone a frame later, and
// the vitals chip can only say that something was granted, not what.
func (m *Model) noteGrant(text string) {
	m.appendEntry(entry{kind: entrySystem, text: text})
}

// quoteAll quotes a run of command grants for a sentence that lists them, so
// a multi-word grant reads as one thing rather than as two.
func quoteAll(cmds []string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = strconv.Quote(c)
	}
	return out
}

// grantStatus is `/permissions grants`: everything this session has stopped
// asking about, and the one line that takes it back. It names each grant in
// the same words the card used to record it, so the two can be recognised as
// the same act.
func (m Model) grantStatus() string {
	g := m.grants()
	if !g.Any() && !m.policy.turn.Any() && len(m.policy.roles) == 0 && len(m.policy.allowlist) == 0 && len(m.policy.denylist) == 0 &&
		len(m.policy.allowHosts) == 0 && len(m.policy.denyHosts) == 0 && len(m.scopeDirs()) == 0 && len(m.writerDirs()) == 0 {
		return "nothing is granted — every gated call asks.\n" +
			"[a] on a confirm prompt offers the grants that call can make, each with when it ends; /permissions allow <commands|edits> grants the category"
	}
	var sb strings.Builder
	sb.WriteString("Grants:\n")
	if g.AllEdits {
		sb.WriteString("  edits      every edit, anywhere (/permissions allow edits) — " + endsWithSession + "\n")
	}
	if g.AllCommands {
		sb.WriteString("  commands   every command (/permissions allow commands) — " + endsWithSession + "\n")
	}
	// Every grant is listed with when it ends, in the words the row that made
	// it printed: a listing that named a grant differently from the card
	// would read as a second grant rather than as the same one, and a grant
	// whose end is not stated is one the reader has to remember (grant.go).
	writeGrants(&sb, g, endsWithSession)
	// The roles are written here rather than in writeGrants because they are
	// not one of the two sets that function exists to render the same way:
	// there is no turn-length half of a role grant to keep in step with
	// (policyState).
	for _, r := range m.policy.roles {
		sb.WriteString("  agents     " + rolePlural(r) + " — " + endsWithSession + "\n")
	}
	writeGrants(&sb, m.policy.turn, endsWithTurn)
	if !g.Any() && !m.policy.turn.Any() && len(m.policy.roles) == 0 {
		sb.WriteString("  (none — everything below came from config)\n")
	}
	for _, d := range m.scopeDirs() {
		sb.WriteString("  scope      " + displayDir(d) + " — in the working scope (/add-dir drop takes it back)\n")
	}
	for _, d := range m.writerDirs() {
		sb.WriteString("  writers    " + displayDir(d) + " — for writers; the session already had it (/add-dir drop takes it back)\n")
	}
	if n := len(m.policy.allowlist); n > 0 {
		fmt.Fprintf(&sb, "  config     %s from behavior.command_allowlist — not this session's to revoke\n", plural(n, "command pattern"))
	}
	if n := len(m.policy.denylist); n > 0 {
		fmt.Fprintf(&sb, "  config     %s from behavior.command_denylist — refused before anything here can allow them\n", plural(n, "command pattern"))
	}
	for _, h := range m.policy.allowHosts {
		sb.WriteString("  config     " + h + " from web.allow_hosts — not this session's to revoke\n")
	}
	if n := len(m.policy.denyHosts); n > 0 {
		fmt.Fprintf(&sb, "  config     %s from web.deny_hosts — refused before anything here can allow them\n", plural(n, "host"))
	}
	if g.Any() || len(m.policy.roles) > 0 {
		sb.WriteString("/permissions revoke [edits|commands|hosts|agents] takes them back.")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// writeGrants lists one set of scoped grants, each row ending in the words
// its own end was granted under. It is one function over both sets rather
// than a loop per kind inside the listing because every one of these rows
// says the same thing about when it ends, and a kind that acquired a
// different phrase for it would be a second answer to the one question this
// listing exists to answer.
//
// The blanket grants are not here: they have no pattern to print and a
// sentence of their own naming the command that set them.
func writeGrants(sb *strings.Builder, g agent.Grants, ends string) {
	end := " — " + ends + "\n"
	for _, d := range g.EditDirs {
		sb.WriteString("  edits      " + displayDir(d) + end)
	}
	for _, p := range g.EditPaths {
		sb.WriteString("  edits      " + p + ", that file alone" + end)
	}
	for _, c := range g.Commands {
		sb.WriteString("  commands   " + strconv.Quote(c) + end)
	}
	for _, c := range g.ExactCommands {
		sb.WriteString("  commands   " + strconv.Quote(c) + ", that line alone" + end)
	}
	for _, h := range g.Hosts {
		sb.WriteString("  hosts      " + h + ", that host alone" + end)
	}
}

// allowCommand is `/permissions allow <commands|edits>`: the blanket grant,
// which used to be one keystroke on a card. It is a command now because of
// what it is — a decision about every call the rest of the session will make,
// taken once, in front of no particular one of them.
func (m *Model) allowCommand(args []string) string {
	if len(args) != 1 {
		return "usage: /permissions allow <commands|edits> — the blanket grants. For one shape of call, [a] on its confirm prompt"
	}
	switch args[0] {
	case "commands", "cmds":
		if m.policy.allCommands {
			return "commands already run without asking. /permissions revoke commands takes it back"
		}
		m.policy.allCommands = true
		m.syncGrants()
		return "every command will now run without asking, except the safety-flagged ones, which always ask.\n/permissions revoke commands takes it back"
	case "edits":
		if m.policy.allEdits {
			return "edits already apply without asking. /permissions revoke edits takes it back"
		}
		m.policy.allEdits = true
		m.syncGrants()
		return "every edit will now apply without asking, anywhere in the workspace.\n/permissions revoke edits takes it back"
	}
	return "usage: /permissions allow <commands|edits>"
}

// revokeCommand is `/permissions revoke`: the way back a session grant never
// had. Until it existed, [a] pressed once on one `go test` was the last time
// the session asked about anything, and only restarting it — or plan mode,
// which refuses everything — undid that.
func (m *Model) revokeCommand(args []string) string {
	if len(args) > 1 {
		return "usage: /permissions revoke [edits|commands|hosts|agents]"
	}
	scope := "all"
	if len(args) == 1 {
		scope = args[0]
	}
	var gone []string
	switch scope {
	case "all":
		gone = m.revokeGrants()
	// Each of the three takes the turn's grants of that kind with the
	// session's: what the reader named is a kind, and a grant of that kind
	// left standing because it was going to expire on its own is a grant the
	// revoke they typed did not take back.
	case "edits":
		if m.policy.allEdits {
			gone = append(gone, "every edit")
		}
		for _, d := range m.policy.editDirs {
			gone = append(gone, "edits in "+displayDir(d))
		}
		for _, p := range m.policy.editPaths {
			gone = append(gone, "edits to "+p)
		}
		m.policy.allEdits, m.policy.editDirs, m.policy.editPaths = false, nil, nil
		for _, d := range m.policy.turn.EditDirs {
			gone = append(gone, "edits in "+displayDir(d)+" this turn")
		}
		for _, p := range m.policy.turn.EditPaths {
			gone = append(gone, "edits to "+p+" this turn")
		}
		m.policy.turn.AllEdits, m.policy.turn.EditDirs, m.policy.turn.EditPaths = false, nil, nil
	case "commands", "cmds":
		if m.policy.allCommands {
			gone = append(gone, "every command")
		}
		gone = append(gone, quoteAll(m.policy.commands)...)
		gone = append(gone, quoteAll(m.policy.exactCommands)...)
		m.policy.allCommands, m.policy.commands, m.policy.exactCommands = false, nil, nil
		for _, c := range append(append([]string(nil), m.policy.turn.Commands...), m.policy.turn.ExactCommands...) {
			gone = append(gone, strconv.Quote(c)+" this turn")
		}
		m.policy.turn.AllCommands, m.policy.turn.Commands, m.policy.turn.ExactCommands = false, nil, nil
	case "hosts", "host":
		for _, h := range m.policy.hosts {
			gone = append(gone, "fetches from "+h)
		}
		m.policy.hosts = nil
		for _, h := range m.policy.turn.Hosts {
			gone = append(gone, "fetches from "+h+" this turn")
		}
		m.policy.turn.Hosts = nil
	case "agents", "roles":
		gone = m.revokeRoles()
	default:
		return "usage: /permissions revoke [edits|commands|hosts|agents]"
	}
	m.syncGrants()
	if len(gone) == 0 {
		return "nothing was granted; everything already asks"
	}
	return "revoked, and asking again: " + strings.Join(gone, " · ")
}
