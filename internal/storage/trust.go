package storage

// Trust for a checkout. The skills, agent profiles, quality suites, hooks
// and servers a repository defines are not the person's own decision, so
// none of them load until they have said so, and what they said so about is
// the checkout as it stood — the fingerprint — so an edit asks again. The
// answer lives here rather than in the checkout because a file in a checkout
// is the thing being decided about.
// See
// docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs.

// ProjectTrusted returns the fingerprint the checkout under root was
// trusted at.
func (db *DB) ProjectTrusted(root string) (string, bool) {
	var fp string
	// No row and a broken read are the same answer here, and it is the safe
	// one: a checkout nobody can show an answer for is a checkout that has
	// not been answered for.
	if err := db.sql.QueryRow(`SELECT fingerprint FROM project_trust WHERE root = ?`, root).Scan(&fp); err != nil {
		return "", false
	}
	return fp, true
}

// TrustProject records that the checkout under root may load what it
// defines, at this fingerprint, replacing an earlier answer.
func (db *DB) TrustProject(root, fingerprint string) error {
	_, err := db.sql.Exec(
		`INSERT INTO project_trust (root, fingerprint) VALUES (?, ?)
		 ON CONFLICT(root) DO UPDATE SET fingerprint = excluded.fingerprint, trusted_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		root, fingerprint)
	return err
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
