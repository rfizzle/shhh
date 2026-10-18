package chatgpt

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// signIn runs one sign-in against the fake with a fresh listener.
func signIn(t *testing.T, a *Account, fake *fakeAuth, reply func(url.Values) url.Values, edit func(*LoginOptions)) (*browser, Result, error) {
	t.Helper()
	b := newBrowser(t, fake, newPipeListener())
	b.reply = reply
	o := b.loginOpts()
	if edit != nil {
		edit(&o)
	}
	res, err := a.Login(context.Background(), o)
	return b, res, err
}

// A first sign-in registers: the dynamic client with the agent's name and
// this host's ID, a fresh S256 challenge, the resource and the six scopes,
// and the client ID the callback issues is the one saved.
func TestLogin_FirstRegistration(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	hostID, _ := a.HostID()
	b, res, err := signIn(t, a, fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := b.page(t)
	want := map[string]string{
		"response_type": "code", "client_id": DynamicClient, "agent_name_hint": AgentName,
		"ext_agent_host_id": hostID, "redirect_uri": "http://127.0.0.1:54321/auth/callback",
		"scope": Scopes, "resource": Resource, "code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("authorize %s = %q, want %q", k, q.Get(k), v)
		}
	}
	if len(strings.Fields(q.Get("scope"))) != 6 {
		t.Errorf("scope has %d entries", len(strings.Fields(q.Get("scope"))))
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if len(q.Get(k)) < 43 {
			t.Errorf("%s = %q is not a fresh 32-byte value", k, q.Get(k))
		}
	}
	for _, k := range []string{"id_token_hint", "login_hint", "prompt"} {
		if q.Has(k) {
			t.Errorf("a first sign-in sent %s", k)
		}
	}
	exchange := fake.tokenRequests[0]
	if exchange.Get("client_id") != issuedClient || exchange.Get("code") != "auth-code-xyz" ||
		exchange.Get("redirect_uri") != q.Get("redirect_uri") || exchange.Get("resource") != Resource {
		t.Errorf("exchange form = %v", exchange)
	}
	if challengeOf(exchange.Get("code_verifier")) != q.Get("code_challenge") {
		t.Error("the verifier does not match the challenge")
	}
	if !res.Registered || !res.FirstSignIn || !res.PlanGranted || res.Email != testEmail {
		t.Errorf("Result = %+v", res)
	}
	c, _ := a.Credential()
	if c.ClientID != issuedClient || c.Subject != testSubject || c.HostID != hostID || !c.SignedIn() || c.Issuer != Issuer {
		t.Errorf("saved %+v", c)
	}
	// A second attempt draws new single-use values.
	b2, _, err := signIn(t, a, fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b2.page(t).Get("state") == q.Get("state") || b2.page(t).Get("nonce") == q.Get("nonce") {
		t.Error("state or nonce was reused")
	}
}

func challengeOf(verifier string) string {
	return attempt{verifier: verifier}.challenge()
}

// A returning sign-in names its issued client, hints at the account, and
// does not register again.
func TestLogin_ReturningSendsHints(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	saved := seed(t, a, clock, time.Hour)
	b, res, err := signIn(t, a, fake, nil, func(o *LoginOptions) { o.Consent = true })
	if err != nil {
		t.Fatal(err)
	}
	q := b.page(t)
	if q.Get("client_id") != issuedClient || q.Has("agent_name_hint") ||
		q.Get("id_token_hint") != saved.IDToken || q.Get("login_hint") != testEmail || q.Get("prompt") != "consent" {
		t.Errorf("returning authorize = %v", q)
	}
	if res.Registered || res.FirstSignIn {
		t.Errorf("Result = %+v", res)
	}
	c, _ := a.Credential()
	if c.ClientID != issuedClient || c.RefreshToken == saved.RefreshToken {
		t.Errorf("the returning sign-in was not saved: %+v", c)
	}
}

// A no in the browser exchanges nothing and saves nothing.
func TestLogin_DeclinedDoesNotExchange(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	_, _, err := signIn(t, a, fake, func(q url.Values) url.Values {
		return url.Values{"state": q["state"], "error": {"access_denied"}}
	}, nil)
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined", err)
	}
	if len(fake.tokenRequests) != 0 {
		t.Error("a declined sign-in was exchanged")
	}
	if c, _ := a.Credential(); c.SignedIn() || c.ClientID != "" {
		t.Errorf("a declined sign-in saved %+v", c)
	}
}

