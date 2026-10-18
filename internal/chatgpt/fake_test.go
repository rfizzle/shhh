package chatgpt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/testhttp"
)

// The tokens the fake server issues are opaque strings, long enough that
// every eight-byte run is distinctive, so a test can look for any piece of
// one in anything that must not carry it.
const (
	issuedClient = "oaiapp_testclient123"
	testSubject  = "user-abc123"
	testEmail    = "person@example.com"
)

// heldClock is a clock a test moves by hand.
type heldClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *heldClock { return &heldClock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)} }

func (c *heldClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *heldClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeAuth is auth.openai.com in memory: discovery, a key set, the token
// endpoint and revocation. It is reached through testhttp under the real
// host name, so the package's fixed URLs are the ones exercised and no
// request can leave the process.
type fakeAuth struct {
	t     *testing.T
	clock *heldClock

	mu  sync.Mutex
	key *ecdsa.PrivateKey
	kid string
	// sub and email are who the ID token says signed in.
	sub, email string
	// scope is what the token endpoint grants; "" leaves it out.
	scope string
	// nonce is what the last authorize URL asked for, which the ID token
	// carries back unless badNonce is set.
	nonce    string
	badNonce bool
	// exchangeCode and refreshCode, when set, refuse that grant with the
	// code and status.
	exchangeCode   string
	refreshCode    string
	refreshStatus  int
	refreshDelay   time.Duration
	tokenRequests  []url.Values
	revokeRequests []url.Values
	revokeStatus   []int
	serial         int
}

func newFakeAuth(t *testing.T, clock *heldClock) (*fakeAuth, *http.Client) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAuth{t: t, clock: clock, key: key, kid: "k1", sub: testSubject, email: testEmail, scope: Scopes}
	reg := &testhttp.Registry{}
	srv := reg.NewServer(f)
	srv.Alias("auth.openai.com")
	t.Cleanup(srv.Close)
	return f, reg.Client()
}

func (f *fakeAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, http.StatusOK, map[string]string{
			"issuer":              Issuer,
			"jwks_uri":            Issuer + "/jwks",
			"revocation_endpoint": Issuer + "/oauth/revoke",
		})
	case "/jwks":
		f.mu.Lock()
		pub := f.key.PublicKey
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{ecJWK(&pub, f.kid)}})
	case "/api/accounts/oauth/token":
		_ = r.ParseForm()
		f.token(w, r.PostForm)
	case "/oauth/revoke":
		_ = r.ParseForm()
		f.mu.Lock()
		f.revokeRequests = append(f.revokeRequests, r.PostForm)
		status := http.StatusOK
		if len(f.revokeStatus) > 0 {
			status, f.revokeStatus = f.revokeStatus[0], f.revokeStatus[1:]
		}
		f.mu.Unlock()
		w.WriteHeader(status)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAuth) token(w http.ResponseWriter, form url.Values) {
	f.mu.Lock()
	f.tokenRequests = append(f.tokenRequests, form)
	f.serial++
	n := f.serial
	delay := f.refreshDelay
	f.mu.Unlock()
	switch form.Get("grant_type") {
	case "authorization_code":
		if f.exchangeCode != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": f.exchangeCode})
			return
		}
		f.mu.Lock()
		nonce := f.nonce
		if f.badNonce {
			nonce = "not-the-nonce"
		}
		claims := map[string]any{
			"iss": Issuer, "sub": f.sub, "aud": form.Get("client_id"), "email": f.email,
			"exp": f.clock.Now().Add(time.Hour).Unix(), "iat": f.clock.Now().Unix(), "nonce": nonce,
		}
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, f.tokenSet(n, mintES256(f.t, f.key, f.kid, claims)))
	case "refresh_token":
		if delay > 0 {
			time.Sleep(delay)
		}
		if f.refreshCode != "" || f.refreshStatus != 0 {
			status := f.refreshStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			body := map[string]string{}
			if f.refreshCode != "" {
				body["error"] = f.refreshCode
			}
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, f.tokenSet(n, ""))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

// tokenSet is a token response numbered n, so every set is distinct.
func (f *fakeAuth) tokenSet(n int, idToken string) map[string]any {
	set := map[string]any{
		"access_token":        "at_" + strings.Repeat("A", 8) + "_" + itoa(n) + "_accessvalue",
		"refresh_token":       "rt_" + strings.Repeat("R", 8) + "_" + itoa(n) + "_refreshvalue",
		"token_type":          "Bearer",
		"expires_in":          3600,
		"earliest_refresh_at": f.clock.Now().Add(30 * time.Minute).Unix(),
	}
	if idToken != "" {
		set["id_token"] = idToken
	}
	if f.scope != "" {
		set["scope"] = f.scope
	}
	return set
}

