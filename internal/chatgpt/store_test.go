package chatgpt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// preStoreRecord is the exact text the code before FileStore wrote for the
// credential below (config.ReplaceFile of MarshalIndent plus a newline),
// captured from that code. It is the on-disk format the store must keep.
const preStoreRecord = "{\n  \"email\": \"a@b.c\",\n  \"issuer\": \"https://auth.openai.com\",\n  \"subject\": \"sub1\",\n  \"client_id\": \"cl1\",\n  \"host_id\": \"urn:uuid:11111111-2222-4333-8444-555555555555\",\n  \"id_token\": \"id.tok.sig\",\n  \"access_token\": \"acc.tok.sig\",\n  \"refresh_token\": \"rt_refresh\",\n  \"token_type\": \"Bearer\",\n  \"expires_at\": \"2026-10-18T13:00:00Z\",\n  \"earliest_refresh_at\": \"2026-10-18T12:30:00Z\",\n  \"scopes\": [\n    \"openid\",\n    \"chatgpt.tokens.use.direct\"\n  ],\n  \"saved_at\": \"2026-10-18T12:00:00Z\",\n  \"welcomed_at\": \"2026-10-18T12:00:00Z\"\n}\n"

const preStoreHostID = "urn:uuid:11111111-2222-4333-8444-555555555555"

func preStoreCredential() Credential {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	return Credential{
		Email: "a@b.c", Issuer: Issuer, Subject: "sub1", ClientID: "cl1", HostID: preStoreHostID,
		IDToken: "id.tok.sig", AccessToken: "acc.tok.sig", RefreshToken: "rt_refresh", TokenType: "Bearer",
		ExpiresAt: at.Add(time.Hour), EarliestRefreshAt: at.Add(30 * time.Minute),
		Scopes: []string{"openid", PlanScope}, SavedAt: at, WelcomedAt: at,
	}
}

// A record and a host ID written before the store existed are read after it,
// and the store writes the same bytes the old code did, so the old code reads
// what the new one writes: the format and the file names did not change.
func TestFileStore_KeepsTheOnDiskFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(preStoreRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host-id"), []byte(preStoreHostID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(Options{Store: FileStore{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Credential()
	if err != nil {
		t.Fatal(err)
	}
	want := preStoreCredential()
	if got.RefreshToken != want.RefreshToken || got.ClientID != want.ClientID || !got.ExpiresAt.Equal(want.ExpiresAt) || !got.WelcomedAt.Equal(want.WelcomedAt) || !got.PlanGranted() {
		t.Fatalf("the record written before the store was misread: %+v", got)
	}
	if id, err := a.HostID(); err != nil || id != preStoreHostID {
		t.Fatalf("host ID = %q, %v", id, err)
	}

	// The reverse: a fresh directory written by the store holds, byte for
	// byte, what the old code wrote.
	fresh := t.TempDir()
	b, err := Open(Options{Store: FileStore{Dir: fresh}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.write(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fresh, "credentials.json"))
	if err != nil || string(data) != preStoreRecord {
		t.Fatalf("the store wrote %q, %v; want the pre-store bytes", data, err)
	}
	if err := b.store.writeHostID(preStoreHostID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(fresh, "host-id")); string(data) != preStoreHostID+"\n" {
		t.Fatalf("host-id = %q", data)
	}
	for _, name := range []string{"credentials.json", "host-id"} {
		if info, err := os.Stat(filepath.Join(fresh, name)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
}

func TestOpen_NeedsAStoreDirectory(t *testing.T) {
	if _, err := Open(Options{}); err == nil {
		t.Fatal("an account with no store directory opened")
	}
	if HasRecord(FileStore{}) {
		t.Fatal("an empty store holds a record")
	}
}