// A sign-in without the plan scope is kept, signed in with plan use off.
func TestLogin_MissingScopeIsSignedInWithoutPlan(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	fake.scope = "openid profile email offline_access resource.invoke"
	a := openTest(t, t.TempDir(), client, clock, nil)
	_, res, err := signIn(t, a, fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := a.Credential()
	if res.PlanGranted || c.PlanGranted() || !c.SignedIn() {
		t.Errorf("Result %+v, record plan=%v signed in=%v", res, c.PlanGranted(), c.SignedIn())
	}
}

// Every refusal after the callback leaves the record as it was.
func TestLogin_RefusalsSaveNothing(t *testing.T) {
	cases := []struct {
		name      string
		returning bool
		prepare   func(f *fakeAuth)
		reply     func(url.Values) url.Values
		want      error
	}{
		{name: "registration incomplete", reply: func(q url.Values) url.Values { q.Del("client_id"); return q }, want: errRegistrationIncomplete},
		{name: "mismatched client ID", returning: true,
			reply: func(q url.Values) url.Values { q.Set("client_id", "oaiapp_someoneelse"); return q }, want: errClientMismatch},
		{name: "bad nonce", prepare: func(f *fakeAuth) { f.badNonce = true }, want: errBadIDToken},
		{name: "invalid_grant", prepare: func(f *fakeAuth) { f.exchangeCode = "invalid_grant" }, want: errCodeRefused},
		{name: "different subject", returning: true, prepare: func(f *fakeAuth) { f.sub = "someone-else" }, want: errDifferentAccount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			fake, client := newFakeAuth(t, clock)
			if tc.prepare != nil {
				tc.prepare(fake)
			}
			a := openTest(t, t.TempDir(), client, clock, nil)
			var before Credential
			if tc.returning {
				before = seed(t, a, clock, time.Hour)
			}
			_, _, err := signIn(t, a, fake, tc.reply, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			after, _ := a.Credential()
			if after.RefreshToken != before.RefreshToken || after.ClientID != before.ClientID || after.Subject != before.Subject {
				t.Errorf("a refused sign-in changed the record: %+v", after)
			}
		})
	}
}

// With no browser — asked for, or one that would not open — the address is
// printed on the terminal and the sign-in waits for it.
func TestLogin_NoBrowserPrintsTheURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(o *LoginOptions, b *browser)
	}{
		{"--no-browser", func(o *LoginOptions, b *browser) {
			o.NoBrowser = true
			o.Browser = func(string) error { t.Error("the opener ran under --no-browser"); return nil }
		}},
		{"the opener fails", func(o *LoginOptions, b *browser) {
			o.Browser = func(string) error { return errors.New("no display") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			fake, client := newFakeAuth(t, clock)
			a := openTest(t, t.TempDir(), client, clock, nil)
			b := newBrowser(t, fake, newPipeListener())
			o := b.loginOpts()
			tc.edit(&o, b)
			say := &urlWatcher{found: func(page string) { _ = b.open(page) }}
			o.Say = say
			if _, err := a.Login(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(say.text(), AuthorizeURL+"?") || !strings.Contains(say.text(), "127.0.0.1:54321") {
				t.Errorf("the terminal was told:\n%s", say.text())
			}
		})
	}
}

// urlWatcher is the person reading the terminal: when the address appears,
// they open it.
type urlWatcher struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	found func(string)
	seen  bool
}

func (w *urlWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf.Write(p)
	text := w.buf.String()
	seen := w.seen
	w.mu.Unlock()
	if i := strings.Index(text, AuthorizeURL+"?"); i >= 0 && !seen {
		w.mu.Lock()
		w.seen = true
		w.mu.Unlock()
		page := strings.Fields(text[i:])[0]
		w.found(page)
	}
	return len(p), nil
}

