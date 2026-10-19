package ui

// The one-shot's provider failures (
// docs/interface/surfaces.md#the-recovery-row).
//
// `shhh cmd "find the big files"` is where most people meet the product, and
// it is where a raw provider error used to land hardest: one line of Go on
// stderr, no indication whether the key, the network or the account was the
// problem. The classification is the same one the session uses — that is the
// point of it living in github.com/rfizzle/llm — and so is the row it renders on.
//
// What differs is the offer. A session can hand you a key to press; a
// one-shot has already exited by the time you have read the row, so the way
// out is a command, and the row says the command instead of pretending a
// keystroke would reach it.

import (
	"strings"

	"github.com/rfizzle/llm/failure"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// failureReportWidth is what the report renders at. The one-shot has no
// layout of its own to measure, and the row's own fields are what matter.
const failureReportWidth = 88

// FailureReport renders a classified provider failure as the failure row,
// with the way out stated as a command. It reports false for an error that is
// not a provider failure, which the caller should keep handling as it always
// has.
func FailureReport(err error, model string) (string, bool) {
	f, ok := failure.As(err)
	if !ok {
		return "", false
	}
	row := components.RecoveryRow{
		State:     components.RecoveryBroken,
		Verb:      components.VerbModel,
		Subject:   failureSubject(f, model),
		Qualifier: f.Headline(),
		Outcome:   failureOutcome(f),
		Detail:    f.Detail(),
		MaxDetail: 3,
		Duration:  components.NoDuration,
		Note:      failureRemedy(f),
	}
	if f.Recoverable() {
		row.State = components.RecoveryStalled
	}
	if f.Class == failure.ClassCancelled {
		row.State = components.RecoveryStopped
	}
	return row.View(failureReportWidth), true
}

// FailureLine is the same failure for a terminal that is not one: one line,
// no chrome, still classified. A pipe gets the name of what went wrong and
// the provider's own words, and nothing that needs a cursor to read.
func FailureLine(err error) (string, bool) {
	f, ok := failure.As(err)
	if !ok {
		return "", false
	}
	line := "shhh: " + f.Headline()
	if f.Message != "" {
		line += " — " + strings.SplitN(f.Message, "\n", 2)[0]
	}
	return line, true
}

// failureSubject is the model that failed, or the provider when the caller
// never named a model.
func failureSubject(f *failure.Failure, model string) string {
	if model != "" {
		return model
	}
	return f.Provider
}

// failureOutcome is the right-aligned field: the one fact about this class
// that decides what to do next, never a repeat of the class itself.
func failureOutcome(f *failure.Failure) string {
	switch f.Class {
	case failure.ClassAuth:
		if f.KeyTail == "" {
			return "no key was sent"
		}
		return "key ···" + f.KeyTail + " rejected"
	case failure.ClassRateLimit:
		if f.RetryAfter > 0 {
			return "retry in " + components.FormatElapsed(f.RetryAfter)
		}
		return "retry shortly"
	case failure.ClassQuota:
		return "the account, not the rate"
	case failure.ClassOverloaded:
		return "the provider's side"
	case failure.ClassContextLength:
		return "over the window"
	case failure.ClassModelNotFound:
		return "the id, not the key"
	case failure.ClassNetwork:
		return "never reached it"
	case failure.ClassMalformed:
		return "unreadable reply"
	case failure.ClassCancelled:
		return components.OutcomeCancelled
	}
	return "message below"
}

// failureRemedy is the way out, as a command. Every one of these is something
// the reader can paste; none of them is a key, because there is no longer a
// program listening for one.
func failureRemedy(f *failure.Failure) string {
	switch f.Class {
	case failure.ClassAuth:
		hint := f.KeyEnv
		if hint == "" {
			hint = "SHHH_API_KEY"
		}
		return "export " + hint + ", or shhh config set --global provider.api_key <key>"
	case failure.ClassQuota:
		return "waiting will not clear this — shhh providers lists the alternatives"
	case failure.ClassRateLimit, failure.ClassOverloaded, failure.ClassNetwork:
		return "run it again, or --provider <name> to ask somewhere else"
	case failure.ClassContextLength:
		return "shorten the prompt, or pipe less into it"
	case failure.ClassModelNotFound:
		return "--model <name>, or shhh providers lists what each one serves"
	case failure.ClassCancelled:
		return "nothing was sent"
	}
	return "shhh providers shows what this machine can reach"
}
