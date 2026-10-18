package provider

import "context"

// TokenSource is a bearer that moves under a session. refused is the bearer
// the last request was refused with, or empty. A source still holding refused
// refreshes before it answers; one that answers refused again could not. An
// error is classified unauthorized unless it is a network failure.
//
// It is the one handle the ChatGPT plan route holds on its credential: an
// access token comes out of it and nothing else does, so a refresh token is
// never in this package, on a request, or in an error built here.
// See docs/capabilities/chatgpt-plan.md#what-a-plan-session-cannot-do.
type TokenSource interface {
	Token(ctx context.Context, refused string) (string, error)
}