func (w *urlWatcher) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// The wait is bounded, and a cancelled sign-in stops waiting at once.
func TestLogin_WaitIsBounded(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	ln := newPipeListener()
	o := LoginOptions{
		Browser: func(string) error { return nil },
		Listen:  func() (net.Listener, error) { return ln, nil },
		Wait:    30 * time.Millisecond,
	}
	if _, err := a.Login(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not finished within") {
		t.Fatalf("err = %v, want the bounded wait", err)
	}
	select {
	case <-ln.closed:
	default:
		t.Error("the listener was left open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o.Listen = func() (net.Listener, error) { return newPipeListener(), nil }
	o.Wait = time.Hour
	if _, err := a.Login(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want cancelled", err)
	}
	_ = fake
}

// The callback handler answers only its own path on its own host, and only
// the first request.
func TestCallback_AnswersOnceOnItsOwnHost(t *testing.T) {
	h := newCallbackHandler("127.0.0.1:5000", "s")
	get := func(host, target string) int {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := get("evil.example:5000", "/auth/callback?state=s&code=c"); got != http.StatusNotFound {
		t.Errorf("another Host = %d", got)
	}
	if got := get("127.0.0.1:5000", "/favicon.ico"); got != http.StatusNotFound {
		t.Errorf("another path = %d", got)
	}
	// A wrong state is refused and does not settle the attempt.
	if got := get("127.0.0.1:5000", "/auth/callback?state=forged&code=c"); got != http.StatusBadRequest {
		t.Errorf("a wrong state = %d", got)
	}
	select {
	case cb := <-h.done:
		t.Fatalf("a wrong state settled the attempt: %+v", cb)
	default:
	}
	if got := get("127.0.0.1:5000", "/auth/callback?state=s&code=c&client_id=x"); got != http.StatusOK {
		t.Errorf("the callback = %d", got)
	}
	if cb := <-h.done; cb.err != nil || cb.code != "c" || cb.clientID != "x" {
		t.Errorf("callback = %+v", cb)
	}
	if got := get("127.0.0.1:5000", "/auth/callback?state=s&code=c"); got != http.StatusGone {
		t.Errorf("a second callback = %d", got)
	}
}

// Nothing the package logs is an address or any part of a token, a code or
// the attempt's single-use values, at the most talkative level, across a
// sign-in, a refresh, a refused refresh and a sign-out.
func TestLogin_NothingInTheLogIsAURLOrAToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shhh.log")
	lf, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	SetLogger(slog.New(slog.NewTextHandler(lf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { SetLogger(nil) })

	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	say := &urlWatcher{found: func(string) {}}
	b, _, err := signIn(t, a, fake, nil, func(o *LoginOptions) { o.Say = say })
	if err != nil {
		t.Fatal(err)
	}
	signedIn, _ := a.Credential()
	if _, err := a.Token(context.Background(), signedIn.AccessToken); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := a.Credential()
	// A browser that would not open, so the address is printed, and a
	// forged callback after it.
	forger := newBrowser(t, fake, newPipeListener())
	forger.reply = func(q url.Values) url.Values { q.Set("state", "x"); return q }
	o := forger.loginOpts()
	o.Browser = func(string) error { return errors.New("no display") }
	o.Say = &urlWatcher{found: func(page string) { _ = forger.open(page) }}
	o.Wait = 300 * time.Millisecond
	if _, err := a.Login(context.Background(), o); err == nil {
		t.Fatal("the forged sign-in succeeded")
	}
	fake.refreshCode = "invalid_grant"
	_, _ = a.Token(context.Background(), refreshed.AccessToken)
	seed(t, a, clock, time.Hour)
	if _, err := a.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "chatgpt sign-in ok") || !strings.Contains(log, "code=invalid_grant") {
		t.Fatalf("the log does not hold the events it should:\n%s", log)
	}
	q := b.page(t)
	forbidden := []string{"http://", "https://", "auth.openai.com", "auth-code-xyz",
		q.Get("state"), q.Get("nonce"), q.Get("code_challenge")}
	for _, c := range []Credential{signedIn, refreshed} {
		forbidden = append(forbidden, c.AccessToken, c.RefreshToken, c.IDToken)
	}
	for _, s := range forbidden {
		if s == "" {
			continue
		}
		probe := s
		if len(probe) > 12 && !strings.Contains(probe, "/") {
			probe = probe[len(probe)/2-6 : len(probe)/2+6]
		}
		if strings.Contains(log, probe) {
			t.Errorf("the log carries %q:\n%s", probe, log)
		}
	}
}
