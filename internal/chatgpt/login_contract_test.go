//go:build contract

package chatgpt

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The real loopback listener: the sign-in listens on 127.0.0.1 on a port
// the system picks, and a browser's GET to the printed redirect reaches it.
// The authorization server is still the in-memory fake; this proves the
// socket, not OpenAI.
func TestLogin_RealLoopbackListener(t *testing.T) {
	clock := newClock()
	fake, client := newFakeAuth(t, clock)
	a := openTest(t, t.TempDir(), client, clock, nil)
	got := make(chan int, 1)
	o := LoginOptions{
		Wait: 10 * time.Second,
		Browser: func(page string) error {
			u, err := url.Parse(page)
			if err != nil {
				return err
			}
			q := u.Query()
			fake.mu.Lock()
			fake.nonce = q.Get("nonce")
			fake.mu.Unlock()
			back := url.Values{"state": {q.Get("state")}, "code": {"auth-code-xyz"}, "client_id": {issuedClient}}
			go func() {
				resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(q.Get("redirect_uri") + "?" + back.Encode())
				if err != nil {
					got <- 0
					return
				}
				resp.Body.Close()
				got <- resp.StatusCode
			}()
			return nil
		},
	}
	res, err := a.Login(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if status := <-got; status != http.StatusOK {
		t.Errorf("the callback answered %d", status)
	}
	if !res.Registered || !res.PlanGranted {
		t.Errorf("Result = %+v", res)
	}
}
