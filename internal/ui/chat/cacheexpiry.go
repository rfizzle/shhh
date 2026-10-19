package chat

// What a cache expiry cost, said once when it is seen. The ledger takes the
// observation at the gate; this is the one line the person gets for it,
// because it is the figure that makes the lease question concrete.
// See docs/capabilities/providers.md#the-session-measures-how-long-the-cache-lives.

import (
	"fmt"

	"github.com/rfizzle/agentcore/meter"
)

// noteCacheExpiries puts a notice on the transcript for each cache expiry the
// session's own requests have met since the last one. A child's expiry is
// its own prompt's and is not the person's pause.
func (m *Model) noteCacheExpiries() {
	for _, ex := range m.wiring.Ledger.TakeExpiries() {
		if ex.Origin.Source != meter.SourceAgent {
			continue
		}
		m.appendEntry(entry{kind: entrySystem, text: cacheExpiredNotice(ex)})
	}
}

// cacheExpiredNotice is the receipt: how long the person was away, what was
// written again and at what multiple of the input price, and what an idle
// compaction would have cost instead. Where the model has no price it says
// the tokens and nothing about money.
func cacheExpiredNotice(ex meter.CacheExpiry) string {
	said := fmt.Sprintf("cache expired while idle · %s away · %s rewritten", meter.GapLabel(ex.Gap), formatTokenCount(int64(ex.Rewritten)))
	if !ex.Priced {
		return said
	}
	return said + fmt.Sprintf(" at %g× (%s) · an idle compaction would have cost about %s",
		ex.Mult, formatCost(ex.Cost), formatCost(ex.CompactCost))
}
