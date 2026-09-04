package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

type ChatSession struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChatListEntry is one saved session as a listing shows it. Title is the
// generated one, empty until a reading produced it; the name is always the
// slot's own and is what every command addresses the session by.
type ChatListEntry struct {
	Name      string
	Title     string
	UpdatedAt time.Time
	Turns     int
	// Live marks a slot another running session is writing to. Opening one
	// means reading a conversation that is still being added to elsewhere,
	// and the next autosave over there takes the slot back
	// (docs/capabilities/sessions-and-memory.md#a-session-knows-it-is-not-alone).
	Live bool
}

// ChatExistsError is what a rename into a name already in use returns. It is
// its own type so a caller can tell a collision — which the person can fix by
// choosing another name — from a store that failed.
type ChatExistsError struct{ Name string }

func (e ChatExistsError) Error() string { return fmt.Sprintf("a chat named %q already exists", e.Name) }

// ChatNotFoundError is the error for a name no saved session carries.
type ChatNotFoundError struct{ Name string }

func (e ChatNotFoundError) Error() string { return fmt.Sprintf("chat %q not found", e.Name) }

// ChatSlotConflictError is what a save returns when the slot no longer holds
// what this process left in it — another session has the slot and its
// conversation is in there. It is its own type so the caller can move its own
// conversation somewhere safe, which is the one useful answer; a store that
// failed cannot be answered that way.
type ChatSlotConflictError struct{ Name string }

func (e ChatSlotConflictError) Error() string {
	return fmt.Sprintf("chat %q holds a conversation this session did not write", e.Name)
}

// chatSlotAttempts bounds the suffixes a claim will try before giving up.
// Reaching it means several dozen sessions started in the same second on one
// store, at which point the honest answer is an error and not a longer loop.
const chatSlotAttempts = 64

// ClaimChatSlot takes a slot for a session that is starting: it inserts a row
// under name, or under "name (2)", "name (3)"… when the name is taken, and
// returns the name it got. The insert is what decides the collision, not a
// look before it: two processes reading "free" in the same instant would both
// mint the same name, which is exactly how two sessions started in the same
// second used to end up autosaving over each other.
//
// The row is empty until the first save, and a slot with no messages is not
// listed, so a claim is invisible until the session writes something.
// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
func (db *DB) ClaimChatSlot(name string) (string, error) {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	return db.claimChatSlot(name)
}

