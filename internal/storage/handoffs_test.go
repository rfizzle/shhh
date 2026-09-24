package storage

import (
	"bytes"
	"testing"
)

func TestChildHandoff_RoundTripsByOpaqueHandle(t *testing.T) {
	db := openTestDB(t)
	id, err := db.StartAgentSession("researcher", "test", "model")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"task":"survey"}`)
	handle, err := db.SaveChildHandoff(id, want)
	if err != nil {
		t.Fatal(err)
	}
	if handle == "" || handle == string(want) {
		t.Fatalf("handle must be opaque: %q", handle)
	}
	got, err := db.LoadChildHandoff(handle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if _, err := db.LoadChildHandoff("handoff-missing"); err == nil {
		t.Fatal("missing handoff should be refused")
	}
}

func TestChildHandoff_UpdateKeepsTheHandle(t *testing.T) {
	db := openTestDB(t)
	id, err := db.StartAgentSession("writer", "test", "model")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := db.SaveChildHandoff(id, []byte(`{"task":"write","patch_evidence":"ev-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"task":"write","landed":true}`)
	if err := db.UpdateChildHandoff(handle, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadChildHandoff(handle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if err := db.UpdateChildHandoff("handoff-missing", want); err == nil {
		t.Fatal("updating a missing handoff should be refused")
	}
}
