package storage

import "time"

// UnratedSession is one session waiting for a person's read on it, together
// with the reminder of what it was.
//
// The reminder is the one field here that is content: it is the title a model
// wrote for the conversation, or failing that the first thing the person said
// in it. That is a deliberate crossing of the line the rest of this file
// holds — the record is content-free, and the question "was this session any
// good" cannot be answered by anything that is. It is read for the walk and
// never written back: what the store keeps is the answer, which is one bit.
// See docs/capabilities/sessions-and-memory.md#a-rating-is-how-you-check-the-inference.
type UnratedSession struct {
	ID        int64
	StartedAt time.Time
	Kind      string
	Model     string
	Turns     int64
	// Outcome is how the record read the session's ending, empty where it
	// could not say. It is what the rating is there to check.
	Outcome string
	// Chat is the saved conversation's name — the handle `shhh chats show`
	// takes, so a reader who wants more than the reminder can go and get it.
	Chat string
	// Title is what a model called the conversation, empty when none did;
	// Opening is the first thing the person said in it.
	Title   string
	Opening string
}

// ListUnratedSessions returns recent sessions nobody has rated yet, newest
// first, and only those whose linked conversation still holds something the
// reader can be reminded by.
//
// The join is what enforces that, and it is the point rather than a
// convenience: a session is a fortnight of nothing to look at without the
// conversation beside it, and asking someone to judge a row of token counts
// produces an answer about nothing. A sub-agent's session is left out by the
// same clause — no child is linked to a conversation — which is right for a
// different reason: a child is judged by the parent that spawned it.
//
// It joins on the row's reference and not on the slot's name: a
// conversation renamed since, or one the session was moved out of, still
// answers for the row that wrote it.
//
// The mapping it joins on is not one to one, and the reminder is the half
// that suffers. Resuming a conversation opens a second session row against
// the same name, so both are reminded by the first sitting's title and
// opening line — the later one is described by work it did not do. What
// keeps the two apart on the card is everything else the row carries: the
// name, the turn count and when it started.
func (db *DB) ListUnratedSessions(limit int) ([]UnratedSession, error) {
	// Whitespace is not a reminder: a message of three spaces and a newline
	// satisfies a bare `!= ''` and reaches the card as an empty body, and the
	// walk would then be asking about a session it is showing nothing of.
	// The trim names its characters because SQLite's one-argument trim takes
	// only spaces, which would leave exactly that message in.
	const opening = `(SELECT m.content FROM chat_messages m
		           WHERE m.session_id = c.id AND m.role = 'user'
		             AND trim(m.content, char(32,9,10,13)) != ''
		           ORDER BY m.seq LIMIT 1)`
	return queryRows(db, func(r rowScanner) (UnratedSession, error) {
		var (
			u         UnratedSession
			startedAt string
		)
		if err := r.Scan(&u.ID, &startedAt, &u.Kind, &u.Model, &u.Turns, &u.Outcome,
			&u.Chat, &u.Title, &u.Opening); err != nil {
			return u, err
		}
		u.StartedAt, _ = time.Parse(observeTimeFormat, startedAt)
		return u, nil
	}, `SELECT a.id, a.started_at, a.kind, a.model, a.turns, COALESCE(a.outcome, ''),
		        c.name, c.title, `+opening+`
		 FROM agent_sessions a
		 JOIN chat_sessions c ON c.id = a.chat_session_id
		 WHERE a.rating IS NULL AND `+opening+` IS NOT NULL
		 ORDER BY a.id DESC
		 LIMIT ?`, limit)
}

// RateAgentSession records a thumbs-up (true) or thumbs-down (false) for a
// session, the way RateRequest does for a command.
func (db *DB) RateAgentSession(id int64, up bool) error {
	rating := 0
	if up {
		rating = 1
	}
	_, err := db.sql.Exec(`UPDATE agent_sessions SET rating = ? WHERE id = ?`, rating, id)
	return err
}
