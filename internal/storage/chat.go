package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

type ChatSession struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
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
	now := stamp(time.Now())
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
		field(strconv.FormatBool(msg.Machine))
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
	if err := db.saveChatMarked(moved, messages, hold); err != nil {
		return moved, err
	}
	// The conversation is in the new slot; the records of what it
	// changed have to follow or a later resume of this sitting would
	// open on the files with nobody owning them.
	if err := db.CopyChanges(slot, moved); err != nil {
		return moved, err
	}
	return moved, nil
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

// saveChat is the write both of those go through. The whole transaction is
// what a refused lock is tried again from, never a statement inside it: a
// save reads the slot before it writes, and a write refused on its way up
// from that read has to read the slot again to be judging what is in there
// now (storage.go).
func (db *DB) saveChat(name string, messages []provider.Message, mark bool, hold *ChatHold) error {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()

	if err := retryBusy(func() error {
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
		return tx.Commit()
	}); err != nil {
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

	now := stamp(time.Now())
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
		        head    = (SELECT head    FROM chat_sessions WHERE name = ?),
		        steps   = (SELECT steps   FROM chat_sessions WHERE name = ?)
		  WHERE id = ?`,
		parentName, parentName, parentName, branchID,
	); err != nil {
		return fmt.Errorf("carry resume state to branch: %w", err)
	}
	// The folded turns go to the branch too, so it draws what its parent
	// drew: compaction replaced them in the model's list, never in the record.
	if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ? AND seq < 0`, branchID); err != nil {
		return fmt.Errorf("clear branch folded: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, tool_call_id, attachments, machine, turn, round, checkpoint, machine_kind)
		 SELECT ?, m.seq, m.role, m.content, m.tool_calls, m.tool_call_id, m.attachments, m.machine, m.turn, m.round, m.checkpoint, m.machine_kind
		 FROM chat_messages m JOIN chat_sessions p ON p.id = m.session_id
		 WHERE p.name = ? AND m.seq < 0`, branchID, parentName,
	); err != nil {
		return fmt.Errorf("carry folded turns to branch: %w", err)
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
	now := stamp(time.Now())
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
		// A slot this process never wrote is another conversation's: the
		// folded turns beside it go with it, so a reopen never draws them
		// over the conversation written in its place.
		if err := clearStrangersFold(tx, sessionID, seen); err != nil {
			return 0, err
		}
		if seen && stored < len(messages) && chatDigest(messages[:stored+1]) == mine.digest {
			from = stored + 1
		} else if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ? AND seq >= 0`, sessionID); err != nil {
			return 0, fmt.Errorf("clear messages: %w", err)
		}
	}

	for i := from; i < len(messages); i++ {
		if err := insertChatMessage(tx, sessionID, i, messages[i]); err != nil {
			return 0, err
		}
	}

	return sessionID, nil
}

