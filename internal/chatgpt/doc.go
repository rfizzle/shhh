// Package chatgpt signs a person in with their ChatGPT plan and keeps that
// sign-in usable, so a session can spend the plan instead of API credit.
//
// The contract is OpenAI's: an OpenID Connect sign-in with PKCE against
// auth.openai.com, answered on a loopback callback at 127.0.0.1, which
// registers shhh on this host once and hands back a client ID kept from then
// on; an access token that lasts an hour and a refresh token that rotates on
// every refresh; and a revocation endpoint for signing out. Everything that
// can be got wrong in that exchange — a forged callback, a token minted for
// another client, a different account answering, two processes spending one
// refresh token — is checked here, before anything is written.
//
// The record lives in a FileStore the app supplies: shhh passes a directory in
// its data directory, never the config directory a person commits with their
// dotfiles. The tokens leave this package only as the bearer a request is sent
// with and through the callback that hides them from the model. Nothing here
// logs a URL, a body or a token: the log carries event names and the server's
// error codes.
// See docs/capabilities/chatgpt-plan.md.
package chatgpt
