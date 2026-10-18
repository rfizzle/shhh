package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// A sign-out sends the refresh token to the discovered revocation endpoint
// and clears the tokens, keeping the registration and the host.
func TestLogout_RevokesThenClears(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	hostID, _ := a.HostID()
	saved := seed(t, a, clock, time.Hour)
	revoked, err := a.Logout(context.Background())
	if err != nil || !revoked {
		t.Fatalf("Logout = %v, %v", revoked, err)
	}
	if len(fake.revokeRequests) != 1 {
		t.Fatalf("revocation requests = %d", len(fake.revokeRequests))
	}
	form := fake.revokeRequests[0]
	if form.Get("token") != saved.RefreshToken || form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != issuedClient {
		t.Errorf("revocation form = %v", form)
	}
	c, _ := a.Credential()
	if c.SignedIn() || c.AccessToken != "" || c.ClientID != issuedClient {
		t.Errorf("after Logout: %+v", c)
	}
	if id, _ := a.HostID(); id != hostID {
		t.Error("Logout changed the host ID")
	}
	if _, err := a.Logout(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("a second Logout = %v, want ErrNotSignedIn", err)
	}
}

// The server answers an already-invalid token with an empty 200, and that
// is a confirmed sign-out like any other.
func TestLogout_AlreadyInvalidIs200(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	fake.revokeStatus = []int{http.StatusOK}
	a := openTest(t, t.TempDir(), client, clock, nil)
	seed(t, a, clock, -time.Hour)
	if revoked, err := a.Logout(context.Background()); err != nil || !revoked {
		t.Fatalf("Logout = %v, %v", revoked, err)
	}
}

// A server that keeps failing is tried four times with backoff between,
// and then the tokens are cleared anyway and the sign-out says it was not
// confirmed. A refusal that is not a server error is not retried.
func TestLogout_UnconfirmedRevocationSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		tries    int
		waits    []time.Duration
	}{
		{"server errors", []int{503, 502, 500, 503}, 4, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
		{"recovers", []int{503, 200}, 2, []time.Duration{time.Second}},
		{"refused", []int{400}, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newClock()
			fake, client := newFakeAuth(t, clock)
			fake.revokeStatus = tc.statuses
			a := openTest(t, t.TempDir(), client, clock, nil)
			var waits []time.Duration
			a.sleep = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }
			seed(t, a, clock, time.Hour)
			revoked, err := a.Logout(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantRevoked := tc.statuses[len(tc.statuses)-1] == 200
			if revoked != wantRevoked {
				t.Errorf("revoked = %v, want %v", revoked, wantRevoked)
			}
			if len(fake.revokeRequests) != tc.tries {
				t.Errorf("tries = %d, want %d", len(fake.revokeRequests), tc.tries)
			}
			if len(waits) != len(tc.waits) {
				t.Errorf("waits = %v, want %v", waits, tc.waits)
			}
			for i := range min(len(waits), len(tc.waits)) {
				if waits[i] != tc.waits[i] {
					t.Errorf("wait %d = %v, want %v", i, waits[i], tc.waits[i])
				}
			}
			if c, _ := a.Credential(); c.SignedIn() {
				t.Error("the tokens were kept after an unconfirmed revocation")
			}
		})
	}
}
