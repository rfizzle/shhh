package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/logs"
)

// terminalCodes are the refresh refusals no retry can turn around: the
// refresh token is spent, expired, replayed or revoked, or the client it was
// issued to is gone. Each signs the person out; every other failure keeps the
// record, because a network that is down for a minute must not cost a
// sign-in.
var terminalCodes = map[string]bool{
	"invalid_grant":             true,
	"invalid_refresh_token":     true,
	"token_expired":             true,
	"refresh_token_expired":     true,
	"refresh_token_invalidated": true,
	"refresh_token_reused":      true,
	"invalid_client":            true,
}

// defaultTokenLife is what an access token is taken to last when the
// response does not say: the documented hour.
const defaultTokenLife = time.Hour

// Token answers a bearer for the next request. refused is the bearer the
// last request was refused with, or empty; a source still holding it
// refreshes before it answers, once, and answers whatever the server gave.
//
// A token inside the refresh margin is replaced, but never before the
// server's earliest refresh time unless it was refused: refreshing early
// spends a refresh token for nothing and rotates every other process's copy
// out from under it. The refresh runs holding the account's lock and then
// the file lock, and the record is read again once both are held, so the
// process that waited finds the token the first one fetched instead of
// spending the refresh token a second time.
// See docs/capabilities/chatgpt-plan.md#one-refresh-at-a-time.
func (a *Account) Token(ctx context.Context, refused string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cred, err := a.read()
	if err != nil {
		return "", err
	}
	if !cred.SignedIn() {
		return "", ErrNotSignedIn
	}
	a.report(cred)
	if a.usable(cred, refused) {
		return cred.AccessToken, nil
	}
	var token string
	err = a.withLock(ctx, func() error {
		cred, err := a.read()
		if err != nil {
			return err
		}
		if !cred.SignedIn() {
			return ErrNotSignedIn
		}
		a.report(cred)
		if a.usable(cred, refused) {
			token = cred.AccessToken
			return nil
		}
		next, err := a.refresh(ctx, cred)
		if err != nil {
			var te *tokenError
			if errors.As(err, &te) && terminalCodes[te.code] {
				logs.Logger().Warn("chatgpt refresh refused", "code", te.code)
				if werr := a.write(cred.cleared(a.now())); werr != nil {
					return werr
				}
				return ErrSignInRequired
			}
			logs.Logger().Warn("chatgpt refresh failed", "kind", failureKind(err))
			return err
		}
		if err := a.write(next); err != nil {
			return fmt.Errorf("the refreshed ChatGPT sign-in could not be saved: %w", err)
		}
		logs.Logger().Info("chatgpt refresh ok")
		a.report(next)
		token = next.AccessToken
		return nil
	})
	return token, err
}

// usable reports whether the record's access token can be sent as it is.
func (a *Account) usable(c Credential, refused string) bool {
	if c.AccessToken == "" || c.AccessToken == refused || c.ExpiresAt.IsZero() {
		return false
	}
	now := a.now()
	if now.Before(c.ExpiresAt.Add(-RefreshMargin)) {
		return true
	}
	return now.Before(c.EarliestRefreshAt) && now.Before(c.ExpiresAt)
}

// refresh spends the record's refresh token and answers the record with the
// new set in it. The access token, refresh token, expiry and scopes are
// replaced together: a record holding a new access token beside the spent
// refresh token signs the person out at the next refresh.
func (a *Account) refresh(ctx context.Context, c Credential) (Credential, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"refresh_token": {c.RefreshToken},
		"resource":      {Resource},
	}
	tr, err := a.postToken(ctx, form)
	if err != nil {
		return Credential{}, err
	}
	if tr.AccessToken == "" {
		return Credential{}, errors.New("the ChatGPT token endpoint answered without an access token")
	}
	now := a.now()
	c.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		c.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		c.IDToken = tr.IDToken
	}
	if tr.TokenType != "" {
		c.TokenType = tr.TokenType
	}
	c.ExpiresAt = tr.expiresAt(now)
	c.EarliestRefreshAt = tr.EarliestRefreshAt.Time
	if scopes := tr.scopes(); scopes != nil {
		c.Scopes = scopes
	}
	c.SavedAt = now
	return c, nil
}

