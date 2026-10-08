package storage

import (
	"fmt"
	"strings"
	"time"
)

// ChatListEntry is one saved session as a listing shows it. Title is the
// generated one, empty until a reading produced it; the name is always the
// slot's own and is what every command addresses the session by.
type ChatListEntry struct {
	Name  string
	Title string
	// Summary is the slot's standing account — two sentences on what the
	// conversation was doing and where it left off, or the handoff its last
	// compaction wrote, whichever came later — and empty until one of them
	// was written (docs/capabilities/sessions-and-memory.md#a-title-you-did-not-write).
	Summary   string
	UpdatedAt time.Time
	// Turns is every user turn the record holds, the ones a compaction folded
	// away (stored at seq < 0) included, for the list and the search alike
	// (docs/interface/surfaces.md#selectors).
	Turns int
	// Live marks a slot another running session is writing to. Opening one
	// means reading a conversation that is still being added to elsewhere,
	// and the next autosave over there takes the slot back
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	Live bool
}

// ListChats is every saved conversation, newest first, with the id breaking
// a tie on the update stamp so that two slots written in one tick still list
// in one order. A slot holding no messages is not one of them: a session
// claims its slot when it starts and may never write to it, and a listing
// that offered those would put an empty conversation at the top of
// `--continue` for as long as a session sits idle.
//
// Each entry says whether another running session has the slot, which is the
// one thing a reader cannot see from the row itself: a name and a timestamp
// look the same whether the conversation is finished or still being written.
func (db *DB) ListChats() ([]ChatListEntry, error) {
	// Read before the listing's cursor is open: the store runs on one
	// connection, so a second query issued mid-walk would wait on it. And
	// read best-effort — a mark that could not be taken costs the mark, not
	// the listing, which is the answer the caller actually asked for.
	live, _ := db.liveChatSlots(time.Now())
	return queryRows(db, scanChatListEntry(live),
		`SELECT s.name, s.title, s.summary, s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) as turns
		 FROM chat_sessions s
		 JOIN chat_messages m ON m.session_id = s.id
		 GROUP BY s.id
		 ORDER BY s.updated_at DESC, s.id DESC`,
	)
}

// scanChatListEntry reads one row of a listing, ListChats' or SearchChats',
// and marks it with whether live says another session has the slot.
func scanChatListEntry(live map[string]bool) func(rowScanner) (ChatListEntry, error) {
	return func(r rowScanner) (ChatListEntry, error) {
		var (
			e         ChatListEntry
			updatedAt string
		)
		if err := r.Scan(&e.Name, &e.Title, &e.Summary, &updatedAt, &e.Turns); err != nil {
			return e, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		e.Live = live[e.Name]
		return e, nil
	}
}

// SearchChats is every saved conversation carrying what was typed, newest
// first — matched on the words in its messages and on the words in the title
// a reading gave it.
//
// Both halves are needed and neither is enough. A conversation is remembered
// by something that was said in it far more often than by the timestamp it
// was filed under, which is what the message half is for; and a conversation
// that holds no messages at all still has a title, which is the only thing
// there is to find it by.
// See docs/capabilities/sessions-and-memory.md#finding-a-conversation-again.
//
// **Each word narrows the answer, and each is looked for in the whole
// conversation rather than in one message of it.** A person searching for two
// words is describing a conversation they remember, not a sentence somebody
// said — the two halves of "the retry flake" are as likely to be a question
// and its answer as one line. So the index is asked once per word and the
// answers are intersected on the session, which is also what lets one word
// come from the title and the next from the transcript.
//
// A query with no word in it matches nothing rather than everything: a search
// that answered with the whole store would look like a search that worked.
func (db *DB) SearchChats(query string) ([]ChatListEntry, error) {
	terms := matchTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	// A compound SELECT has no precedence to lean on — every operator in one
	// binds as tightly as the next — so each word's two sources are wrapped
	// in a FROM before the words are intersected.
	perWord := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)*2)
	for _, term := range terms {
		perWord = append(perWord, `SELECT id FROM (
		         SELECT said.session_id AS id FROM chat_message_search
		           JOIN chat_messages said ON said.id = chat_message_search.rowid
		          WHERE chat_message_search MATCH ?
		         UNION
		         SELECT rowid AS id FROM chat_title_search WHERE chat_title_search MATCH ?
		     )`)
		args = append(args, term, term)
	}

	// Read before the listing's cursor is open and best-effort, for the two
	// reasons ListChats reads it that way.
	live, _ := db.liveChatSlots(time.Now())
	return queryRows(db, scanChatListEntry(live),
		`SELECT s.name, s.title, s.summary, s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) AS turns
		 FROM chat_sessions s
		 LEFT JOIN chat_messages m ON m.session_id = s.id
		 WHERE s.id IN (`+strings.Join(perWord, " INTERSECT ")+`)
		 GROUP BY s.id
		 ORDER BY s.updated_at DESC, s.id DESC`, args...,
	)
}

