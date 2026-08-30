package storage

import "database/sql"

// Trust for project-scope MCP servers. A definition in a checkout is not
// the person's own decision, so it does not start until they have said so,
// and what they said so about is the definition as it was — the fingerprint
// — so an edit to the file asks again. The answer lives here rather than in
// the checkout because a file in a checkout is the thing being decided
// about (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).

// MCPTrusted returns the fingerprint a project server was trusted at.
func (db *DB) MCPTrusted(root, name string) (string, bool) {
	var fp string
	err := db.sql.QueryRow(`SELECT fingerprint FROM mcp_trust WHERE root = ? AND name = ?`, root, name).Scan(&fp)
	if err == sql.ErrNoRows || err != nil {
		return "", false
	}
	return fp, true
}

// TrustMCP records that the server named may start under root, at this
// fingerprint, replacing an earlier answer.
func (db *DB) TrustMCP(root, name, fingerprint string) error {
	_, err := db.sql.Exec(
		`INSERT INTO mcp_trust (root, name, fingerprint) VALUES (?, ?, ?)
		 ON CONFLICT(root, name) DO UPDATE SET fingerprint = excluded.fingerprint, trusted_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		root, name, fingerprint)
	return err
}

// DistrustMCP withdraws the answer. It reports whether there was one.
func (db *DB) DistrustMCP(root, name string) (bool, error) {
	res, err := db.sql.Exec(`DELETE FROM mcp_trust WHERE root = ? AND name = ?`, root, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