// tokenResponse is the token endpoint's answer to an exchange or a refresh.
type tokenResponse struct {
	AccessToken       string   `json:"access_token"`
	RefreshToken      string   `json:"refresh_token"`
	IDToken           string   `json:"id_token"`
	TokenType         string   `json:"token_type"`
	ExpiresIn         flexInt  `json:"expires_in"`
	EarliestRefreshAt flexTime `json:"earliest_refresh_at"`
	Scope             string   `json:"scope"`
}

func (t tokenResponse) expiresAt(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return now.Add(defaultTokenLife)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// scopes are the granted scopes, or nil when the response did not list
// them. An absent scope means the request's scopes were granted unchanged
// (RFC 6749 §5.1), which the caller fills in.
func (t tokenResponse) scopes() []string {
	if strings.TrimSpace(t.Scope) == "" {
		return nil
	}
	return strings.Fields(t.Scope)
}

// flexInt reads a number written as a number or as a string of one.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number")
	}
	*n = flexInt(v)
	return nil
}

// flexTime reads a time written as Unix seconds or as RFC 3339. The docs
// name earliest_refresh_at without fixing its encoding, so both are taken.
type flexTime struct{ time.Time }

func (t *flexTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == `""` {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		inner := strings.Trim(s, `"`)
		if parsed, err := time.Parse(time.RFC3339, inner); err == nil {
			t.Time = parsed
			return nil
		}
		s = inner
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a time")
	}
	t.Time = time.Unix(int64(v), 0)
	return nil
}

// tokenError is a refusal from the authorization server, carrying only the
// status and the server's error code — never the body, which can echo what
// was sent.
type tokenError struct {
	status int
	code   string
}

func (e *tokenError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("the ChatGPT sign-in server refused the request (HTTP %d)", e.status)
	}
	return fmt.Sprintf("the ChatGPT sign-in server refused the request (HTTP %d, %s)", e.status, e.code)
}

// postToken posts a form to the token endpoint and decodes the answer.
func (a *Account) postToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	status, body, err := a.post(ctx, TokenURL, form)
	if err != nil {
		return tokenResponse{}, err
	}
	if status != http.StatusOK {
		return tokenResponse{}, &tokenError{status: status, code: errorCode(body)}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("the ChatGPT token endpoint answered with something that is not a token response: %w", err)
	}
	return tr, nil
}

// maxBody bounds what is read from the authorization server.
const maxBody = 1 << 20

// post sends a form and answers the status and the body.
func (a *Account) post(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return a.do(req)
}

// get fetches a JSON document.
func (a *Account) get(ctx context.Context, endpoint string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	return a.do(req)
}

func (a *Account) do(req *http.Request) (int, []byte, error) {
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, scrubURLError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, bytes.TrimSpace(body), nil
}

// scrubURLError drops the request URL from a transport error while keeping
// what it wraps, so a timeout is still a timeout to errors.As. The URLs this
// package sends are fixed, but the message is printed and logged, and a URL
// is the kind of thing this package does not print.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return &url.Error{Op: ue.Op, URL: "(the ChatGPT sign-in server)", Err: ue.Err}
	}
	return err
}

// errorCode reads the server's error code out of a refusal: OAuth's
// {"error": "invalid_grant"} or the object form {"error": {"code": …}}. It
// is reduced to the characters a code is made of, so a body that is not a
// refusal cannot put anything else into a message or a log.
func errorCode(body []byte) string {
	var flat struct {
		Error json.RawMessage `json:"error"`
		Code  string          `json:"code"`
	}
	if json.Unmarshal(body, &flat) != nil {
		return ""
	}
	var code string
	if json.Unmarshal(flat.Error, &code) != nil {
		var nested struct {
			Code string `json:"code"`
			Type string `json:"type"`
		}
		if json.Unmarshal(flat.Error, &nested) == nil {
			code = nested.Code
			if code == "" {
				code = nested.Type
			}
		}
	}
	if code == "" {
		code = flat.Code
	}
	return cleanCode(code)
}

// cleanCode keeps a code to letters, digits, '_', '.' and '-' and to 64 of
// them.
func cleanCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if b.Len() >= 64 {
			break
		}
		if r == '_' || r == '.' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// failureKind is a refresh failure in one word for the log.
func failureKind(err error) string {
	var te *tokenError
	switch {
	case errors.As(err, &te):
		if te.code != "" {
			return te.code
		}
		return "http-" + strconv.Itoa(te.status)
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return "timeout"
	}
	return "network"
}
