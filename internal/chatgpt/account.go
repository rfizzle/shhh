package chatgpt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage/datadir"
)

// The contract's fixed values, from OpenAI's sign-in documentation. Only the
// callback's port may vary: the redirect is registered as 127.0.0.1 and a
// `localhost` redirect is refused by the server.
const (
	Issuer        = "https://auth.openai.com"
	AuthorizeURL  = Issuer + "/api/accounts/authorize"
	TokenURL      = Issuer + "/api/accounts/oauth/token"
	DiscoveryURL  = Issuer + "/.well-known/openid-configuration"
	Resource      = "https://api.openai.com/v1"
	Scopes        = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	PlanScope     = "chatgpt.tokens.use.direct"
	DynamicClient = "dynamic_agent_client"
	AgentName     = "shhh"
	CallbackPath  = "/auth/callback"
	UsageURL      = "https://chatgpt.com/settings/usage"
	// RefreshMargin is how long before expiry a token is replaced. Five
	// minutes covers a long request started just before the hour is up
	// without refreshing a token that has most of its life left.
	RefreshMargin = 5 * time.Minute
	// TokenKind is the word a hidden token's placeholder carries.
	TokenKind = "chatgpt-token"
)

// The command every error below names, so the way out is in the sentence.
const loginCommand = "`shhh providers login chatgpt`"

var (
	// ErrNotSignedIn is a host with no usable sign-in: none was ever made,
	// or it was signed out. It is the plan route's own signed-out error, so
	// the provider and every report built on it say one sentence, not two.
	ErrNotSignedIn = provider.ErrChatGPTSignedOut
	// ErrSignInRequired is a sign-in the server will no longer refresh. The
	// tokens have been cleared; the account and the registration are kept.
	// It is also the plan route's signed-out error to errors.Is, because
	// what is to be done about it is the same.
	ErrSignInRequired error = signInExpired{}
	// ErrPlanNotGranted is a sign-in without the scope that lets a request
	// spend the plan.
	ErrPlanNotGranted = errors.New("signed in to ChatGPT, but plan use was not granted")
	// ErrDeclined is the person saying no in the browser.
	ErrDeclined = errors.New("sign-in was declined in the browser")
)

// signInExpired is ErrSignInRequired: its own words, and the signed-out
// error underneath.
type signInExpired struct{}

func (signInExpired) Error() string { return "the ChatGPT sign-in has expired — run " + loginCommand }
func (signInExpired) Unwrap() error { return provider.ErrChatGPTSignedOut }

// File names inside the account directory.
const (
	credentialsFile = "credentials.json"
	hostIDFile      = "host-id"
	lockFileName    = "credentials.lock"
)

// Credential is the record kept in credentials.json, with the fields
// OpenAI's documentation lists. HostID is recorded as the docs ask, but the
// host-id file beside the record is what a sign-in sends: a record copied
// from another machine must not make this one claim to be it.
type Credential struct {
	Email             string    `json:"email,omitempty"`
	Issuer            string    `json:"issuer,omitempty"`
	Subject           string    `json:"subject,omitempty"`
	ClientID          string    `json:"client_id,omitempty"`
	HostID            string    `json:"host_id,omitempty"`
	IDToken           string    `json:"id_token,omitempty"`
	AccessToken       string    `json:"access_token,omitempty"`
	RefreshToken      string    `json:"refresh_token,omitempty"`
	TokenType         string    `json:"token_type,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitzero"`
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitzero"`
	Scopes            []string  `json:"scopes,omitempty"`
	SavedAt           time.Time `json:"saved_at,omitzero"`
	// WelcomedAt is when this host showed the one-time "you're using your
	// ChatGPT plan" panel. It survives sign-out: the panel is once per host.
	WelcomedAt time.Time `json:"welcomed_at,omitzero"`
}

// SignedIn reports whether the record holds a refresh token, which is what
// keeps a sign-in alive past the hour.
func (c Credential) SignedIn() bool { return c.RefreshToken != "" }

// PlanGranted reports whether the sign-in may spend the plan.
func (c Credential) PlanGranted() bool { return slices.Contains(c.Scopes, PlanScope) }

// String and GoString keep the tokens out of anything that prints a
// credential with fmt: a debugging Printf("%v", cred) is how a refresh token
// ends up in a log, and this makes that line say whose record it was instead.
func (c Credential) String() string {
	if c.Email == "" {
		return "chatgpt credential"
	}
	return "chatgpt credential for " + c.Email
}

// GoString is String for %#v.
func (c Credential) GoString() string { return c.String() }

// cleared is the record with its tokens gone and everything that identifies
// the account and this host's registration kept: the next sign-in is a
// returning one, with the same client ID, and the one-time panel stays seen.
func (c Credential) cleared(now time.Time) Credential {
	c.IDToken, c.AccessToken, c.RefreshToken, c.TokenType = "", "", "", ""
	c.ExpiresAt, c.EarliestRefreshAt = time.Time{}, time.Time{}
	c.Scopes = nil
	c.SavedAt = now
	return c
}

