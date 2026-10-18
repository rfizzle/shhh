package chatgpt

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// revokeAttempts and revokeBackoff are how hard a sign-out tries to tell the
// server: the first try and three more, a second, two and four apart. A
// server down for longer than that is a sign-out that is local only, which
// the person is told so they can disconnect the app themselves.
const (
	revokeAttempts = 4
	revokeBackoff  = time.Second
)

// revokeTimeout bounds one revocation request; the sign-out holds the
// refresh lock while it runs.
const revokeTimeout = 10 * time.Second

// Logout revokes the refresh token at the server and clears the tokens
// either way, keeping the client ID and the host ID. revoked is false when
// the server did not confirm, which leaves the app connected on ChatGPT's
// side until the person disconnects it there.
//
// It holds the refresh lock throughout, so a refresh in another process
// cannot rotate a new token into the record between the revocation and the
// clear.
// See docs/capabilities/chatgpt-plan.md#signing-out.
func (a *Account) Logout(ctx context.Context) (revoked bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	err = a.withLock(ctx, func() error {
		cred, err := a.current()
		if err != nil {
			return err
		}
		if !cred.SignedIn() {
			return ErrNotSignedIn
		}
		revoked = a.revoke(ctx, cred)
		return a.write(cred.cleared(a.now()))
	})
	log().Info("chatgpt sign-out", "revoked", revoked)
	return revoked, err
}

// revoke tells the server the refresh token is finished. An empty 200 is
// success, an already-invalid token included (RFC 7009 §2.2); a network
// failure or a 5xx is tried again; any other refusal is final.
func (a *Account) revoke(ctx context.Context, cred Credential) bool {
	form := url.Values{
		"token":           {cred.RefreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {cred.ClientID},
	}
	endpoint := ""
	wait := revokeBackoff
	for try := 0; try < revokeAttempts; try++ {
		if try > 0 {
			if a.sleep(ctx, wait) != nil {
				return false
			}
			wait *= 2
		}
		done, retry := a.revokeOnce(ctx, &endpoint, form)
		if done {
			return true
		}
		if !retry {
			return false
		}
	}
	return false
}

// revokeOnce is one try: the endpoint is discovered on the first that gets
// that far, and the answer says whether it worked and whether another try
// could.
func (a *Account) revokeOnce(ctx context.Context, endpoint *string, form url.Values) (done, retry bool) {
	ctx, cancel := context.WithTimeout(ctx, revokeTimeout)
	defer cancel()
	if *endpoint == "" {
		d, err := a.discover(ctx)
		if err != nil {
			return false, transient(err)
		}
		if d.RevocationEndpoint == "" {
			return false, false
		}
		*endpoint = d.RevocationEndpoint
	}
	status, _, err := a.post(ctx, *endpoint, form)
	switch {
	case err != nil:
		return false, transient(err)
	case status == http.StatusOK:
		return true, false
	case status >= 500:
		return false, true
	}
	log().Warn("chatgpt revocation refused", "status", status)
	return false, false
}

// transient is a failure another try could get past: the network, a timeout,
// or a server error answering the configuration.
func transient(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || isServerStatus(err)
}

// isServerStatus is a configuration fetch that answered 5xx.
func isServerStatus(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status >= 500
}
