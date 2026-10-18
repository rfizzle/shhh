package chatgpt

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"
)

// callback is what the browser brought back to the loopback listener.
type callback struct {
	code     string
	clientID string
	err      error
}

// Reasons a callback is refused. None of them carries the query: it holds
// the authorization code.
var (
	errBadState = errors.New("the browser came back with a sign-in shhh did not start; nothing was saved")
	errNoCode   = errors.New("the browser came back without a sign-in code; nothing was saved")
)

// callbackHandler answers the one request the browser makes when the
// sign-in is done. It is a plain handler so a test can drive it without a
// listener. The first request to the callback path settles the attempt,
// whatever it carried; anything after it is told the sign-in is over.
type callbackHandler struct {
	// host is the Host the browser must have used: 127.0.0.1 and the
	// listener's port. A page elsewhere that points a request at the port
	// under another name — DNS rebinding — is refused before the query is
	// read.
	host  string
	state string
	once  sync.Once
	done  chan callback
}

func newCallbackHandler(host, state string) *callbackHandler {
	return &callbackHandler{host: host, state: state, done: make(chan callback, 1)}
}

func (h *callbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != CallbackPath || r.Host != h.host {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	settled := false
	h.once.Do(func() {
		settled = true
		cb := h.read(r)
		h.done <- cb
		switch {
		case cb.err == nil:
			_, _ = w.Write([]byte("shhh has the sign-in. You can close this tab; the terminal says how it went.\n"))
		case errors.Is(cb.err, ErrDeclined):
			_, _ = w.Write([]byte("The sign-in was declined, and shhh saved nothing. You can close this tab.\n"))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("shhh could not use this sign-in; the terminal says why. You can close this tab.\n"))
		}
	})
	if !settled {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte("This sign-in is already finished. You can close this tab.\n"))
	}
}

// read checks the callback in the order the contract asks: state first,
// because a forged request has to be refused before anything it says is
// believed, then a refusal from the server, then the code.
func (h *callbackHandler) read(r *http.Request) callback {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(h.state)) != 1 {
		return callback{err: errBadState}
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return callback{err: ErrDeclined}
		}
		return callback{err: &tokenError{status: http.StatusBadRequest, code: cleanCode(e)}}
	}
	code := q.Get("code")
	if code == "" {
		return callback{err: errNoCode}
	}
	return callback{code: code, clientID: q.Get("client_id")}
}
