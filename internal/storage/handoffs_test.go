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
