// Package cli is every cobra command shhh answers to, and the assembly of each
// surface that runs a conversation: the one-shot command, the inline shell
// widget, chat, the coding agent, the unattended and served runs, the config,
// doctor and history commands, and the evaluation harness.
//
// It owns the flags and their reading, the build of a session from the
// working scope, the store, the provider and the toolset, and the order those
// are opened and closed in, whichever surface holds the result. Config is read
// through the merged layers and the store is opened through one helper, so a
// command never reaches past either.
//
// It does not own what a surface does once it is running. The agent loop, the
// screen, the tools, the approval rules and the persistence are packages of
// their own, and this one wires them together and hands each its pieces
// rather than keeping a copy of their logic.
//
// See docs/architecture.md#a-session-is-assembled-in-one-place,
// docs/architecture.md#the-unattended-surfaces-share-one-tail and
// docs/capabilities/headless.md.
package cli
