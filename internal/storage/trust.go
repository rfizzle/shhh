package storage

// Trust for a checkout. The skills, agent profiles, quality suites, hooks
// and servers a repository defines are not the person's own decision, so
// none of them load until they have said so. The answer is keyed on the root
// and holds until it is withdrawn; beside it is the digest of each kind as a
// session last read it, which is what lets the next one say, once, what moved.
// The answer lives here rather than in the checkout because a file in a
// checkout is the thing being decided about.
// See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.

import "encoding/json"

// ProjectTrusted returns the digest of each kind the checkout under root was
// last read at, if it was ever trusted. A row recorded before digests were
// kept per kind answers with none.
func (db *DB) ProjectTrusted(root string) (map[string]string, bool) {
	var kinds string
	// No row and a broken read are the same answer here, and it is the safe
	// one: a checkout nobody can show an answer for is a checkout that has
	// not been answered for.
	if err := db.sql.QueryRow(`SELECT kinds FROM project_trust WHERE root = ?`, root).Scan(&kinds); err != nil {
		return nil, false
	}
	var digests map[string]string
	// A column that will not parse is read as a row with no digests: the
	// answer is still on record, and the next session stamps it again.
	_ = json.Unmarshal([]byte(kinds), &digests)
	return digests, true
}

// TrustProject records that the checkout under root may load what it
// defines, with the digests it stands at, replacing an earlier answer.
func (db *DB) TrustProject(root, fingerprint string, kinds map[string]string) error {
	enc, err := json.Marshal(kinds)
	if err != nil {
		return err
	}
	_, err = db.sql.Exec(
		`INSERT INTO project_trust (root, fingerprint, kinds) VALUES (?, ?, ?)
		 ON CONFLICT(root) DO UPDATE SET fingerprint = excluded.fingerprint, kinds = excluded.kinds,
		 trusted_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		root, fingerprint, string(enc))
	return err
}

// RestampProject moves an existing answer to the digests the checkout stands
// at now, so a change is told once rather than at every session. It never
// creates an answer — a checkout with no row is one nobody has trusted — and
// it leaves when the answer was given alone. It reports whether there was a
// row to move.
func (db *DB) RestampProject(root, fingerprint string, kinds map[string]string) (bool, error) {
	enc, err := json.Marshal(kinds)
	if err != nil {
		return false, err
	}
	var had bool
	err = retryBusy(func() error {
		res, err := db.sql.Exec(`UPDATE project_trust SET fingerprint = ?, kinds = ? WHERE root = ?`,
			fingerprint, string(enc), root)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		had = n > 0
		return nil
	})
	return had, err
}

// DistrustProject withdraws the answer. It reports whether there was one.
func (db *DB) DistrustProject(root string) (bool, error) {
	res, err := db.sql.Exec(`DELETE FROM project_trust WHERE root = ?`, root)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
