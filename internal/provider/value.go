package provider

// The structured part of a tool result.
//
// A tool executor hands back a string, and every wrapper on the chain a call
// passes through passes one, for the reason attachment/tool.go gives for the
// parts that are not text. A tool whose result is also a value a surface
// reads — the quality gate's verdict — leaves it here, and the loop that
// records the result collects it on the way past and files it on the message
// (Message.Value).
//
// It is filed under the call, not under the text. The text is the one thing
// the wrappers are allowed to change — the secret scrub rewrites a body that
// holds a secret, the repeat detector adds a notice — and two calls can
// return the same text, so a text key loses the value to either. What both
// ends hold, and no wrapper touches, is the tool's name and its arguments:
// the executor is handed them and the loop that calls it has them. A call
// that is not the gate's finds nothing under its own name, whatever its
// output quotes.

import (
	"encoding/json"
	"sync"
)

// maxPendingValues bounds what is held while waiting to be collected: a
// whole round's calls at once, with room to spare, past which the oldest goes
// because the entries that live longer than the moment between returning and
// being collected are the ones nobody came back for.
const maxPendingValues = 32

type pendingValue struct {
	key   string
	value json.RawMessage
}

var (
	valueMu sync.Mutex
	// values is oldest first. A call collects the newest entry under its key,
	// so two calls with the same name and arguments in one round each take
	// one, and an entry nobody collected (a cancelled turn) is passed over by
	// the next call that left a fresh one.
	values []pendingValue
)

func valueKey(name string, args string) string { return name + "\x00" + args }

// NoteValue records the value the call to name with args is returning beside
// its text.
func NoteValue(name string, args json.RawMessage, value json.RawMessage) {
	if len(value) == 0 {
		return
	}
	valueMu.Lock()
	defer valueMu.Unlock()
	values = append(values, pendingValue{key: valueKey(name, string(args)), value: value})
	if len(values) > maxPendingValues {
		values = values[len(values)-maxPendingValues:]
	}
}

// TakeValue collects the value recorded for a call and forgets it. A call
// nothing was recorded for — nearly all of them — returns nil.
func TakeValue(name string, args string) json.RawMessage {
	key := valueKey(name, args)
	valueMu.Lock()
	defer valueMu.Unlock()
	for i := len(values) - 1; i >= 0; i-- {
		if values[i].key == key {
			v := values[i].value
			values = append(values[:i], values[i+1:]...)
			return v
		}
	}
	return nil
}
