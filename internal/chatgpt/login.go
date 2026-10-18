package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
)

// DefaultWait is how long a sign-in waits for the browser to come back.
// Five minutes is long enough to find a password manager and approve a
// prompt on a phone, and short enough that a forgotten terminal stops
// listening on its own.
const DefaultWait = 5 * time.Minute

// LoginOptions is how a sign-in reaches the person.
type LoginOptions struct {
	// Browser opens the authorize page; nil is the desktop's opener.
	Browser func(url string) error
	// NoBrowser skips the opener and prints the address instead.
	NoBrowser bool
	// Say is the person's terminal: the address when it has to be opened
	// by hand, and that the sign-in is waiting. Never a log.
	Say io.Writer
	// Listen opens the loopback listener; nil listens on 127.0.0.1 on a
	// port the system picks.
	Listen func() (net.Listener, error)
	// Wait bounds the wait for the browser; 0 is DefaultWait.
	Wait time.Duration
	// Consent asks the server to show the permission page again, for a
	// sign-in that came back without plan use.
	Consent bool
}

// Result is what a finished sign-in says.
type Result struct {
	Email string
	// PlanGranted is false for a sign-in that is kept without plan use.
	PlanGranted bool
	// FirstSignIn is the first account this host has signed in with.
	FirstSignIn bool
	// Registered is a sign-in that registered shhh on this host and was
	// issued a client ID.
	Registered bool
}

// Refusals a finished callback can still end in. Nothing is written for any
// of them.
var (
	errRegistrationIncomplete = errors.New("the ChatGPT sign-in came back without registering shhh on this host; run the sign-in again")
	errClientMismatch         = errors.New("the ChatGPT sign-in came back for a different registration than this host's; nothing was saved")
	errCodeRefused            = errors.New("the ChatGPT sign-in code was refused; run the sign-in again")
)

// attempt is one sign-in's single-use values. state ties the callback to
// this attempt, nonce ties the ID token to it, and the verifier proves to
// the token endpoint that whoever redeems the code is whoever asked for it.
type attempt struct {
	state, nonce, verifier string
	clientID               string
	returning              bool
	redirect               string
	hostID                 string
	idTokenHint, loginHint string
	consent                bool
}

func newAttempt(saved Credential, hostID, redirect string, consent bool) (attempt, error) {
	at := attempt{redirect: redirect, hostID: hostID, consent: consent, clientID: DynamicClient}
	for _, dst := range []*string{&at.state, &at.nonce, &at.verifier} {
		v, err := randomToken()
		if err != nil {
			return attempt{}, err
		}
		*dst = v
	}
	if saved.ClientID != "" {
		at.returning = true
		at.clientID = saved.ClientID
		at.idTokenHint = saved.IDToken
		at.loginHint = saved.Email
	}
	return at, nil
}

