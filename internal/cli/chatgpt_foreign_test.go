package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/chatgpt"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/tools"
)

// A session opened before another process signs in or refreshes learns
// nothing of the new tokens from the account, so the model's two ways to the
// record are closed separately: the read tools refuse the path, and the
// shell's output is scrubbed after the vault has re-read the record. Nothing
// of the new refresh token or of either JWT's signature reaches what the
// model reads or what the evidence store keeps.
// See docs/capabilities/chatgpt-plan.md#what-keeps-the-tokens-from-the-model.
func TestChatGPT_ARecordAnotherProcessWroteNeverReachesTheModel(t *testing.T) {
	dir := resetChatGPT(t)
	signInRecord(t, testChatGPT)
	vault, err := loadSecrets(config.Config{}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(t.TempDir(), "evidence")
	store, err := evidence.OpenAt(evDir, "s")
	if err != nil {
		t.Fatal(err)
	}
	reducer := evidence.NewReducer(store)
	reducer.SetScrub(vault.Scrub)

	// Another process refreshes: new tokens, in a record of another size,
	// swapped in whole the way the account writes it.
	random := func() string {
		b := make([]byte, 192)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	header := seg(`{"alg":"RS256","typ":"JWT"}`)
	newAccess := header + "." + seg(`{"iss":"https://auth.openai.com","aud":"x"}`) + "." + random()
	newID := header + "." + seg(`{"iss":"https://auth.openai.com","email":"p@example.com"}`) + "." + random()
	newRefresh := "rftok_" + random()
	next := testChatGPT
	next.AccessToken, next.IDToken, next.RefreshToken = newAccess, newID, newRefresh
	next.ExpiresAt = time.Now().Add(time.Hour)
	data, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "other-process.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "credentials.json")
	if err := os.Rename(tmp, record); err != nil {
		t.Fatal(err)
	}

	// The model's tools: the read tool is shhh's own, and the shell reads the
	// file the way a command with no containment does.
	run := func(_ context.Context, name string, args json.RawMessage) (string, error) {
		if name == "execute_command" {
			b, err := os.ReadFile(record)
			return string(b) + strings.Repeat(" ", 1<<16), err
		}
		return tools.Execute(context.Background(), name, args)
	}
	exec := vault.WrapExecutor(reducer.WrapExecutor(run))

	var seen []string
	out, err := exec(context.Background(), tools.ReadFileName, json.RawMessage(`{"path":`+quote(record)+`}`))
	if err == nil || !strings.Contains(err.Error(), "never shown to the model") {
		t.Errorf("read_file of the record = %q, %v; want the refusal", out, err)
	}
	seen = append(seen, out)
	if err != nil {
		seen = append(seen, err.Error())
	}
	out, err = exec(context.Background(), "execute_command", json.RawMessage(`{"command":"cat credentials.json"}`))
	if err != nil {
		t.Fatal(err)
	}
	seen = append(seen, out)

	forms := []string{newRefresh, newAccess[strings.LastIndexByte(newAccess, '.')+1:], newID[strings.LastIndexByte(newID, '.')+1:]}
	check := func(what, text string) {
		t.Helper()
		for _, f := range forms {
			if strings.Contains(text, f) || strings.Contains(text, base64.StdEncoding.EncodeToString([]byte(f))) {
				t.Errorf("%s carries %.12q", what, f)
			}
			for i := 0; i+8 <= len(f); i += 7 {
				if strings.Contains(text, f[i:i+8]) {
					t.Errorf("%s carries a run of %.12q", what, f)
					break
				}
			}
		}
	}
	for _, text := range seen {
		check("what the model read", text)
	}
	if !strings.Contains(out, "[redacted:") {
		t.Errorf("the shell's output was not scrubbed: %.200q", out)
	}
	kept := 0
	if err := filepath.WalkDir(evDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		kept++
		b, rerr := os.ReadFile(p)
		check("the evidence store ("+d.Name()+")", string(b))
		return rerr
	}); err != nil {
		t.Fatal(err)
	}
	if kept == 0 {
		t.Error("the evidence store kept nothing, so it was not checked")
	}
}

// A scrub reads the record only when its version changed.
func TestTokenRelay_ReadsTheRecordOnlyWhenItChanged(t *testing.T) {
	dir := resetChatGPT(t)
	write := func(refresh string) {
		t.Helper()
		c := testChatGPT
		c.RefreshToken = refresh
		data, _ := json.Marshal(c)
		tmp := filepath.Join(dir, "next.tmp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "credentials.json")); err != nil {
			t.Fatal(err)
		}
	}
	r := &tokenRelay{}
	before := chatgpt.StatRecord(dir)
	r.refresh()
	if !before.Same(chatgpt.RecordStamp{}) || len(r.seen) != 0 {
		t.Fatal("an absent record is learned as nothing")
	}
	write("rftok_first_aaaaaaaa")
	r.refresh()
	if len(r.seen) == 0 {
		t.Fatal("a record that appeared was not learned")
	}
	n := len(r.seen)
	r.refresh()
	r.refresh()
	if len(r.seen) != n {
		t.Errorf("an unchanged record was learned again: %d -> %d", n, len(r.seen))
	}
	write("rftok_second_bbbbbbbbbbbb")
	r.refresh()
	if len(r.seen) <= n {
		t.Error("a rewritten record was not learned")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A record that reads but does not parse is a version too, and is not read
// again until something replaces it.
func TestTokenRelay_AMalformedRecordIsRememberedAsAVersion(t *testing.T) {
	dir := resetChatGPT(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	rl := &tokenRelay{}
	rl.refresh()
	if !rl.stamp.Same(chatgpt.StatRecord(dir)) {
		t.Error("the corrupt record would be read and parsed again at the next scrub")
	}
}
