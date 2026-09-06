package chat

// The word a switch is set with. Every `/ui <thing> <on|off>` command and
// `/gate on|off` read their argument here rather than each spelling out its
// own switch, so the vocabulary cannot drift apart one command at a time —
// a reader who learned that `/ui mouse true` works would otherwise find that
// `/gate true` does not, for no reason either command could give.
//
// The words are the ones the usage lines name plus the two spellings a
// person arrives with from a config file: `true`/`false` and `yes`/`no`.
// Nothing else is accepted, because a switch that guessed at `enable` or `1`
// would be guessing at the opposite just as often.

// parseToggle reads a switch's argument. ok is false for a word that names
// neither side, which every caller answers with its own error naming the
// setting — the word the reader typed is wrong for that command, and the
// command is what knows how to say so.
func parseToggle(word string) (on, ok bool) {
	switch word {
	case "on", "true", "yes":
		return true, true
	case "off", "false", "no":
		return false, true
	}
	return false, false
}