// randomToken is 32 random bytes, base64url without padding: 43 characters,
// which is also a valid PKCE verifier.
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// challenge is the verifier's S256 challenge.
func (at attempt) challenge() string {
	sum := sha256.Sum256([]byte(at.verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeURL is the page the browser opens. A first sign-in registers:
// the dynamic client, the agent's name and this host's ID. A returning one
// names the client it was issued and hints at the account, so the person is
// not asked to pick it again.
func (at attempt) authorizeURL() string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {at.clientID},
		"redirect_uri":          {at.redirect},
		"scope":                 {Scopes},
		"resource":              {Resource},
		"state":                 {at.state},
		"nonce":                 {at.nonce},
		"code_challenge":        {at.challenge()},
		"code_challenge_method": {"S256"},
		"ext_agent_host_id":     {at.hostID},
	}
	if !at.returning {
		q.Set("agent_name_hint", AgentName)
	}
	if at.idTokenHint != "" {
		q.Set("id_token_hint", at.idTokenHint)
	}
	if at.loginHint != "" {
		q.Set("login_hint", at.loginHint)
	}
	if at.consent {
		q.Set("prompt", "consent")
	}
	return AuthorizeURL + "?" + q.Encode()
}

// Login signs in through the browser and saves the record.
//
// The listener is open before the browser is, so the callback cannot arrive
// at a port nobody holds; it listens on 127.0.0.1 only, which is the one
// redirect host the server accepts and the one no other machine can reach.
// The record is written only after every check has passed — the callback's
// state, the registration, the code exchange, the ID token, and that a
// returning sign-in is the same account — so a sign-in that fails part-way
// leaves the previous one as it was.
// See docs/capabilities/chatgpt-plan.md#what-the-sign-in-checks.
func (a *Account) Login(ctx context.Context, o LoginOptions) (Result, error) {
	hostID, err := a.HostID()
	if err != nil {
		return Result{}, err
	}
	saved, err := a.Credential()
	if err != nil {
		return Result{}, err
	}
	disc, err := a.discover(ctx)
	if err != nil {
		return Result{}, err
	}
	listen := o.Listen
	if listen == nil {
		listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}
	ln, err := listen()
	if err != nil {
		return Result{}, fmt.Errorf("could not listen for the sign-in on 127.0.0.1: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return Result{}, errors.New("the sign-in listener is not on 127.0.0.1")
	}
	host := "127.0.0.1:" + strconv.Itoa(addr.Port)
	at, err := newAttempt(saved, hostID, "http://"+host+CallbackPath, o.Consent)
	if err != nil {
		return Result{}, err
	}

	h := newCallbackHandler(host, at.state)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	cb, err := a.awaitCallback(ctx, o, at, host, h)
	if err != nil {
		logs.Logger().Info("chatgpt sign-in ended", "reason", reasonOf(err))
		return Result{}, err
	}
	res, err := a.finish(ctx, at, cb, saved, disc)
	if err != nil {
		logs.Logger().Info("chatgpt sign-in ended", "reason", reasonOf(err))
		return Result{}, err
	}
	logs.Logger().Info("chatgpt sign-in ok", "registered", res.Registered, "plan", res.PlanGranted)
	return res, nil
}

// awaitCallback opens the page, or says where it is, and waits for the
// browser to come back.
func (a *Account) awaitCallback(ctx context.Context, o LoginOptions, at attempt, host string, h *callbackHandler) (callback, error) {
	say := o.Say
	if say == nil {
		say = io.Discard
	}
	wait := o.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	page := at.authorizeURL()
	opened := false
	if !o.NoBrowser {
		browser := o.Browser
		if browser == nil {
			browser = openBrowser
		}
		if err := browser(page); err == nil {
			opened = true
		} else {
			logs.Logger().Info("chatgpt sign-in browser did not open")
		}
	}
	if opened {
		fmt.Fprintf(say, "Opened your browser to sign in to ChatGPT. Waiting on %s for up to %s; Ctrl+C stops.\n", host, wait)
	} else {
		// The address goes to the person's terminal and nowhere else: a
		// returning sign-in's carries the previous ID token as a hint.
		fmt.Fprintf(say, "Open this address in a browser on a machine whose browser can reach this host's %s:\n\n  %s\n\nWaiting for up to %s; Ctrl+C stops.\n", host, page, wait)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case cb := <-h.done:
		return cb, cb.err
	case <-timer.C:
		return callback{}, fmt.Errorf("the sign-in was not finished within %s; nothing was saved", wait)
	case <-ctx.Done():
		return callback{}, ctx.Err()
	}
}

// finish redeems the code and saves the record, after every check.
func (a *Account) finish(ctx context.Context, at attempt, cb callback, saved Credential, disc discovery) (Result, error) {
	clientID := at.clientID
	switch {
	case !at.returning && cb.clientID == "":
		return Result{}, errRegistrationIncomplete
	case !at.returning:
		clientID = cb.clientID
	case cb.clientID != "" && cb.clientID != at.clientID:
		return Result{}, errClientMismatch
	}
	tr, err := a.postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {cb.code},
		"code_verifier": {at.verifier},
		"redirect_uri":  {at.redirect},
		"resource":      {Resource},
	})
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.code == "invalid_grant" {
			return Result{}, errCodeRefused
		}
		return Result{}, err
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" || tr.IDToken == "" {
		return Result{}, errors.New("the ChatGPT sign-in answered without the tokens it should have; nothing was saved")
	}
	keys, err := a.fetchKeys(ctx, disc.JWKSURI)
	if err != nil {
		return Result{}, err
	}
	now := a.now()
	claims, err := verifyIDToken(tr.IDToken, keys, clientID, at.nonce, now)
	if err != nil {
		return Result{}, err
	}
	scopes := tr.scopes()
	if scopes == nil {
		scopes = strings.Fields(Scopes)
	}
	cred := Credential{
		Email: claims.Email, Issuer: Issuer, Subject: claims.Sub, ClientID: clientID, HostID: at.hostID,
		IDToken: tr.IDToken, AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, TokenType: tr.TokenType,
		ExpiresAt: tr.expiresAt(now), EarliestRefreshAt: tr.EarliestRefreshAt.Time, Scopes: scopes, SavedAt: now,
	}
	if err := a.save(ctx, cred); err != nil {
		return Result{}, err
	}
	return Result{
		Email: cred.Email, PlanGranted: cred.PlanGranted(),
		FirstSignIn: saved.Subject == "", Registered: !at.returning,
	}, nil
}

// errDifferentAccount is a returning sign-in that came back as someone
// else. The record is the saved account's, and replacing it is a decision
// the person makes by removing it, not one a browser tab makes for them.
var errDifferentAccount = errors.New("the browser signed in to a different ChatGPT account than the one saved on this host")

func (a *Account) differentAccount() error {
	return fmt.Errorf("%w; nothing was replaced — to change accounts, delete %s and sign in again",
		errDifferentAccount, a.CredentialsPath())
}

// save writes a new sign-in under both locks, checking the account against
// the record as it is now — another process may have signed in meanwhile —
// and keeping this host's one-time panel mark.
func (a *Account) save(ctx context.Context, cred Credential) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withLock(ctx, func() error {
		cur, err := a.current()
		if err != nil {
			return err
		}
		if cur.Subject != "" && cur.Subject != cred.Subject {
			return a.differentAccount()
		}
		cred.WelcomedAt = cur.WelcomedAt
		if err := a.write(cred); err != nil {
			return err
		}
		a.report(cred)
		return nil
	})
}

// reasonOf is why a sign-in ended, in a word for the log.
func reasonOf(err error) string {
	switch {
	case errors.Is(err, ErrDeclined):
		return "declined"
	case errors.Is(err, errRegistrationIncomplete):
		return "registration-incomplete"
	case errors.Is(err, errClientMismatch):
		return "client-mismatch"
	case errors.Is(err, errCodeRefused):
		return "invalid_grant"
	case errors.Is(err, errBadIDToken):
		return "bad-id-token"
	case errors.Is(err, errDifferentAccount):
		return "different-account"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	var te *tokenError
	if errors.As(err, &te) {
		return failureKind(err)
	}
	return "error"
}
