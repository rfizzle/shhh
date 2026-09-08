package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const seatbeltPath = "/usr/bin/sandbox-exec"

// envPath is the program that hands the contained command its environment.
// Seatbelt has no environment option of its own — the policy is about what a
// process may reach, not what it is told — so the allowlist is applied by
// starting from nothing and naming what survives, which is what bubblewrap's
// --clearenv does a step earlier.
const envPath = "/usr/bin/env"

// seatbeltNote records honestly that Apple has deprecated sandbox-exec; it
// still works and remains the only unprivileged containment mechanism on
// macOS. One caveat versus bubblewrap: Seatbelt denies reads of masked paths
// (they error) rather than presenting them as empty.
const seatbeltNote = "Seatbelt (sandbox-exec) — deprecated by Apple but functional; masked paths fail reads rather than reading empty"

func detectSeatbelt() Availability {
	if _, err := os.Stat(seatbeltPath); err != nil {
		return Availability{Mechanism: "sandbox-exec", Detail: "sandbox-exec not found at " + seatbeltPath}
	}
	return Availability{Mechanism: "sandbox-exec", OK: true, Detail: seatbeltNote}
}

// seatbeltProfile builds the SBPL policy. Later rules take precedence in
// SBPL, so the deny mask comes after the write allowances and outranks them.
//
// The host's temporary directories are denied *before* the write allowances,
// which is the same ordering bubblewrap gets from mounting the tmpfs before
// the binds: a grant of something under /tmp is what brings it back, and it
// brings back exactly what was granted. A grant has to be re-allowed for
// reading as well, because the deny took reads too and an allowance of writes
// does not undo that — the base is (allow default), so nothing else under
// /tmp needs saying.
func seatbeltProfile(s spec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	b.WriteString("(allow file-write*\n  (subpath \"/dev\"))\n")
	if len(s.tmpHidden) > 0 {
		b.WriteString("(deny file-read* file-write*")
		for _, t := range s.tmpHidden {
			fmt.Fprintf(&b, "\n  (subpath %s)", sbplQuote(t))
		}
		b.WriteString(")\n")
		if readable := tmpReadable(s); len(readable) > 0 {
			b.WriteString("(allow file-read*")
			for _, r := range readable {
				fmt.Fprintf(&b, "\n  (subpath %s)", sbplQuote(r))
			}
			b.WriteString(")\n")
		}
	}
	if len(s.write) > 0 {
		b.WriteString("(allow file-write*")
		for _, w := range s.write {
			fmt.Fprintf(&b, "\n  (subpath %s)", sbplQuote(w))
		}
		b.WriteString(")\n")
	}
	if len(s.denyDirs)+len(s.denyFiles) > 0 {
		b.WriteString("(deny file-read* file-write*")
		for _, d := range s.denyDirs {
			fmt.Fprintf(&b, "\n  (subpath %s)", sbplQuote(d))
		}
		for _, f := range s.denyFiles {
			fmt.Fprintf(&b, "\n  (literal %s)", sbplQuote(f))
		}
		b.WriteString(")\n")
	}
	if s.tmpdir != "" {
		// Last, because the session's scratch lives under the state
		// directory the fixed mask has just denied: SBPL gives the later rule
		// precedence, so this is what makes one session's temporary directory
		// its own and every other session's unreachable.
		fmt.Fprintf(&b, "(allow file-read* file-write*\n  (subpath %s))\n", sbplQuote(s.tmpdir))
	}
	if dirs := traversable(s); len(dirs) > 0 {
		// After every deny, because it reaches through them: an allowance
		// inside a mask is only an allowance if the path down to it can be
		// walked, and a deny of file-read* takes that walk with it.
		b.WriteString("(allow file-read-metadata")
		for _, d := range dirs {
			fmt.Fprintf(&b, "\n  (literal %s)", sbplQuote(d))
		}
		b.WriteString(")\n")
	}
	if s.agentSocket != "" {
		// The variable is already gone from the environment, but the path is
		// a convention as much as an address and a command that guessed it
		// would be talking to a key it cannot read. Connecting to a unix
		// socket is an outbound network operation in SBPL, so both the file
		// and the connect are denied.
		fmt.Fprintf(&b, "(deny file-read* file-write*\n  (literal %s))\n", sbplQuote(s.agentSocket))
		fmt.Fprintf(&b, "(deny network-outbound\n  (literal %s))\n", sbplQuote(s.agentSocket))
	}
	if !s.network {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// tmpReadable are the paths inside a hidden temporary directory that the deny
// must not take with it: the write grants, which were asked for by name, and a
// workspace or working directory that happens to live under /tmp without being
// one.
func tmpReadable(s spec) []string {
	var out []string
	inside := func(path string) bool {
		for _, t := range s.tmpHidden {
			if within(path, t) {
				return true
			}
		}
		return false
	}
	for _, w := range s.write {
		if inside(w) {
			out = append(out, w)
		}
	}
	for _, v := range s.tmpVisible {
		if inside(v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// traversable are the directories a denied subtree still has to answer a
// metadata read for: the ancestors of every path the profile allows back
// inside one, from the deny's own root down to the allowance's parent.
//
// SBPL denies by path, and a deny of file-read* over a directory takes
// `lstat` of that directory with it. Nothing that opens a file by name
// notices, because the kernel walks the path itself and a rule about a
// directory is not a rule about traversing it. A program that resolves the
// path *before* opening it does notice, and SQLite is one: its unix VFS
// builds a database's full pathname by lstat'ing every prefix in turn to find
// out whether any of them is a symbolic link, and treats any errno but ENOENT
// as fatal. A denial answers EPERM, so a database inside the session's own
// tmpdir — allowed, writable, with a plain file already written beside it —
// cannot be opened at all, and what comes back is SQLITE_CANTOPEN (14) with
// no mention of a sandbox. Every store a contained command opens is behind
// that: `go test` over a package with one, a tool keeping an index, anything
// built on SQLite.
//
// So the answer is a rule for the operation rather than a wider allowance.
// The ancestors get file-read-metadata and nothing else, which is what an
// lstat asks for: it says the directory is there and says nothing about what
// is in it, because listing one is file-read-data and stays denied.
// See docs/capabilities/containment.md#a-denial-arrives-as-the-commands-own-error.
func traversable(s spec) []string {
	denied := make([]string, 0, len(s.tmpHidden)+len(s.denyDirs))
	denied = append(denied, s.tmpHidden...)
	denied = append(denied, s.denyDirs...)
	allowed := tmpReadable(s)
	if s.tmpdir != "" {
		allowed = append(allowed, s.tmpdir)
	}
	var out []string
	for _, a := range allowed {
		for _, d := range denied {
			if a == d || !within(a, d) {
				continue
			}
			// Every deny root here is a real directory somewhere under the
			// home or the temporary directory, never "/" — which is worth
			// knowing, because within() reads "/" as a prefix of nothing and
			// a mask over the root would come out of this walk empty.
			for p := filepath.Dir(a); within(p, d); p = filepath.Dir(p) {
				if !slices.Contains(out, p) {
					out = append(out, p)
				}
				if p == d {
					break // the deny's own root is the last one that needs saying
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// seatbeltPrefix builds the sandbox-exec invocation up to the contained
// command, with the environment allowlist applied on the way in. Nothing but
// env sits between containment and the command: it is not a shell, so the
// command's own argv is still never parsed or re-quoted.
func seatbeltPrefix(s spec) []string {
	argv := []string{seatbeltPath, "-p", seatbeltProfile(s), envPath, "-i"}
	return append(argv, s.env...)
}

func seatbeltArgv(s spec, command string) []string {
	return append(seatbeltPrefix(s), s.shell, "-c", command)
}

// sbplQuote renders a path as an SBPL string literal.
func sbplQuote(p string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
}