// claimChatSlot is ClaimChatSlot with chatMu already held.
func (db *DB) claimChatSlot(name string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for n := 1; n <= chatSlotAttempts; n++ {
		claimed := name
		if n > 1 {
			claimed = fmt.Sprintf("%s (%d)", name, n)
		}
		res, err := db.sql.Exec(
			`INSERT OR IGNORE INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
			claimed, now, now,
		)
		if err != nil {
			return "", fmt.Errorf("claim slot: %w", err)
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			db.chatWrote[claimed] = chatWrite{seq: -1, digest: chatDigest(nil)}
			return claimed, nil
		}
	}
	return "", fmt.Errorf("claim slot: %q and %d suffixes are all taken", name, chatSlotAttempts-1)
}

// ReleaseChatSlot gives back a slot this process claimed and never wrote to,
// so a session that resumed an older conversation or was closed without a
// word leaves nothing behind. A slot this process did not claim is left
// alone whatever it holds: the row is another session's live claim, and
// deleting it would hand that session's name to the next one to ask for it.
func (db *DB) ReleaseChatSlot(name string) error {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	wrote, mine := db.chatWrote[name]
	if !mine || wrote.seq >= 0 {
		return nil
	}
	delete(db.chatWrote, name)

	_, err := db.sql.Exec(
		`DELETE FROM chat_sessions WHERE name = ?
		   AND NOT EXISTS (SELECT 1 FROM chat_messages m WHERE m.session_id = chat_sessions.id)
		   AND NOT EXISTS (SELECT 1 FROM chat_sessions c WHERE c.parent_id = chat_sessions.id)`,
		name,
	)
	return err
}

// chatWrite is what this process last put in a slot, or last read out of it:
// how far the messages went, and a fingerprint of them.
//
// The seq alone is what tells this session's rows from a stranger's. The
// digest is what tells a save that continues the conversation from one that
// rewrites it — a rewind or a compaction leaves a slot with a different
// conversation in it, and one of those can be the same length as what it
// replaced, so a save judged by its length alone would append onto messages
// nobody is having any more.
type chatWrite struct {
	seq    int
	digest uint64
}

// chatDigest fingerprints a conversation over exactly the fields a row keeps,
// so that what comes back out of the store digests to what went in. What is
// not kept is left out: the reasoning a turn did is not stored, and an
// attachment's bytes are counted rather than read, because a message whose
// image changed under an unchanged sentence is not a thing that happens and
// hashing a screenshot on every autosave is.
//
// Hashing the conversation on each save is work, but it is memory-speed work
// standing in for the disk-speed work it replaces: rewriting the same bytes
// into the store, which is what every autosave used to do.
func chatDigest(messages []provider.Message) uint64 {
	h := fnv.New64a()
	field := func(s string) {
		_, _ = io.WriteString(h, s)
		_, _ = h.Write([]byte{0})
	}
	for _, msg := range messages {
		field(string(msg.Role))
		field(msg.Content)
		field(msg.ToolCallID)
		for _, tc := range msg.ToolCalls {
			field(tc.ID)
			field(tc.Name)
			field(tc.Arguments)
			field(tc.Signature)
		}
		for _, a := range msg.Attachments {
			field(string(a.Kind))
			field(a.Name)
			field(a.MediaType)
			field(strconv.Itoa(len(a.Data)))
		}
	}
	return h.Sum64()
}

// rememberChat records what this process now has in a slot.
func (db *DB) rememberChat(name string, messages []provider.Message) {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	db.chatWrote[name] = chatWrite{seq: len(messages) - 1, digest: chatDigest(messages)}
}

// forgetChat drops what this process knew about a slot, for a name that is
// no longer the one it was: deleted, or renamed.
func (db *DB) forgetChat(name string) {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	delete(db.chatWrote, name)
}

// AutosaveChat writes a session's conversation to the slot it holds, or, when
// that slot no longer holds what this session put there, to one claimed under
// fresh. It answers with the slot the conversation is now in, which is the
// one the session has to go on saving to.
//
// The move is what a refusal is for: leaving the conversation unsaved would
// protect the other session's transcript by losing this one. It happens down
// here rather than in the caller's answer to the refusal so that a save on
// the way out still lands, and where each lost slot went is remembered, so a
// second refusal on it follows the first rather than making a second copy.
// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
// hold is what the slot should say about the conversation being mid-turn, and
// it is written here rather than beside the save because the two are one fact:
// two autosaves overlapping, each with its own answer, could otherwise land
// their halves in either order and leave a slot claiming a hold for a
// conversation that no longer has one.
func (db *DB) AutosaveChat(slot, fresh string, messages []provider.Message, hold *ChatHold) (string, error) {
	err := db.saveChatMarked(slot, messages, hold)
	var taken ChatSlotConflictError
	if !errors.As(err, &taken) {
		return slot, err
	}
	moved, err := db.movedChatSlot(slot, fresh)
	if err != nil {
		return slot, err
	}
	return moved, db.saveChatMarked(moved, messages, hold)
}

// movedChatSlot is where a slot this process lost has been replaced, claiming
// one under fresh the first time it is asked.
func (db *DB) movedChatSlot(from, fresh string) (string, error) {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	if to, ok := db.chatMoved[from]; ok {
		return to, nil
	}
	to, err := db.claimChatSlot(fresh)
	if err != nil {
		return "", err
	}
	db.chatMoved[from] = to
	return to, nil
}

// SaveChat writes a conversation to a named slot. It leaves the mid-turn mark
// alone: the name a person typed is a copy of the conversation, and whether
// the live session is holding a turn is not a fact about the copy.
func (db *DB) SaveChat(name string, messages []provider.Message) error {
	return db.saveChat(name, messages, false, nil)
}

// saveChatMarked is SaveChat plus the mid-turn mark, written in the same
// transaction so the two can never disagree.
func (db *DB) saveChatMarked(name string, messages []provider.Message, hold *ChatHold) error {
	return db.saveChat(name, messages, true, hold)
}

func (db *DB) saveChat(name string, messages []provider.Message, mark bool, hold *ChatHold) error {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()

	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := db.saveChatTx(tx, name, messages); err != nil {
		return err
	}
	if mark {
		if err := setChatHoldTx(tx, name, hold); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	db.chatWrote[name] = chatWrite{seq: len(messages) - 1, digest: chatDigest(messages)}
	return nil
}

// SaveChatBranch stores messages as a branch session of parentName: the
// branch gets its own session row with parent_id pointing at the parent
// (created as an empty session if it doesn't exist yet, so a never-saved live
// session can still grow branches).
func (db *DB) SaveChatBranch(parentName, branchName string, messages []provider.Message) error {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()

	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
		parentName, now, now,
	); err != nil {
		return fmt.Errorf("ensure parent session: %w", err)
	}

	branchID, err := db.saveChatTx(tx, branchName, messages)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE chat_sessions SET parent_id = (SELECT id FROM chat_sessions WHERE name = ?) WHERE id = ?`,
		parentName, branchID,
	); err != nil {
		return fmt.Errorf("link branch to parent: %w", err)
	}
	// What the parent is opened again on goes to the branch as well. A branch
	// is a tail of the conversation it forked from, so the compaction that
	// wrote that summary is in its own past too, and the commit the fork was
	// taken at is the commit its transcript describes. Without this a branch
	// would be the one conversation that comes back with no idea when it was
	// written down.
	if _, err := tx.Exec(
		`UPDATE chat_sessions
		    SET summary = (SELECT summary FROM chat_sessions WHERE name = ?),
		        head    = (SELECT head    FROM chat_sessions WHERE name = ?)
		  WHERE id = ?`,
		parentName, parentName, branchID,
	); err != nil {
		return fmt.Errorf("carry resume state to branch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	db.chatWrote[branchName] = chatWrite{seq: len(messages) - 1, digest: chatDigest(messages)}
	return nil
}

// saveChatTx puts one session's messages in the slot inside tx, preserving
// any existing parent link, and returns the session id.
//
// **A save writes the messages the slot does not have yet.** A conversation
// grows a turn at a time and never changes what is behind it, so a save that
// replaced every row wrote the whole conversation back to disk to record one
// sentence — the most work at the moment there was least to do, and it got
// worse the longer the sitting went on
// (docs/capabilities/sessions-and-memory.md#a-save-writes-the-turn-not-the-conversation).
//
// The exception is the conversation that did change behind itself: a rewind
// drops the tail, a compaction puts a summary where the opening used to be.
// Those are rewritten whole, and what tells them apart from a continuation is
// the fingerprint of what this process last left in the slot — the length
// alone would not, because a conversation can be rewritten into one exactly
// as long as the one it replaced.
//
// A slot that has grown past what this process wrote to it is refused rather
// than either: those rows are another session's conversation and writing over
// them would be the last anyone saw of it. A slot nothing here has touched is
// written as it always was — a name the person typed is theirs to overwrite.
//
// The test for a stranger is that the slot differs from what this process
// left there, not that it grew: a session that rewound or compacted leaves
// fewer messages than it once wrote, and a slot judged only by its length
// would be emptied over the shorter conversation somebody else had just put
// in it.
//
// What was written is remembered by the caller once the commit lands, never
// here: a mark recorded for a transaction that then rolled back would claim
// rows nobody wrote, and the next save would take that as licence to replace
// whatever is really in the slot. The caller holds chatMu across both.
func (db *DB) saveChatTx(tx *sql.Tx, name string, messages []provider.Message) (int64, error) {
	var sessionID int64
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// from is the first message this save has to write. A slot being made
	// holds nothing, so it is all of them.
	from := 0

	err := tx.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO chat_sessions (name, created_at, updated_at) VALUES (?, ?, ?)`,
			name, now, now,
		)
		if err != nil {
			return 0, fmt.Errorf("insert session: %w", err)
		}
		sessionID, _ = res.LastInsertId()
	} else if err != nil {
		return 0, fmt.Errorf("lookup session: %w", err)
	} else {
		stored, err := storedChatSeq(tx, sessionID)
		if err != nil {
			return 0, err
		}
		mine, seen := db.chatWrote[name]
		if seen && stored != mine.seq {
			return 0, ChatSlotConflictError{Name: name}
		}
		if _, err := tx.Exec(`UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, now, sessionID); err != nil {
			return 0, fmt.Errorf("update session: %w", err)
		}
		if seen && stored < len(messages) && chatDigest(messages[:stored+1]) == mine.digest {
			from = stored + 1
		} else if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ?`, sessionID); err != nil {
			return 0, fmt.Errorf("clear messages: %w", err)
		}
	}

	for i := from; i < len(messages); i++ {
		msg := messages[i]
		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return 0, fmt.Errorf("marshal tool calls: %w", err)
			}
			s := string(b)
			toolCallsJSON = &s
		}
		// Attachment bytes are saved with the turn that carried them
		//, so resuming a session keeps the screenshot the question
		// was about rather than a sentence pointing at nothing.
		var attachmentsJSON *string
		if len(msg.Attachments) > 0 {
			b, err := json.Marshal(msg.Attachments)
			if err != nil {
				return 0, fmt.Errorf("marshal attachments: %w", err)
			}
			s := string(b)
			attachmentsJSON = &s
		}
		_, err := tx.Exec(
			`INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, tool_call_id, attachments)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sessionID, i, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolCallID, attachmentsJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("insert message %d: %w", i, err)
		}
	}

	return sessionID, nil
}

