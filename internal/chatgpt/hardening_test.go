package chatgpt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/testhttp"
)

// A forged callback is answered 400 and the sign-in keeps waiting for the
// genuine one, which then completes it.
func TestLogin_AWrongStateKeepsWaiting(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	b := newBrowser(t, fake, newPipeListener())
	o := b.loginOpts()
	forged := make(chan int, 1)
	o.Browser = func(page string) error {
		u, _ := url.Parse(page)
		q := u.Query()
		resp, err := b.ln.browserClient().Get(q.Get("redirect_uri") + "?state=forged&code=stolen")
		if err != nil {
			return err
		}
		resp.Body.Close()
		forged <- resp.StatusCode
		return b.open(page)
	}
	if _, err := a.Login(context.Background(), o); err != nil {
		t.Fatalf("the genuine callback after a forged one: %v", err)
	}
	if got := <-forged; got != http.StatusBadRequest {
		t.Errorf("the forged callback = %d, want 400", got)
	}
	if n := fake.tokenCount("authorization_code"); n != 1 {
		t.Errorf("exchanges = %d, want only the genuine code's", n)
	}
	if fake.tokenRequests[0].Get("code") == "stolen" {
		t.Error("the forged code was exchanged")
	}
}

// A redirect from the token endpoint is never followed: the refresh form
// would go to the redirect's target with the refresh token in it. The
// caller's client is left as it was handed in.
func TestToken_NeverFollowsARedirect(t *testing.T) {
	clock := newClock()
	var stolen atomic.Int32
	reg := &testhttp.Registry{}
	auth := reg.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://elsewhere.example/steal")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	auth.Alias("auth.openai.com")
	elsewhere := reg.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stolen.Add(1)
	}))
	elsewhere.Alias("elsewhere.example")
	t.Cleanup(auth.Close)
	t.Cleanup(elsewhere.Close)
	client := reg.Client()
	a := openTest(t, t.TempDir(), client, clock, nil)
	if client.CheckRedirect != nil {
		t.Error("Open changed the caller's client")
	}
	old := seed(t, a, clock, time.Minute)
	if _, err := a.Token(context.Background(), ""); err == nil {
		t.Fatal("a redirected refresh succeeded")
	}
	if stolen.Load() != 0 {
		t.Error("the refresh form followed the redirect")
	}
	if c, _ := a.Credential(); c.RefreshToken != old.RefreshToken {
		t.Error("a redirect cleared the record")
	}
}

// invalid_client means the registration is gone, so the client ID goes
// with the tokens and the next sign-in registers again.
func TestToken_InvalidClientClearsTheRegistration(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	fake.refreshCode, fake.refreshStatus = "invalid_client", http.StatusUnauthorized
	a := openTest(t, t.TempDir(), client, clock, nil)
	seed(t, a, clock, time.Minute)
	if _, err := a.Token(context.Background(), ""); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("Token = %v", err)
	}
	c, _ := a.Credential()
	if c.ClientID != "" || c.Subject != testSubject {
		t.Errorf("after invalid_client: client=%q subject=%q", c.ClientID, c.Subject)
	}
	fake.refreshCode, fake.refreshStatus = "", 0
	b, _, err := signIn(t, a, fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q := b.page(t); q.Get("client_id") != DynamicClient || q.Get("agent_name_hint") != AgentName {
		t.Errorf("the sign-in after invalid_client did not register: %v", q)
	}
}

// A refresh the disk will not take keeps the rotated set: the request
// still gets its token, and the next call writes the record, spending no
// second refresh.
func TestToken_AFailedWriteKeepsTheRotatedToken(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write")
	}
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	dir := t.TempDir()
	a := openTest(t, dir, client, clock, nil)
	old := seed(t, a, clock, time.Hour)
	if _, err := a.HostID(); err != nil { // makes the lock file while the directory takes it
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	fresh, err := a.Token(context.Background(), old.AccessToken)
	if err != nil || fresh == old.AccessToken {
		t.Fatalf("Token with an unwritable record = %q, %v", fresh, err)
	}
	if c, _ := a.Credential(); c.RefreshToken != old.RefreshToken {
		t.Fatal("the record changed on a disk that refused the write")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	again, err := a.Token(context.Background(), "")
	if err != nil || again != fresh {
		t.Fatalf("the next call = %q, %v; want the kept token %q", again, err, fresh)
	}
	c, _ := a.Credential()
	if c.AccessToken != fresh || c.RefreshToken == old.RefreshToken {
		t.Errorf("the kept set was not written: %+v", c)
	}
	if n := fake.tokenCount("refresh_token"); n != 1 {
		t.Errorf("refresh count = %d, want 1", n)
	}
}

// A lock another process will not let go of is a timeout, which callers
// treat as a passing failure, not a signed-out account.
func TestToken_ALockTimeoutIsADeadline(t *testing.T) {
	clock := newClock()
	_, client := newFakeAuth(t, clock)
	dir := t.TempDir()
	holder := openTest(t, dir, client, clock, nil)
	waiter := openTest(t, dir, client, clock, nil)
	waiter.lockWait = 20 * time.Millisecond
	seed(t, holder, clock, time.Minute)
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = holder.withLock(context.Background(), func() error { close(held); <-release; return nil })
	}()
	<-held
	_, err := waiter.Token(context.Background(), "")
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSignInRequired) {
		t.Errorf("Token behind a held lock = %v, want a deadline", err)
	}
}

// azp is checked whenever it is present, one audience or several.
func TestVerifyIDToken_ChecksAzpWheneverPresent(t *testing.T) {
	now := newClock().Now()
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keys := parseKeys(t, ecJWK(&ec.PublicKey, "ec1"))
	claims := goodClaims(now)
	claims["azp"] = "oaiapp_other"
	if _, err := verifyIDToken(mintES256(t, ec, "ec1", claims), keys, issuedClient, "the-nonce", now); !errors.Is(err, errBadIDToken) {
		t.Errorf("a foreign azp with one audience = %v", err)
	}
	claims["azp"] = issuedClient
	if _, err := verifyIDToken(mintES256(t, ec, "ec1", claims), keys, issuedClient, "the-nonce", now); err != nil {
		t.Errorf("a matching azp = %v", err)
	}
}