// clearStrangersFold drops the folded turns of a slot this process never
// wrote; one it did write keeps them through a rewrite.
func clearStrangersFold(tx *sql.Tx, sessionID int64, seen bool) error {
	if seen {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ? AND seq < 0`, sessionID); err != nil {
		return fmt.Errorf("clear folded: %w", err)
	}
	return nil
}

// insertChatMessage writes one message at seq in a session's slot.
func insertChatMessage(tx *sql.Tx, sessionID int64, seq int, msg provider.Message) error {
	var toolCallsJSON *string
	if len(msg.ToolCalls) > 0 {
		b, err := json.Marshal(msg.ToolCalls)
		if err != nil {
			return fmt.Errorf("marshal tool calls: %w", err)
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
			return fmt.Errorf("marshal attachments: %w", err)
		}
		s := string(b)
		attachmentsJSON = &s
	}
	// The turn and the round the message was written in ride with it,
	// so a recorded event can be joined to the words it came from
	// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
	// The machine message's kind is NULL where it has none, the way a
	// row written before the column reads.
	var machineKind *string
	if msg.MachineKind != "" {
		k := string(msg.MachineKind)
		machineKind = &k
	}
	_, err := tx.Exec(
		`INSERT INTO chat_messages (session_id, seq, role, content, tool_calls, tool_call_id, attachments, machine, turn, round, checkpoint, machine_kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, seq, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolCallID, attachmentsJSON, msg.Machine,
		msg.Turn, msg.Round, msg.Checkpoint, machineKind,
	)
	if err != nil {
		return fmt.Errorf("insert message %d: %w", seq, err)
	}
	return nil
}

// storedChatSeq is the highest seq the slot's conversation holds, or -1 when
// it holds none. Folded turns (seq below zero) sit beside the conversation,
// not in it, so a slot with only those reads as empty.
func storedChatSeq(tx *sql.Tx, sessionID int64) (int, error) {
	var seq sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM chat_messages WHERE session_id = ? AND seq >= 0`, sessionID).Scan(&seq); err != nil {
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
	messages, err := db.chatMessages(sessionID)
	if err != nil {
		return nil, err
	}
	// What was read is what this process has in the slot: a resumed session
	// autosaves over the conversation it just loaded, and must be able to
	// tell that from another session's messages arriving underneath it.
	//
	// It is here rather than in chatMessages because only a read by name is
	// a session taking up a slot. A read by row id is somebody looking at a
	// conversation — the export's transcript join — and a reader that
	// remembered the slot would make this process believe it owns one it has
	// never written to.
	db.rememberChat(name, messages)
	return messages, nil
}

// SaveChatFolded keeps the turns a compaction folded beside the slot's
// conversation, replacing whatever folded turns it held. They are the record
// of what the model no longer carries, so a reopen can draw them and open
// their pictures as the live session did, while LoadChat goes on handing the
// model the compacted list.
//
// They live in the slot's own message table at seq below zero, oldest first,
// so a save of the conversation (which owns seq zero and up) never touches
// them and no migration is needed. An empty folded clears the slot's.
// See docs/interface/surfaces.md#the-compaction-receipt.
func (db *DB) SaveChatFolded(name string, folded []provider.Message) error {
	db.chatMu.Lock()
	defer db.chatMu.Unlock()
	return retryBusy(func() error {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var sessionID int64
		err = tx.QueryRow(`SELECT id FROM chat_sessions WHERE name = ?`, name).Scan(&sessionID)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lookup session: %w", err)
		}
		// Folded turns only ever grow within one conversation, so the same
		// count is the same turns; checking it keeps an autosave from
		// rewriting every picture on each turn.
		var held int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE session_id = ? AND seq < 0`, sessionID).Scan(&held); err != nil {
			return fmt.Errorf("count folded: %w", err)
		}
		if held == len(folded) {
			return nil
		}
		if _, err := tx.Exec(`DELETE FROM chat_messages WHERE session_id = ? AND seq < 0`, sessionID); err != nil {
			return fmt.Errorf("clear folded: %w", err)
		}
		for i, msg := range folded {
			if err := insertChatMessage(tx, sessionID, i-len(folded), msg); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

// LoadChatFolded is the turns a compaction folded out of a slot's
// conversation, oldest first, or none for a slot that was never compacted.
func (db *DB) LoadChatFolded(name string) ([]provider.Message, error) {
	return queryRows(db, scanChatMessage,
		`SELECT m.role, m.content, m.tool_calls, m.tool_call_id, m.attachments, m.machine, m.turn, m.round, m.checkpoint, m.machine_kind
		 FROM chat_messages m JOIN chat_sessions s ON s.id = m.session_id
		 WHERE s.name = ? AND m.seq < 0 ORDER BY m.seq`, name,
	)
}

// chatMessages is one slot's conversation by row id, which is how anything
// holding a reference to a conversation rather than its name reads it: a
// name can be renamed out from under the row that named it, and the record's
// link to the conversation it wrote is a reference for exactly that reason
// (docs/capabilities/sessions-and-memory.md#a-round-can-be-read-back).
func (db *DB) chatMessages(sessionID int64) ([]provider.Message, error) {
	return queryRows(db, scanChatMessage,
		`SELECT role, content, tool_calls, tool_call_id, attachments, machine, turn, round, checkpoint, machine_kind
		 FROM chat_messages WHERE session_id = ? AND seq >= 0 ORDER BY seq`, sessionID,
	)
}

// scanChatMessage reads one row of chatMessages' query back into the message
// it was saved from.
func scanChatMessage(r rowScanner) (provider.Message, error) {
	var (
		role, content, toolCallID      string
		toolCallsJSON, attachmentsJSON *string
		machineKind                    *string
		machine, checkpoint            bool
		turn, round                    int64
	)
	if err := r.Scan(&role, &content, &toolCallsJSON, &toolCallID, &attachmentsJSON, &machine,
		&turn, &round, &checkpoint, &machineKind); err != nil {
		return provider.Message{}, err
	}
	msg := provider.Message{
		Role:       provider.Role(role),
		Content:    content,
		ToolCallID: toolCallID,
		Machine:    machine,
		// Where it was written, carried back so a conversation replayed
		// into the agent keeps the position it was saved with rather
		// than being restamped with wherever the resume has got to.
		Turn:  turn,
		Round: round,
		// And whether it was the run reporting on itself, so a reopened
		// transcript draws the note at the rung it was written at.
		Checkpoint: checkpoint,
	}
	if machineKind != nil {
		msg.MachineKind = provider.MachineKind(*machineKind)
	}
	if toolCallsJSON != nil {
		if err := json.Unmarshal([]byte(*toolCallsJSON), &msg.ToolCalls); err != nil {
			return provider.Message{}, fmt.Errorf("unmarshal tool calls: %w", err)
		}
	}
	if attachmentsJSON != nil {
		if err := json.Unmarshal([]byte(*attachmentsJSON), &msg.Attachments); err != nil {
			return provider.Message{}, fmt.Errorf("unmarshal attachments: %w", err)
		}
	}
	return msg, nil
}