// PruneOldChats deletes every saved conversation nothing has written to for
// longer than retentionDays, and reports how many sessions went. Zero days is
// off, and this is the one window a person can turn off: a conversation is
// their work rather than the residue a session leaves behind, so the answer
// to "keep every one of them" is an answer the key takes
// (docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window).
//
// **A family goes or stays together.** A branch is a tail of the conversation
// it forked from, the way deleting one by hand already treats it, so the
// window is put to the whole family and answered by its newest member. Judged
// one row at a time it would cut either way and both ways are wrong: an old
// root would take a branch somebody used this morning, and an old branch left
// behind would be a conversation under a name nobody typed.
func (db *DB) PruneOldChats(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	// Under the same lock the saves take: the map of what this process has
	// in each slot is read against the rows, and a delete landing between
	// those two would leave a save writing against a slot that no longer
	// exists.
	db.chatMu.Lock()
	defer db.chatMu.Unlock()

	res, err := db.sql.Exec(
		`WITH RECURSIVE family(id, root) AS (
		     SELECT id, id FROM chat_sessions WHERE parent_id IS NULL
		     UNION ALL
		     SELECT s.id, f.root FROM chat_sessions s JOIN family f ON s.parent_id = f.id
		 )
		 DELETE FROM chat_sessions WHERE id IN (
		     SELECT id FROM family WHERE root IN (
		         SELECT f.root FROM family f JOIN chat_sessions s ON s.id = f.id
		         GROUP BY f.root HAVING MAX(s.updated_at) < ?
		     )
		 )`, retentionCutoff(time.Now(), retentionDays))
	if err != nil {
		return 0, fmt.Errorf("prune chats: %w", err)
	}
	return res.RowsAffected()
}

// ChatBranch is one session in a branch family: the root plus every branch
// hanging off it. Parent is empty for the root.
type ChatBranch struct {
	Name      string
	Parent    string
	UpdatedAt time.Time
	Turns     int
}

// RecentChat is the most recently saved session, for the start screen's
// resume suggestion: what it was called, how many turns it holds, and
// what it cost. Cost is only present when an observability record covers the
// moment the session was saved — the two tables are joined by that window
// rather than by name, because a chat session row has never carried a price
// and inventing one is worse than leaving the clause off.
type RecentChat struct {
	Name      string
	Title     string
	UpdatedAt time.Time
	Turns     int
	Cost      float64
	Priced    bool
	// Held is the newest slot that was stepped past because another running
	// session is autosaving into it, and is empty when none was. It is filled
	// whether or not an older slot was left to return, so a caller reads it
	// before it reads the ok: "there is nothing to come back to" and "the
	// thing to come back to belongs to somebody else" are different answers,
	// and a caller that was given an instruction rather than making an offer
	// has to be able to refuse the second one by name.
	Held string
}

// MostRecentChat returns the newest saved session nobody else is writing to,
// or ok=false when there is none. A missing observability record costs the
// price clause, never the suggestion.
//
// A slot a running session is still autosaving into is stepped past rather
// than returned: what it holds is half of somebody else's conversation, and
// their next save takes the slot back from whoever opened it. What was
// stepped past is named in Held rather than dropped, so a caller that has to
// answer for the newest slot in particular can say which one it was.
// See docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone.
func (db *DB) MostRecentChat() (RecentChat, bool, error) {
	entries, err := db.ListChats()
	if err != nil {
		return RecentChat{}, false, err
	}
	past := 0
	for past < len(entries) && entries[past].Live {
		past++
	}
	held := ""
	if past > 0 {
		held = entries[0].Name
	}
	entries = entries[past:]
	if len(entries) == 0 {
		return RecentChat{Held: held}, false, nil
	}
	e := entries[0]
	out := RecentChat{Name: e.Name, Title: e.Title, UpdatedAt: e.UpdatedAt, Turns: e.Turns, Held: held}

	// The session that was running when the chat was last written is the one
	// whose lifetime contains that write: started before it, and either still
	// open or ended after it. Children are excluded — a sub-agent's spend is
	// part of its parent's, not a session of its own to resume. Both columns
	// are written in the same UTC layout, so the comparison is exact.
	at := e.UpdatedAt.UTC().Format(observeTimeFormat)
	row := db.sql.QueryRow(
		`SELECT est_cost FROM agent_sessions
		 WHERE parent_id IS NULL AND started_at <= ?
		   AND (ended_at IS NULL OR ended_at >= ?)
		 ORDER BY started_at DESC, id DESC LIMIT 1`, at, at)
	var cost float64
	if err := row.Scan(&cost); err == nil {
		out.Cost, out.Priced = cost, true
	}
	return out, true, nil
}