func (f *fakeAuth) tokenCount(grant string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, form := range f.tokenRequests {
		if form.Get("grant_type") == grant {
			n++
		}
	}
	return n
}

func itoa(n int) string { return big.NewInt(int64(n)).String() }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ecJWK(pub *ecdsa.PublicKey, kid string) map[string]string {
	raw, _ := pub.Bytes()
	return map[string]string{
		"kty": "EC", "crv": "P-256", "kid": kid, "alg": "ES256", "use": "sig",
		"x": base64.RawURLEncoding.EncodeToString(raw[1:33]),
		"y": base64.RawURLEncoding.EncodeToString(raw[33:]),
	}
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]string {
	return map[string]string{
		"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func segment(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func mintES256(t *testing.T, key *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head := segment(t, map[string]string{"alg": "ES256", "kid": kid, "typ": "JWT"})
	body := segment(t, claims)
	digest := sha256.Sum256([]byte(head + "." + body))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func mintRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head := segment(t, map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	body := segment(t, claims)
	digest := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// openTest opens an account on a temporary directory against the fake.
func openTest(t *testing.T, dir string, client *http.Client, clock *heldClock, onTokens func(a, r, i string)) *Account {
	t.Helper()
	a, err := Open(Options{Dir: dir, Client: client, Now: clock.Now, OnTokens: onTokens})
	if err != nil {
		t.Fatal(err)
	}
	a.lockPoll = 2 * time.Millisecond
	a.sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return a
}

// seed writes a signed-in record whose access token expires in life.
func seed(t *testing.T, a *Account, clock *heldClock, life time.Duration) Credential {
	t.Helper()
	c := Credential{
		Email: testEmail, Issuer: Issuer, Subject: testSubject, ClientID: issuedClient, HostID: "urn:uuid:seed",
		IDToken: "id_seeded_value_1234567", AccessToken: "at_seeded_value_1234567", RefreshToken: "rt_seeded_value_1234567",
		TokenType: "Bearer", ExpiresAt: clock.Now().Add(life), Scopes: strings.Fields(Scopes), SavedAt: clock.Now(),
	}
	if err := a.write(c); err != nil {
		t.Fatal(err)
	}
	return c
}

// pipeListener is a loopback listener with no socket: the callback server
// is served over in-memory pipes, so a sign-in runs end to end in the
// hermetic tier. The real listener is the contract test's.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// browserClient is the person's browser, reaching the callback through the
// pipe listener.
func (l *pipeListener) browserClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: l.dial}, Timeout: 5 * time.Second}
}

// browser plays the person's side of a sign-in: it records the authorize
// URL, tells the fake the nonce, and calls back with what reply makes of
// the query. A nil reply is a person who approved.
type browser struct {
	t     *testing.T
	fake  *fakeAuth
	ln    *pipeListener
	mu    sync.Mutex
	pages []string
	reply func(q url.Values) url.Values
	got   chan int
}

func newBrowser(t *testing.T, fake *fakeAuth, ln *pipeListener) *browser {
	return &browser{t: t, fake: fake, ln: ln, got: make(chan int, 4)}
}

func (b *browser) open(page string) error {
	b.mu.Lock()
	b.pages = append(b.pages, page)
	b.mu.Unlock()
	u, err := url.Parse(page)
	if err != nil {
		return err
	}
	q := u.Query()
	b.fake.mu.Lock()
	b.fake.nonce = q.Get("nonce")
	b.fake.mu.Unlock()
	back := url.Values{"state": {q.Get("state")}, "code": {"auth-code-xyz"}}
	if q.Get("client_id") == DynamicClient {
		back.Set("client_id", issuedClient)
	}
	if b.reply != nil {
		back = b.reply(back)
	}
	go func() {
		resp, err := b.ln.browserClient().Get(q.Get("redirect_uri") + "?" + back.Encode())
		if err != nil {
			b.got <- 0
			return
		}
		resp.Body.Close()
		b.got <- resp.StatusCode
	}()
	return nil
}

func (b *browser) page(t *testing.T) url.Values {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pages) == 0 {
		t.Fatal("no page was opened")
	}
	u, err := url.Parse(b.pages[len(b.pages)-1])
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// loginOpts are the options a test signs in with: this browser, the pipe
// listener, and a short wait.
func (b *browser) loginOpts() LoginOptions {
	return LoginOptions{
		Browser: b.open,
		Listen:  func() (net.Listener, error) { return b.ln, nil },
		Wait:    5 * time.Second,
	}
}