// storedChatSeq is the highest seq the slot holds, or -1 when it holds no
// messages at all.
func storedChatSeq(tx *sql.Tx, sessionID int64) (int, error) {
	var seq sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM chat_messages WHERE session_id = ?`, sessionID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("read slot seq: %w", err)
	}
	if !seq.Valid {
		return -1, nil
	}
	return int(seq.Int64), nil
}

func (db *DB) LoadChat(name string) ([]provider.Message, error) {
	var sessionID int64
	err := db.sql.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return nil, ChatNotFoundError{Name: name}
	}
	if err != nil {
		return nil, err
	}

	rows, err := db.sql.Query(
		`SELECT role, content, tool_calls, tool_call_id, attachments
		 FROM chat_messages WHERE session_id = ? ORDER BY seq`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []provider.Message
	for rows.Next() {
		var (
			role, content, toolCallID      string
			toolCallsJSON, attachmentsJSON *string
		)
		if err := rows.Scan(&role, &content, &toolCallsJSON, &toolCallID, &attachmentsJSON); err != nil {
			return nil, err
		}
		msg := provider.Message{
			Role:       provider.Role(role),
			Content:    content,
			ToolCallID: toolCallID,
		}
		if toolCallsJSON != nil {
			if err := json.Unmarshal([]byte(*toolCallsJSON), &msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("unmarshal tool calls: %w", err)
			}
		}
		if attachmentsJSON != nil {
			if err := json.Unmarshal([]byte(*attachmentsJSON), &msg.Attachments); err != nil {
				return nil, fmt.Errorf("unmarshal attachments: %w", err)
			}
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// What was read is what this process has in the slot: a resumed session
	// autosaves over the conversation it just loaded, and must be able to
	// tell that from another session's messages arriving underneath it.
	db.rememberChat(name, messages)
	return messages, nil
}

// ListChats is every saved conversation, newest first. A slot holding no
// messages is not one of them: a session claims its slot when it starts and
// may never write to it, and a listing that offered those would put an empty
// conversation at the top of `--continue` for as long as a session sits idle.
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
	rows, err := db.sql.Query(
		`SELECT s.name, s.title, s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) as turns
		 FROM chat_sessions s
		 JOIN chat_messages m ON m.session_id = s.id
		 GROUP BY s.id
		 ORDER BY s.updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ChatListEntry
	for rows.Next() {
		var (
			e         ChatListEntry
			updatedAt string
		)
		if err := rows.Scan(&e.Name, &e.Title, &updatedAt, &e.Turns); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		e.Live = live[e.Name]
		entries = append(entries, e)
	}
	return entries, rows.Err()
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
	rows, err := db.sql.Query(
		`SELECT s.name, s.title, s.updated_at,
		        COUNT(CASE WHEN m.role = 'user' THEN 1 END) AS turns
		 FROM chat_sessions s
		 LEFT JOIN chat_messages m ON m.session_id = s.id
		 WHERE s.id IN (`+strings.Join(perWord, " INTERSECT ")+`)
		 GROUP BY s.id
		 ORDER BY s.updated_at DESC`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ChatListEntry
	for rows.Next() {
		var (
			e         ChatListEntry
			updatedAt string
		)
		if err := rows.Scan(&e.Name, &e.Title, &updatedAt, &e.Turns); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		e.Live = live[e.Name]
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PruneOldChats deletes every saved conversation nothing has written to for
// longer than retentionDays, and reports how many sessions went. Zero days is
// off, which is what an unset setting means: a conversation is a person's
// work and the product does not throw one away on a window nobody chose
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
		 ORDER BY started_at DESC LIMIT 1`, at, at)
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
	rows, err := db.sql.Query(
		`WITH RECURSIVE up(id, parent_id) AS (
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
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []ChatBranch
	for rows.Next() {
		var (
			b         ChatBranch
			updatedAt string
		)
		if err := rows.Scan(&b.Name, &b.Parent, &updatedAt, &b.Turns); err != nil {
			return nil, err
		}
		b.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		branches = append(branches, b)
	}
	return branches, rows.Err()
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

// ChatHold is the mark a conversation saved mid-turn carries beside itself:
// the turn was parked at a round boundary rather than finished, and this is
// where it had got to. Both counts belong to the turn and not to the process
// that wrote them — a new session's own round counter starts at nothing, and
// the grant has to come back with the turn or the round it resumes into stops
// again at a ceiling the person had already lifted.
// See docs/capabilities/sessions-and-memory.md#a-held-turn-comes-back-held.
type ChatHold struct {
	Rounds  int `json:"rounds"`
	Granted int `json:"granted"`
}

// setChatHoldTx writes the mark inside tx, or clears it when h is nil. Every
// autosave carries an answer — nil included — so a slot never goes on claiming
// a hold the turn has already been let go of.
func setChatHoldTx(tx *sql.Tx, name string, h *ChatHold) error {
	var held any
	if h != nil {
		b, err := json.Marshal(h)
		if err != nil {
			return err
		}
		held = string(b)
	}
	res, err := tx.Exec(`UPDATE chat_sessions SET held = ? WHERE name = ?`, held, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// ChatHold reads the mark and whether there is one. A slot nothing ever held
// — including every slot written before the column existed — reports false,
// which is the answer that opens it the way it always opened.
func (db *DB) ChatHold(name string) (ChatHold, bool, error) {
	var held sql.NullString
	err := db.sql.QueryRow(`SELECT held FROM chat_sessions WHERE name = ?`, name).Scan(&held)
	if err == sql.ErrNoRows {
		return ChatHold{}, false, nil
	}
	if err != nil || !held.Valid || held.String == "" {
		return ChatHold{}, false, err
	}
	var h ChatHold
	if err := json.Unmarshal([]byte(held.String), &h); err != nil {
		// A mark nobody can read is a mark nobody can act on. Opening the
		// conversation idle is the honest answer and costs one keystroke;
		// refusing to open it at all would cost the whole conversation.
		return ChatHold{}, false, nil
	}
	return h, true, nil
}

// SetChatTitle stores the generated title on a session. It is the one write
// a reading makes, and it never touches the name: the name is the user's,
// the title is the model's, and only the listing puts them side by side.
func (db *DB) SetChatTitle(name, title string) error {
	res, err := db.sql.Exec(`UPDATE chat_sessions SET title = ? WHERE name = ?`, title, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// HasChat reports whether a session by that name is saved.
func (db *DB) HasChat(name string) (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM chat_sessions WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

// ChatTitle reads a session's generated title; empty when none was written
// or the session is unknown.
func (db *DB) ChatTitle(name string) (string, error) {
	var title string
	err := db.sql.QueryRow(`SELECT title FROM chat_sessions WHERE name = ?`, name).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return title, err
}

// ChatResume is what a slot says about the sitting that left it, beside the
// conversation itself: the summary the last compaction wrote, and the commit
// the checkout was on when the slot was last written.
//
// Both are for the conversation's next opening rather than for this one.
// Summary is what a compaction already produced — nothing is summarized to
// fill it — and Head is what makes the difference between the tree the
// transcript describes and the tree in front of the reader something the
// session can state instead of something it has to be told.
// See docs/capabilities/sessions-and-memory.md#a-resumed-session-sees-the-tree-as-it-is.
type ChatResume struct {
	Summary string
	Head    string
}

// SetChatResume stores what the slot is opened again on. It is the title's
// shape and rides beside it on the same save: one write, both halves, so a
// slot can never carry a summary from one sitting and a commit from another.
func (db *DB) SetChatResume(name string, r ChatResume) error {
	res, err := db.sql.Exec(
		`UPDATE chat_sessions SET summary = ?, head = ? WHERE name = ?`, r.Summary, r.Head, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ChatNotFoundError{Name: name}
	}
	return nil
}

// ChatResume reads it back. A slot that never wrote one — every slot written
// before the columns existed included — answers with both halves empty, which
// is the answer that opens the conversation the way it always opened.
func (db *DB) ChatResume(name string) (ChatResume, error) {
	var r ChatResume
	err := db.sql.QueryRow(
		`SELECT summary, head FROM chat_sessions WHERE name = ?`, name).Scan(&r.Summary, &r.Head)
	if err == sql.ErrNoRows {
		return ChatResume{}, nil
	}
	return r, err
}
