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
	// Every byte a process writes goes through this pass, and the gap the
	// searches buy is what pays for a table this size: measured together on
	// one host, the automata alone read a log with no credential in it at
	// about 4 MB/s and the searches turn the same text away at about
	// 190 MB/s. Output with no credential in it is the ordinary case, and
	// this is what keeps the ordinary case cheap — so a row that arrives
	// without a marker costs every reader of every log, not just its own.
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
		// GitLab's personal access tokens carry their own prefix and are
		// twenty characters after it. `gldt-` and `glrt-` are the deploy and
		// runner variants of the same format; only the personal one is here,
		// because it is the one a coding session meets in a CI file.
		kind:    "gitlab-token",
		markers: []string{"glpat-"},
		re:      regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),
	},
	{
		// Slack's tokens are xox + one letter for the kind + a hyphen, and
		// the rest is digits, letters and hyphens. The letters are the ones
		// Slack actually issues, which deliberately excludes `o`: `xoxo-`
		// is a sign-off, and a chat log is exactly the kind of text this
		// runs over. Ten characters of tail is the floor, only so that
		// `xoxb-` written in prose is not a match. `xapp-` is the same
		// issuer's app-level token, which authenticates a socket-mode
		// connection rather than a workspace call and so never appears with
		// an `xox` prefix.
		kind:    "slack-token",
		markers: []string{"xox", "xapp-"},
		re:      regexp.MustCompile(`\b(?:xox[abeprs]|xapp)-[0-9A-Za-z-]{10,}`),
	},
	{
		// Anthropic's keys are `sk-ant-` plus the key material, and this is
		// the one family shhh most needs to catch: its own provider key is
		// in the environment of the session it is scrubbing, and a `.env` in
		// the project is the ordinary place a second one sits.
		kind:    "anthropic-key",
		markers: []string{"sk-ant-"},
		re:      regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{24,}`),
	},
	{
		// OpenAI's keys are the loosest marker here — `sk-` is three
		// characters, and it is also how a kebab-case identifier starts — so
		// the alphabet does the work the prefix cannot. The bare form is
		// `sk-` and then base62 alone, which no hyphenated slug can be; the
		// project, service-account and admin forms carry their own word
		// first, so hyphens after that word are the issuer's and not a
		// slug's. Thirty-two is the floor for both, well under the 48 the
		// bare form actually has and well over anything a name reaches.
		kind:    "openai-key",
		markers: []string{"sk-"},
		re:      regexp.MustCompile(`\bsk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{32,}|\bsk-[A-Za-z0-9]{32,}\b`),
	},
	{
		// A Google API key is `AIza` and 35 more characters; the length is
		// the pattern, since there is no delimiter and no checksum to lean
		// on. The floor is exact and the ceiling is not, because a pattern
		// that stops at 39 characters inside a longer run leaves the tail of
		// the token in the clear right after the placeholder — which reads
		// as redacted and is not. Redacting a few characters that were never
		// part of the key is the failure worth having.
		kind:    "google-api-key",
		markers: []string{"AIza"},
		re:      regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35,255}`),
	},
	{
		// Stripe's live secret and restricted keys. The test-mode prefixes
		// (`sk_test_`, `rk_test_`) are deliberately absent: they authorise
		// nothing real, and a test key redacted out of a payment integration
		// is a model debugging a request it cannot see.
		kind:    "stripe-key",
		markers: []string{"sk_live_", "rk_live_"},
		re:      regexp.MustCompile(`\b[sr]k_live_[A-Za-z0-9]{16,}\b`),
	},
	{
		// An npm automation or granular token: `npm_` and 36 base62
		// characters, the format every token issued since 2021 has.
		kind:    "npm-token",
		markers: []string{"npm_"},
		re:      regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),
	},
	{
		// SendGrid's key is three dot-separated parts, and the first is the
		// literal `SG`. Both tails are required and long, because `SG.` on
		// its own is an abbreviation and a sentence can end in one. The last
		// part has a floor and no useful ceiling, for the reason the Google
		// row has one: a bounded tail stops mid-token and leaves the rest
		// beside the placeholder.
		kind:    "sendgrid-key",
		markers: []string{"SG."},
		re:      regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{20,24}\.[A-Za-z0-9_-]{40,255}`),
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
	{
		// The catch-all for the ones with no marker of their own: a token
		// nobody prefixed still has to travel in an Authorization header, and
		// the header word is the marker. It runs last so that a family with
		// its own name keeps it — a bearer JWT comes back `[redacted:jwt]`,
		// which is the more useful answer.
		//
		// Twenty characters of the header alphabet is what keeps `bearer`
		// written in prose out of it: the word is followed by a word, and a
		// twenty-character unbroken run of base64 and URL punctuation is not
		// one. The word itself goes into the placeholder along with the
		// token, because the alternative is a capture group and the reader
		// loses nothing — the line still says `authorization:`.
		kind:    "bearer-token",
		markers: []string{"earer"},
		re:      regexp.MustCompile(`\b[Bb]earer[ \t]+[A-Za-z0-9._~+/=-]{20,}`),
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
