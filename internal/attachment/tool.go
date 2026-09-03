package attachment

// The non-text part of a tool result.
//
// A tool executor hands back a string. That is the shape of every wrapper on
// the chain a call passes through — the repeat detector, a sub-agent's path
// rooting, the secret scrub, the MCP and skill toolsets — so widening it to
// carry bytes would mean widening all of them, and any wrapper that forgot
// would drop the bytes with nothing on screen to say so.
//
// So the bytes are left here, and the loop that records the result picks them
// up on the way past. The tool's own text is what they are filed under: it is
// the one thing both ends hold, the tool having written it and the loop being
// about to file it, and no wrapper in between has a reason to invent the same
// sentence. A wrapper that replaces the text outright — the scrub, when a
// path carries a secret — loses the lookup, and the result is the notice
// alone, which is what a provider that cannot take an image gets anyway.

import (
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/provider"
)

// maxPendingResults bounds what is held while waiting to be collected.
//
// It has to cover a whole round at once. A round's reads are dispatched
// together, up to the loop's parallel bound of eight, and each one records
// its parts before any of them is collected — so a cap under that would evict
// an image that was still coming for it, and the notice would promise a
// picture nobody sent. Sixteen is that round with room to spare; past it the
// oldest goes, because the only entries that live longer than the microsecond
// between returning and being collected are the ones nobody came back for,
// after a cancelled turn.
const maxPendingResults = 16

var (
	pendingMu sync.Mutex
	pending   = map[string][]provider.Attachment{}
	// pendingOrder is insertion order, oldest first, so eviction can drop
	// the entry that has been waiting longest.
	pendingOrder []string
)

// NoteResult records the parts a tool result carries beyond its text. The
// text itself is what they will be collected by.
func NoteResult(result string, atts ...provider.Attachment) {
	if result == "" || len(atts) == 0 {
		return
	}
	pendingMu.Lock()
	defer pendingMu.Unlock()
	if _, seen := pending[result]; !seen {
		pendingOrder = append(pendingOrder, result)
	}
	pending[result] = atts
	for len(pendingOrder) > maxPendingResults {
		delete(pending, pendingOrder[0])
		pendingOrder = pendingOrder[1:]
	}
}

// TakeResult collects the parts recorded for a result and forgets them. A
// result nothing was recorded for — which is nearly all of them — returns
// nothing.
//
// The match is the tool's text appearing in what came back, not the two being
// equal, because a wrapper is allowed to add to a result: the repeat detector
// puts its notice above a call the session has already made. Equality looked
// right and lost the image on the second read of the same picture, which is
// the read a person asks for when they did not believe the first one.
func TakeResult(result string) []provider.Attachment {
	if result == "" {
		return nil
	}
	pendingMu.Lock()
	defer pendingMu.Unlock()
	for i, key := range pendingOrder {
		if !strings.Contains(result, key) {
			continue
		}
		atts := pending[key]
		delete(pending, key)
		pendingOrder = append(pendingOrder[:i], pendingOrder[i+1:]...)
		return atts
	}
	return nil
}
