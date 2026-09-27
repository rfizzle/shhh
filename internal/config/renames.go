package config

// A key that moved. The settings table is the file's version — there is no
// number written into the file and no migration run at startup — so a key
// that changes its spelling is the one kind of change the table alone cannot
// describe: the old spelling is no longer a row, and a file still holding it
// would read as a typo. This table is what says otherwise, and it is read in
// three places: the load's refusal, which names the new key and the command
// that moves it instead of only refusing; `config init --update`, which
// writes the value under the new name; and the doctor's config row, which
// counts it (docs/capabilities/configuration.md#an-older-file-is-brought-up-to-date).

import "strings"

// Rename is one key that moved: the spelling a file may still hold, the key
// the table reads now, and when it moved, which is what the comment the
// update leaves beside the value says.
type Rename struct {
	From  string
	To    string
	Moved string
}

// renames is every key that moved, oldest first. An entry stays for as long
// as a file written before the move may still be out there, which is to say
// it stays.
var renames = []Rename{
	// The role models were one key each before any role could have a
	// profile table of its own.
	{From: "agents.researcher_model", To: "agents.profiles.researcher.model", Moved: "v0.9.5"},
	{From: "agents.writer_model", To: "agents.profiles.writer.model", Moved: "v0.9.5"},
	{From: "agents.reviewer_model", To: "agents.profiles.reviewer.model", Moved: "v0.9.5"},
}

// Renames is the table, for a surface that lists what moved.
func Renames() []Rename { return renames }

// renameOf is the entry for a key a file spells the old way. The decoder
// matches a file's keys without regard to case, so this does too.
func renameOf(key string) (Rename, bool) {
	for _, r := range renames {
		if strings.EqualFold(r.From, key) {
			return r, true
		}
	}
	return Rename{}, false
}
