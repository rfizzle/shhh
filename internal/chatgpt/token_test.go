package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestToken_FreshTokenIsNotRefreshed(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	c := seed(t, a, clock, time.Hour)
	got, err := a.Token(context.Background(), "")
	if err != nil || got != c.AccessToken {
		t.Fatalf("Token = %q, %v; want the saved token", got, err)
	}
	if n := fake.tokenCount("refresh_token"); n != 0 {
		t.Errorf("a fresh token was refreshed %d times", n)
	}
}

// Inside the margin the token is replaced, and the access token, refresh
// token, expiry and scopes are replaced together.
func TestToken_RefreshesInsideTheMargin(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	old := seed(t, a, clock, RefreshMargin-time.Second)
	fake.scope = "openid offline_access"
	got, err := a.Token(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got == old.AccessToken {
		t.Fatal("the token inside the margin was not replaced")
	}
	form := fake.tokenRequests[0]
	if form.Get("client_id") != issuedClient || form.Get("refresh_token") != old.RefreshToken || form.Get("resource") != Resource {
		t.Errorf("refresh form = %v", form)
	}
	c, _ := a.Credential()
	if c.AccessToken != got || c.RefreshToken == old.RefreshToken || !c.ExpiresAt.Equal(clock.Now().Add(time.Hour)) ||
		strings.Join(c.Scopes, " ") != "openid offline_access" || c.EarliestRefreshAt.IsZero() {
		t.Errorf("the record after a refresh = access %q refresh %q expires %v scopes %v earliest %v",
			c.AccessToken, c.RefreshToken, c.ExpiresAt, c.Scopes, c.EarliestRefreshAt)
	}
	if c.ClientID != issuedClient || c.Subject != testSubject {
		t.Error("the refresh lost the account")
	}
}

// A refused bearer is refreshed once; the token that comes back is not
// refused, so a second call answers it without another exchange.
func TestToken_RefusedTokenIsRefreshedOnce(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	old := seed(t, a, clock, time.Hour)
	fresh, err := a.Token(context.Background(), old.AccessToken)
	if err != nil || fresh == old.AccessToken {
		t.Fatalf("Token(refused) = %q, %v", fresh, err)
	}
	// A second request refused with the old bearer finds the new one.
	again, err := a.Token(context.Background(), old.AccessToken)
	if err != nil || again != fresh {
		t.Fatalf("Token(old refused) = %q, %v; want %q", again, err, fresh)
	}
	if n := fake.tokenCount("refresh_token"); n != 1 {
		t.Errorf("refresh count = %d, want 1", n)
	}
}

// earliest_refresh_at holds a token that is inside the margin, unless the
// server refused it.
func TestToken_HonoursEarliestRefreshAt(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	c := seed(t, a, clock, RefreshMargin-time.Minute)
	c.EarliestRefreshAt = clock.Now().Add(time.Minute)
	if err := a.write(c); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Token(context.Background(), ""); err != nil || got != c.AccessToken {
		t.Fatalf("Token before earliest_refresh_at = %q, %v", got, err)
	}
	if n := fake.tokenCount("refresh_token"); n != 0 {
		t.Fatalf("refreshed before earliest_refresh_at")
	}
	if got, err := a.Token(context.Background(), c.AccessToken); err != nil || got == c.AccessToken {
		t.Fatalf("a refused token before earliest_refresh_at = %q, %v", got, err)
	}
	if n := fake.tokenCount("refresh_token"); n != 1 {
		t.Fatalf("refresh count = %d, want 1", n)
	}
}

// Two Accounts on one directory — two processes, as far as the lock can
// tell — wanting a refresh at once make one exchange, and the second gets
// the first one's token.
func TestToken_TwoConcurrentCallersMakeOneExchange(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	fake.refreshDelay = 100 * time.Millisecond
	dir := t.TempDir()
	first := openTest(t, dir, client, clock, nil)
	second := openTest(t, dir, client, clock, nil)
	seed(t, first, clock, time.Minute)

	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i, a := range []*Account{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = a.Token(context.Background(), "")
		}()
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
	}
	if got[0] != got[1] {
		t.Errorf("the callers got different tokens: %q and %q", got[0], got[1])
	}
	if n := fake.tokenCount("refresh_token"); n != 1 {
		t.Errorf("refresh count = %d, want 1", n)
	}
}

