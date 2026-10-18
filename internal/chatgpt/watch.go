package chatgpt

import (
	"errors"
	"os"
	"time"
)

// RecordStamp identifies one version of the credential record on disk: the
// file's identity, size and modification time, which change whenever another
// process replaces it. A holder that has read the record keeps the stamp it
// read under and compares it with a fresh one, which is a stat and no read.
type RecordStamp struct {
	info os.FileInfo
	size int64
	mod  time.Time
}

// Same reports whether two stamps name one version of the record. Two stamps
// of an absent record are the same.
func (s RecordStamp) Same(o RecordStamp) bool {
	if (s.info == nil) != (o.info == nil) {
		return false
	}
	if s.info == nil {
		return true
	}
	return s.size == o.size && s.mod.Equal(o.mod) && os.SameFile(s.info, o.info)
}

// StatRecord stamps the record in the store, creating nothing. An absent
// record, or one that cannot be statted, is the zero stamp; a caller learns
// what the record holds from ReadRecord.
func StatRecord(store FileStore) RecordStamp {
	if store.Dir == "" {
		return RecordStamp{}
	}
	info, err := os.Stat(store.CredentialsPath())
	if err != nil {
		return RecordStamp{}
	}
	return RecordStamp{info: info, size: info.Size(), mod: info.ModTime()}
}

// ReadRecord reads the record in the store the way an Account does, without
// opening one: no directory is created, no lock is taken, and nothing is
// logged. A record that is not there is the zero Credential and no error.
func ReadRecord(store FileStore) (Credential, error) { return readRecord(store) }

// errMalformed marks a record that was read and did not parse.
type errMalformed struct{ error }

func (e errMalformed) Unwrap() error { return e.error }

// IsMalformed reports whether ReadRecord read the record and found it is not
// one, which a holder remembers as a version rather than trying again.
func IsMalformed(err error) bool {
	var m errMalformed
	return errors.As(err, &m)
}
