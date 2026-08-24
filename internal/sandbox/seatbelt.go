package sandbox

import (
	"fmt"
	"os"
	"strings"
)

const seatbeltPath = "/usr/bin/sandbox-exec"

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
func seatbeltProfile(s spec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
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
	if !s.network {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// seatbeltPrefix builds the sandbox-exec invocation up to the contained
// command.
func seatbeltPrefix(s spec) []string {
	return []string{seatbeltPath, "-p", seatbeltProfile(s)}
}

func seatbeltArgv(s spec, command string) []string {
	return append(seatbeltPrefix(s), s.shell, "-c", command)
}

// sbplQuote renders a path as an SBPL string literal.
func sbplQuote(p string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
}
