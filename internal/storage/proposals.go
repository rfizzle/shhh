package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Proposals already declined. A proposal the person said no to and that comes
// back next session is not a proposal but a nag, and the product promises it
// is not raised again
// (docs/capabilities/sessions-and-memory.md#memory-is-what-shhh-knows-about-your-project).
// So the no is written down, keyed on the checkout it was about, the kind of
// thing proposed and the hash of its normalised text — every surface that
// proposes or declines calls the functions below rather than keeping its own
// idea of what "the same proposal" is.

// ProposalMemory is the kind a declined memory is recorded under, whichever
// surface it was declined on.
const ProposalMemory = "memory"

// DeclinedProposal is one recorded no: the text as it was first declined,
// kept so the list can say what was refused rather than a hash.
type DeclinedProposal struct {
	ID         int64
	Root       string
	Kind       string
	Text       string
	DeclinedAt time.Time
}

// ProposalHash is the key a proposal is recognised by: its text lower-cased,
// its runs of whitespace collapsed to one space, and its closing punctuation
// dropped. The model re-proposes a sentence it was refused with a capital or
// a full stop moved more often than it rewords it, and those are the
// differences a no has to survive; a sentence that says something else is a
// different proposal and is asked about.
func ProposalHash(text string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(text), " "))
	norm = strings.TrimRight(norm, ".!?;:, ")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// DeclineProposal records that the person declined text, of kind, under
// root. Declining the same proposal again is not an error and keeps the first
// row: the second answer is the same answer.
func (db *DB) DeclineProposal(root, kind, text string) error {
	_, err := db.sql.Exec(
		`INSERT INTO proposals_declined (root, kind, hash, text, declined_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(root, kind, hash) DO NOTHING`,
		root, kind, ProposalHash(text), text, stamp(time.Now()))
	return err
}

// ProposalDeclined reports whether text, of kind, was declined under root. A
// store that cannot answer reads as not declined, so the proposal is asked
// about rather than silently dropped.
func (db *DB) ProposalDeclined(root, kind, text string) bool {
	var one int
	err := db.sql.QueryRow(
		`SELECT 1 FROM proposals_declined WHERE root = ? AND kind = ? AND hash = ?`,
		root, kind, ProposalHash(text)).Scan(&one)
	return err == nil
}

// DeclinedProposals lists what was declined under root, newest first.
func (db *DB) DeclinedProposals(root string) ([]DeclinedProposal, error) {
	return queryRows(db, func(r rowScanner) (DeclinedProposal, error) {
		var (
			p  DeclinedProposal
			at string
		)
		if err := r.Scan(&p.ID, &p.Root, &p.Kind, &p.Text, &at); err != nil {
			return p, err
		}
		p.DeclinedAt, _ = time.Parse(time.RFC3339Nano, at)
		return p, nil
	}, `SELECT id, root, kind, text, declined_at FROM proposals_declined
		 WHERE root = ? ORDER BY declined_at DESC, id DESC`, root)
}

// UndeclineProposal takes one recorded no back under root, so the proposal
// can be made again. Ids are numbered across every checkout, so the root
// keeps an id read from one checkout's list from taking back another's.
func (db *DB) UndeclineProposal(root string, id int64) error {
	res, err := db.sql.Exec(`DELETE FROM proposals_declined WHERE id = ? AND root = ?`, id, root)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("declined proposal %d not found", id)
	}
	return nil
}
