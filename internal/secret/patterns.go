package secret

// The second stage of the scrub: the credentials nobody declared.
//
// The vault answers "hide this value", which requires somebody to have named
// it first. The leak that actually happens is the one nobody saw coming — a
// key sitting in a config file the model reads, a bearer token in an API
// response, a private key printed by a script that was only supposed to check
// it exists. None of those were declared, so none of them are values the
// vault can look for.
//
// What they have instead is a shape. Every credential family worth
// recognising carries its own marker, because the services that issue them
// wanted them greppable in a leaked repository as much as anyone: a prefix, a
// fixed length, a delimiter line. That marker is the whole of the recognition
// here, and it is why the table is small — a pattern loose enough to catch an
// unmarked secret is a pattern that redacts ordinary text, and text redacted
// out of a build error is a model debugging something it cannot read.
// See docs/capabilities/secrets.md#the-shapes-it-knows-without-being-told.

import (
	"regexp"
	"strings"
)

// Redacted is what text matching a known shape becomes. It names the kind
// and not a secret, because nothing here knows whose credential it was —
// only what it looked like. A declared secret keeps its own placeholder:
// the vault's pass runs first and there is nothing left for a shape to
// match by the time this one runs.
func Redacted(kind string) string { return "[redacted:" + kind + "]" }

// shape is one credential family: the word its placeholder names, the text
// it is recognised by, and the literals no match of it can be missing.
type shape struct {
	kind string
	re   *regexp.Regexp
	// markers are the issuer's own literal prefixes, and every pattern here
	// is built around one — so text containing none of them cannot match,
	// and a substring search says so far faster than the automaton can.
	// Every byte a process writes goes through this pass: the automata
	// alone read a log with no credential in it at about 20 MB/s, and the
	// searches turn the same text away at about 660 MB/s. Output with no
	// credential in it is the ordinary case, and this is what keeps the
	// ordinary case cheap.
	markers []string
}

// matches reports whether s can contain this shape at all. A shape with no
// markers is always tried, because the alternative is a pattern that is
// silently never applied — the one failure in this file that leaks rather
// than over-redacts, and the one nothing on screen would show.
func (sh shape) matches(s string) bool {
	if len(sh.markers) == 0 {
		return true
	}
	for _, m := range sh.markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// shapes are the families this recognises, in the order they are applied.
// Each one is a marker the issuer put there, never a guess at entropy.
var shapes = []shape{
	{
		// A PEM block is the only shape here that spans lines, and it is
		// matched first and whole. Matching something inside the body
		// instead would leave the BEGIN and END lines wrapped around a
		// placeholder, which reads as a key that is still there. A block
		// whose END never arrived — output cut by a tail, a ring or a
		// truncated spool — is not matched at all; that is the same limit
		// every pattern here has, and the document says so.
		kind:    "private-key",
		markers: []string{"PRIVATE KEY"},
		re:      regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	},
	{
		// AKIA is a long-term AWS access key id and ASIA a temporary one:
		// the prefix plus sixteen more characters of the same alphabet. The
		// word boundaries are what keep twenty characters from being cut
		// out of the middle of a longer run of capitals. AWS's other
		// prefixes in this alphabet — AROA, AIDA, ANPA — identify a
		// principal rather than authenticate as one, so they are not here.
		kind:    "aws-access-key",
		markers: []string{"AKIA", "ASIA"},
		re:      regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	},
	{
		// Every GitHub credential issued since the 2021 format carries its
		// own prefix, so there is nothing to infer: gh[pousr]_ for the
		// classic tokens and github_pat_ for a fine-grained one. The upper
		// bounds are generous rather than exact because GitHub has
		// lengthened these before and a token that outgrew the pattern
		// would be redacted only in part, which is worse than not at all.
		kind:    "github-token",
		markers: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
		re:      regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,255}|github_pat_[A-Za-z0-9_]{80,255})\b`),
	},
	{
		// Slack's tokens are xox + one letter for the kind + a hyphen, and
		// the rest is digits, letters and hyphens. The letters are the ones
		// Slack actually issues, which deliberately excludes `o`: `xoxo-`
		// is a sign-off, and a chat log is exactly the kind of text this
		// runs over. Ten characters of tail is the floor, only so that
		// `xoxb-` written in prose is not a match.
		kind:    "slack-token",
		markers: []string{"xox"},
		re:      regexp.MustCompile(`\bxox[abeprs]-[0-9A-Za-z-]{10,}`),
	},
	{
		// A JWT is three base64url runs separated by dots — which is also
		// the shape of a Go import path, a dotted identifier and a version
		// string, and matching those would be the worst failure this file
		// can have. The header is what makes a JWT recognisable: it is
		// always a JSON object, so it always encodes to something starting
		// `eyJ`, which is the base64 of `{"`. Requiring that prefix is the
		// difference between redacting a bearer token and redacting
		// `github.com/rfizzle/shhh`.
		kind:    "jwt",
		markers: []string{"eyJ"},
		re:      regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{6,}`),
	},
}

// redact replaces every known credential shape in s with its placeholder.
// It is the pass that runs after the vault's own, so it never sees a
// declared value: that one is already the secret's placeholder, and the
// reader can still tell which secret it was.
func redact(s string) string {
	for _, sh := range shapes {
		if !sh.matches(s) {
			continue
		}
		s = sh.re.ReplaceAllLiteralString(s, Redacted(sh.kind))
	}
	return s
}
