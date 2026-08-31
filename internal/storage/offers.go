package storage

// Offers already answered. The start screen offers a checkout the scaffold
// it has no way of writing for itself, and an offer that comes back after it
// has been refused is not an offer but a nag — so the refusal is remembered,
// keyed on the repository it was about
// (docs/interface/surfaces.md#the-start-screen).

// OfferDeclined reports that the named offer was refused under root.
func (db *DB) OfferDeclined(root, offer string) bool {
	var one int
	err := db.sql.QueryRow(`SELECT 1 FROM offers_declined WHERE root = ? AND offer = ?`, root, offer).Scan(&one)
	return err == nil
}

// DeclineOffer records the refusal. Answering twice is not an error: the
// second answer is the same answer.
func (db *DB) DeclineOffer(root, offer string) error {
	_, err := db.sql.Exec(
		`INSERT INTO offers_declined (root, offer) VALUES (?, ?) ON CONFLICT(root, offer) DO NOTHING`,
		root, offer)
	return err
}
