package chatgpt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// An Account is the plan route's bearer source by its method set alone:
// this package imports no provider package, and the provider holds the
// account through an interface with exactly this method.
var _ interface {
	Token(ctx context.Context, refused string) (string, error)
} = (*Account)(nil)

// The account directory is owner-only whether Open made it or found it.
func TestOpen_CreatesTheDirectoryOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX mode bits")
	}
	dir := filepath.Join(t.TempDir(), "data", "chatgpt")
	clock := newClock()
	openTest(t, dir, nil, clock, nil)
	for _, d := range []string{dir, filepath.Dir(dir)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, info.Mode().Perm())
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	openTest(t, dir, nil, clock, nil)
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("a reopened directory kept mode %o", info.Mode().Perm())
	}
}

// The host ID is a version 4 UUID URN, made once and read back from then
// on, and a sign-out leaves it alone.
func TestHostID_GeneratedOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	clock := newClock()
	a := openTest(t, dir, nil, clock, nil)
	id, err := a.HostID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("host ID %q is not a v4 urn:uuid", id)
	}
	again, err := openTest(t, dir, nil, clock, nil).HostID()
	if err != nil || again != id {
		t.Fatalf("second Open's HostID = %q, %v; want %q", again, err, id)
	}
	seed(t, a, clock, time.Hour)
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	if after, _ := a.HostID(); after != id {
		t.Errorf("HostID after Clear = %q, want %q", after, id)
	}
	if err := os.WriteFile(filepath.Join(dir, hostIDFile), []byte("not an id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HostID(); err == nil {
		t.Error("a malformed host-id was accepted")
	}
}

// The record is written whole and owner-only, leaves no temporary file
// beside it, and a record that arrived readable by others is narrowed.
func TestSave_IsAtomicAndOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	clock := newClock()
	a := openTest(t, dir, nil, clock, nil)
	want := seed(t, a, clock, time.Hour)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(a.CredentialsPath())
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("credentials.json mode = %o, want 600", info.Mode().Perm())
		}
		if err := os.Chmod(a.CredentialsPath(), 0o644); err != nil {
			t.Fatal(err)
		}
		seed(t, a, clock, time.Hour)
		if info, _ := os.Stat(a.CredentialsPath()); info.Mode().Perm() != 0o600 {
			t.Errorf("a rewritten record kept mode %o", info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
	got, err := a.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != want.RefreshToken || got.ClientID != want.ClientID || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("read back %+v", got)
	}
	data, _ := os.ReadFile(a.CredentialsPath())
	for _, field := range []string{"email", "issuer", "subject", "client_id", "host_id", "id_token", "access_token",
		"refresh_token", "token_type", "expires_at", "scopes", "saved_at"} {
		if !strings.Contains(string(data), `"`+field+`"`) {
			t.Errorf("the record has no %s field:\n%s", field, data)
		}
	}
}

// Clear takes the tokens and keeps who the account is, this host's
// registration and its ID, and the one-time panel's mark.
func TestClear_KeepsClientHostAndWelcome(t *testing.T) {
	clock := newClock()
	a := openTest(t, t.TempDir(), nil, clock, nil)
	hostID, _ := a.HostID()
	seed(t, a, clock, time.Hour)
	if err := a.MarkWelcomed(); err != nil {
		t.Fatal(err)
	}
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	c, err := a.Credential()
	if err != nil {
		t.Fatal(err)
	}
	if c.SignedIn() || c.AccessToken != "" || c.IDToken != "" || len(c.Scopes) != 0 || !c.ExpiresAt.IsZero() {
		t.Errorf("Clear left tokens: %+v", c)
	}
	if c.ClientID != issuedClient || c.Subject != testSubject || c.Email != testEmail || c.WelcomedAt.IsZero() {
		t.Errorf("Clear lost the account: client=%q sub=%q email=%q welcomed=%v", c.ClientID, c.Subject, c.Email, c.WelcomedAt)
	}
	if after, _ := a.HostID(); after != hostID {
		t.Errorf("Clear changed the host ID")
	}
}

// A record copied from another machine is kept under this machine's host
// ID, and this machine's one-time panel mark.
func TestImport_KeepsThisHostsID(t *testing.T) {
	clock := newClock()
	laptop := openTest(t, t.TempDir(), nil, clock, nil)
	seed(t, laptop, clock, time.Hour)
	laptopID, _ := laptop.HostID()

	vm := openTest(t, t.TempDir(), nil, clock, nil)
	vmID, _ := vm.HostID()
	if err := vm.MarkWelcomed(); err != nil {
		t.Fatal(err)
	}
	if err := vm.Import(laptop.CredentialsPath()); err != nil {
		t.Fatal(err)
	}
	c, _ := vm.Credential()
	if c.HostID != vmID || c.HostID == laptopID {
		t.Errorf("imported HostID = %q, want this host's %q", c.HostID, vmID)
	}
	if !c.SignedIn() || c.ClientID != issuedClient || c.WelcomedAt.IsZero() {
		t.Errorf("imported record = %+v", c)
	}
	if after, _ := vm.HostID(); after != vmID {
		t.Errorf("Import replaced the host-id file")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	_ = os.WriteFile(empty, []byte(`{"email":"x"}`), 0o600)
	if err := vm.Import(empty); err == nil {
		t.Error("a record with no sign-in was imported")
	}
}

// A credential printed with fmt names the account and none of its tokens.
func TestCredential_PrintsNoToken(t *testing.T) {
	clock := newClock()
	c := seed(t, openTest(t, t.TempDir(), nil, clock, nil), clock, time.Hour)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, c)
		for _, tok := range []string{c.AccessToken, c.RefreshToken, c.IDToken} {
			if strings.Contains(out, tok) {
				t.Errorf("%s prints a token: %s", verb, out)
			}
		}
	}
}