// ListChatBranches returns the branch family of name — the root reached by
// walking name's parent chain, plus every descendant — ordered oldest-first.
// An unknown name yields an empty list, not an error.
func (db *DB) ListChatBranches(name string) ([]ChatBranch, error) {
	return queryRows(db, func(r rowScanner) (ChatBranch, error) {
		var (
			b         ChatBranch
			updatedAt string
		)
		if err := r.Scan(&b.Name, &b.Parent, &updatedAt, &b.Turns); err != nil {
			return b, err
		}
		b.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		return b, nil
	}, `WITH RECURSIVE up(id, parent_id) AS (
		     SELECT id, parent_id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id, s.parent_id FROM chat_sessions s JOIN up ON s.id = up.parent_id
		 ),
		 family(id) AS (
		     SELECT id FROM up WHERE parent_id IS NULL
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN family f ON s.parent_id = f.id
		 )
		 SELECT s.name, COALESCE(p.name, ''), s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) AS turns
		 FROM chat_sessions s
		 JOIN family ON family.id = s.id
		 LEFT JOIN chat_sessions p ON p.id = s.parent_id
		 LEFT JOIN chat_messages m ON m.session_id = s.id
		 GROUP BY s.id
		 ORDER BY s.created_at, s.id`, name,
	)
}

// DeleteChat removes a session and every branch hanging off it. The
// branches go with it because a branch is a tail of the conversation it
// forked from: left behind, it would be a session nobody named, holding a
// copy of messages that were just deleted. The confirm that precedes this
// names the count (CountChatBranches), so nothing is removed unannounced
// (docs/interface/surfaces.md#the-inline-confirm).
func (db *DB) DeleteChat(name string) error {
	res, err := db.sql.Exec(
		`WITH RECURSIVE family(id) AS (
		     SELECT id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN family f ON s.parent_id = f.id
		 )
		 DELETE FROM chat_sessions WHERE id IN (SELECT id FROM family)`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ChatNotFoundError{Name: name}
	}
	db.forgetChat(name)
	return nil
}

// CountChatBranches is how many sessions hang off name — its descendants,
// not the family it belongs to — which is what deleting it would take with
// it. An unknown name counts zero.
func (db *DB) CountChatBranches(name string) (int, error) {
	var n int
	err := db.sql.QueryRow(
		`WITH RECURSIVE below(id) AS (
		     SELECT id FROM chat_sessions WHERE name = ?
		     UNION ALL
		     SELECT s.id FROM chat_sessions s JOIN below b ON s.parent_id = b.id
		 )
		 SELECT COUNT(*) - 1 FROM below`, name).Scan(&n)
	if err != nil {
		return 0, err
	}
	return max(n, 0), nil
}

// RenameChat gives a saved session a new name. Branches are linked by id,
// so a renamed root keeps every branch and a renamed branch keeps its
// parent. A name already in use is refused with ChatExistsError rather than
// merged: two conversations under one name is what SaveChat's overwrite
// would make of it, and a rename that silently discards a session is not a
// rename (docs/capabilities/sessions-and-memory.md#housekeeping).
func (db *DB) RenameChat(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	var taken int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE name = ?`, newName).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return ChatExistsError{Name: newName}
	}
	res, err := db.sql.Exec(`UPDATE chat_sessions SET name = ? WHERE name = ?`, newName, oldName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: oldName}
	}
	// The slot is the same row under another name, so what this process has
	// in it goes with it; a save to the new name is still its own.
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	if wrote, mine := db.chatWrote[oldName]; mine {
		delete(db.chatWrote, oldName)
		db.chatWrote[newName] = wrote
	}
	return nil
}