// Options is how an Account is opened.
type Options struct {
	// Dir is the account directory; empty is <datadir>/chatgpt.
	Dir string
	// Client sends every request to the authorization server; nil is a
	// plain client with a timeout.
	Client *http.Client
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// OnTokens is told every set of tokens this account learns — read from
	// the record, exchanged at sign-in, or refreshed — so the caller can
	// hide them from the model. It is called with the account's lock held
	// and must not call back into the account.
	OnTokens func(access, refresh, id string)
}

// Account is one ChatGPT sign-in on this host. It is safe for concurrent
// use, and two Accounts on one directory — in one process or two — refresh
// one at a time: a refresh token is spent by using it, and a second process
// presenting the spent one is answered as a replay and signs the person out.
type Account struct {
	dir      string
	client   *http.Client
	now      func() time.Time
	onTokens func(access, refresh, id string)

	// mu serialises the account's own callers; the file lock does the same
	// across Accounts and processes. It is taken first, always.
	mu       sync.Mutex
	reported string
	// pending is a refreshed record the disk would not take. The refresh
	// token in it is the only live one — the server spent the one on disk
	// when it issued it — so it is kept here and written by the next caller
	// that holds the lock, rather than dropped and the person signed out.
	// pendingFrom is the refresh token it replaced: a record on disk that no
	// longer holds that one was changed by someone else since, and wins.
	pending     *Credential
	pendingFrom string

	// lockWait bounds how long a caller waits for another process's
	// refresh, and lockPoll is how often it looks. A refresh is one HTTP
	// round trip, so thirty seconds is a server that has stopped answering.
	lockWait time.Duration
	lockPoll time.Duration
	// sleep is the revocation backoff's wait, replaced in tests.
	sleep func(context.Context, time.Duration) error
}

// requestTimeout bounds every request to the authorization server. They are
// small form posts and JSON documents; a minute would be a server that is
// down.
const requestTimeout = 30 * time.Second

// Open opens the account directory, creating it owner-only. It reads
// nothing: a host that never signs in pays for a directory and no more.
func Open(o Options) (*Account, error) {
	dir := o.Dir
	if dir == "" {
		var err error
		if dir, err = defaultDir(); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// A directory that was already there keeps nobody else's read on it:
	// the record inside is a credential.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	a := &Account{
		dir: dir, client: o.Client, now: o.Now, onTokens: o.OnTokens,
		lockWait: 30 * time.Second, lockPoll: 50 * time.Millisecond, sleep: sleepCtx,
	}
	// Redirects are never followed. A 307 or 308 from the token endpoint
	// would re-post the form — the refresh token in it — to wherever the
	// Location names; a redirect is an answer that is not 200, and is
	// refused as one. The caller's client is copied, not changed.
	client := http.Client{Timeout: requestTimeout}
	if a.client != nil {
		client = *a.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	a.client = &client
	if a.now == nil {
		a.now = time.Now
	}
	return a, nil
}

// HasRecord reports whether the account directory holds a credential
// record, creating nothing: a caller that only wants to hide tokens that
// exist must not leave a directory behind on a host that never signed in.
// An empty dir is the default one.
func HasRecord(dir string) bool {
	if dir == "" {
		var err error
		if dir, err = defaultDir(); err != nil {
			return false
		}
	}
	_, err := os.Stat(filepath.Join(dir, credentialsFile))
	return err == nil
}

// defaultDir is <datadir>/chatgpt.
func defaultDir() (string, error) {
	data, err := datadir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "chatgpt"), nil
}

// Dir is the account directory, for a sentence that names where the record
// is.
func (a *Account) Dir() string { return a.dir }

// CredentialsPath is the record's path.
func (a *Account) CredentialsPath() string { return filepath.Join(a.dir, credentialsFile) }

// Credential reads the record as it is on disk. No record is the zero
// Credential and no error. The read takes no lock: the record is only ever
// replaced whole, so a read sees one writer's file or the other's.
func (a *Account) Credential() (Credential, error) { return a.read() }

func (a *Account) read() (Credential, error) { return readRecord(a.CredentialsPath()) }

func readRecord(path string) (Credential, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Credential{}, nil
	}
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		// The decoder's error names a character or a type, never the text
		// around it, so it cannot carry a token into the message.
		return Credential{}, fmt.Errorf("%s is not a ChatGPT credential record: %w", path, err)
	}
	return c, nil
}

// current is the record as it stands, for a caller holding both locks: a
// pending refresh is written first, and when the disk still refuses it the
// pending record is the answer, because it holds the live refresh token.
func (a *Account) current() (Credential, error) {
	disk, err := a.read()
	if a.pending == nil {
		return disk, err
	}
	if err == nil && disk.RefreshToken != a.pendingFrom {
		a.pending, a.pendingFrom = nil, ""
		return disk, nil
	}
	if werr := a.write(*a.pending); werr != nil {
		return *a.pending, nil
	}
	c := *a.pending
	a.pending, a.pendingFrom = nil, ""
	return c, nil
}