// Each terminal code signs the person out: the tokens go, the account and
// registration stay, and the answer is ErrSignInRequired.
func TestToken_TerminalCodesClear(t *testing.T) {
	for _, code := range []string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired",
		"refresh_token_invalidated", "refresh_token_reused", "invalid_client"} {
		t.Run(code, func(t *testing.T) {
			clock := newClock()
			fake, client := newFakeAuth(t, clock)
			fake.refreshCode = code
			if code == "invalid_client" {
				fake.refreshStatus = http.StatusUnauthorized
			}
			a := openTest(t, t.TempDir(), client, clock, nil)
			seed(t, a, clock, time.Minute)
			_, err := a.Token(context.Background(), "")
			if !errors.Is(err, ErrSignInRequired) {
				t.Fatalf("Token = %v, want ErrSignInRequired", err)
			}
			c, _ := a.Credential()
			if c.SignedIn() || c.AccessToken != "" || c.IDToken != "" {
				t.Errorf("tokens kept after %s", code)
			}
			if c.ClientID != issuedClient || c.Subject != testSubject || c.Email != testEmail {
				t.Errorf("the account was lost after %s: %+v", code, c)
			}
			if _, err := a.Token(context.Background(), ""); !errors.Is(err, ErrNotSignedIn) {
				t.Errorf("Token after the clear = %v, want ErrNotSignedIn", err)
			}
		})
	}
}

// A refusal that is not terminal, a server error and a timeout keep the
// record: the network being down is not the person signing out.
func TestToken_NetworkFailureKeepsTheRecord(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *fakeAuth)
		ctx     func() (context.Context, context.CancelFunc)
	}{
		{"server error", func(f *fakeAuth) { f.refreshStatus = http.StatusBadGateway }, nil},
		{"rate limited", func(f *fakeAuth) { f.refreshStatus, f.refreshCode = http.StatusTooManyRequests, "rate_limited" }, nil},
		{"timeout", func(f *fakeAuth) { f.refreshDelay = 2 * time.Second }, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 50*time.Millisecond)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			fake, client := newFakeAuth(t, clock)
			tc.prepare(fake)
			a := openTest(t, t.TempDir(), client, clock, nil)
			old := seed(t, a, clock, time.Minute)
			ctx, cancel := context.Background(), context.CancelFunc(func() {})
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			_, err := a.Token(ctx, "")
			if err == nil || errors.Is(err, ErrSignInRequired) {
				t.Fatalf("Token = %v, want a kept-record failure", err)
			}
			c, _ := a.Credential()
			if c.RefreshToken != old.RefreshToken || c.AccessToken != old.AccessToken {
				t.Error("the record was changed by a failure that was not terminal")
			}
			for _, tok := range []string{old.AccessToken, old.RefreshToken, old.IDToken} {
				if strings.Contains(err.Error(), tok) {
					t.Errorf("the error carries a token: %v", err)
				}
			}
		})
	}
}

// OnTokens hears every set the account learns: the one read from disk and
// each refreshed one after it, once each.
func TestToken_OnTokensSeesEverySet(t *testing.T) {
	clock := newClock()
	_, client := newFakeAuth(t, clock)
	var mu sync.Mutex
	var heard []string
	on := func(access, refresh, id string) {
		mu.Lock()
		heard = append(heard, access, refresh, id)
		mu.Unlock()
	}
	a := openTest(t, t.TempDir(), client, clock, on)
	old := seed(t, a, clock, time.Hour)
	if _, err := a.Token(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Token(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	fresh, err := a.Token(context.Background(), old.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := a.Credential()
	want := []string{old.AccessToken, old.RefreshToken, old.IDToken, fresh, c.RefreshToken, c.IDToken}
	if strings.Join(heard, ",") != strings.Join(want, ",") {
		t.Errorf("OnTokens heard %v, want %v", heard, want)
	}
}