// write replaces the record whole and owner-only. ReplaceFile keeps the
// mode of a file already there, so a record copied in with a looser mode is
// narrowed first rather than rewritten as readable as it arrived.
func (a *Account) write(c Credential) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := a.CredentialsPath()
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	}
	return config.ReplaceFile(path, string(data)+"\n", 0o600)
}

// report tells OnTokens about a set it has not been told about yet.
func (a *Account) report(c Credential) {
	if a.onTokens == nil || (c.AccessToken == "" && c.RefreshToken == "" && c.IDToken == "") {
		return
	}
	key := c.AccessToken + "\x00" + c.RefreshToken + "\x00" + c.IDToken
	if key == a.reported {
		return
	}
	a.reported = key
	a.onTokens(c.AccessToken, c.RefreshToken, c.IDToken)
}

// withLock runs fn holding the advisory lock on credentials.lock. The lock
// is a file of its own because the record is replaced by a rename: a lock
// held on the record would be on the old file the moment the first writer
// renamed over it, and the second writer would take a lock on the new one.
func (a *Account) withLock(ctx context.Context, fn func() error) error {
	f, err := os.OpenFile(filepath.Join(a.dir, lockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(a.lockWait)
	for {
		ok, err := lockFile(f)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another shhh has held the ChatGPT sign-in for %s; try again: %w", a.lockWait, context.DeadlineExceeded)
		}
		if err := sleepCtx(ctx, a.lockPoll); err != nil {
			return err
		}
	}
	defer unlockFile(f)
	return fn()
}

// HostID is this host's identifier, generated on first need and kept from
// then on, sign-out and a copied-in record included: OpenAI attributes plan
// use per host, and a new ID would read as a new machine.
func (a *Account) HostID() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var id string
	err := a.withLock(context.Background(), func() error {
		var err error
		id, err = a.ensureHostID()
		return err
	})
	return id, err
}

// ensureHostID reads the host-id file or writes a new one. It is called with
// both locks held, so two processes signing in for the first time agree on
// one ID.
func (a *Account) ensureHostID() (string, error) {
	path := filepath.Join(a.dir, hostIDFile)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if !validHostID(id) {
			return "", fmt.Errorf("%s does not hold a host ID; delete it to have a new one made", path)
		}
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id, err := newHostID()
	if err != nil {
		return "", err
	}
	if err := config.ReplaceFile(path, id+"\n", 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// validHostID accepts the three forms OpenAI's documentation names. shhh
// writes the first; the others are accepted so a person who set one by hand
// keeps it.
func validHostID(id string) bool {
	for _, prefix := range []string{"urn:uuid:", "urn:ietf:params:oauth:jwk-thumbprint:", "did:key:"} {
		if strings.HasPrefix(id, prefix) && len(id) > len(prefix) && !strings.ContainsAny(id, " \t\r\n") {
			return true
		}
	}
	return false
}

// newHostID is a random (version 4) UUID as a URN. A key-thumbprint ID would
// need a key pair kept beside it — a second secret — for a proof of
// possession the server does not ask for.
func newHostID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return "urn:uuid:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// Clear signs out locally: the tokens go and the account, the client ID, the
// host ID and the one-time panel's mark stay.
func (a *Account) Clear() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withLock(context.Background(), func() error {
		c, err := a.current()
		if err != nil {
			return err
		}
		return a.write(c.cleared(a.now()))
	})
}

// MarkWelcomed records that this host has shown the one-time plan panel.
func (a *Account) MarkWelcomed() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withLock(context.Background(), func() error {
		c, err := a.current()
		if err != nil {
			return err
		}
		c.WelcomedAt = a.now()
		return a.write(c)
	})
}

// Import takes a record signed in on another machine — the remote-host case,
// where no browser can reach this one's loopback — and keeps it as this
// host's, under this host's own ID.
// See docs/capabilities/chatgpt-plan.md#signing-in-for-a-remote-host.
func (a *Account) Import(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var in Credential
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("%s is not a ChatGPT credential record: %w", path, err)
	}
	if !in.SignedIn() || in.ClientID == "" {
		return fmt.Errorf("%s holds no sign-in to import", path)
	}
	if in.Issuer != "" && in.Issuer != Issuer {
		return fmt.Errorf("%s was not issued by %s", path, Issuer)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withLock(context.Background(), func() error {
		id, err := a.ensureHostID()
		if err != nil {
			return err
		}
		cur, err := a.current()
		if err != nil {
			return err
		}
		in.HostID = id
		in.WelcomedAt = cur.WelcomedAt
		in.SavedAt = a.now()
		if err := a.write(in); err != nil {
			return err
		}
		a.report(in)
		return nil
	})
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
