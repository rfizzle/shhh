package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/llm/chatgpt"
	"github.com/rfizzle/shhh/internal/testhttp"
)

// pipeListener is a loopback listener with no socket: a connection is handed
// to Accept by whoever dials it. It reports 127.0.0.1, which the sign-in
// insists on.
type pipeListener struct {
	conns chan net.Conn
	once  sync.Once
	done  chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error { l.once.Do(func() { close(l.done) }); return nil }

func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
}

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// What a person reads of the ChatGPT sign-in is written in the app's name
// that shhh hands over, so it reads "shhh" exactly as it did when the sign-in
// lived in this repository: the page the browser lands on, and the sentence
// for a sign-in that came back without registering the host.
func TestChatGPTSignIn_SpeaksInShhhsName(t *testing.T) {
	auth := &testhttp.Registry{}
	srv := auth.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": chatgpt.Issuer, "jwks_uri": chatgpt.Issuer + "/jwks",
		})
	}))
	srv.Alias("auth.openai.com")
	t.Cleanup(srv.Close)

	a, err := chatgpt.Open(chatgpt.Options{App: shhhApp, Store: chatgpt.FileStore{Dir: filepath.Join(t.TempDir(), "chatgpt")}, Client: auth.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ln := newPipeListener()
	browser := &http.Client{Transport: &http.Transport{DialContext: ln.dial}, Timeout: 10 * time.Second}

	var page string
	_, err = a.Login(context.Background(), chatgpt.LoginOptions{
		Say:    io.Discard,
		Listen: func() (net.Listener, error) { return ln, nil },
		Wait:   10 * time.Second,
		Browser: func(authorize string) error {
			u, err := url.Parse(authorize)
			if err != nil {
				return err
			}
			if got := u.Query().Get("agent_name_hint"); got != "shhh" {
				t.Errorf("the host was registered as %q, want shhh", got)
			}
			// The browser comes back with the right state and a code, and
			// without the client ID a first sign-in registers.
			back := u.Query().Get("redirect_uri") + "?" + url.Values{
				"state": {u.Query().Get("state")}, "code": {"c"},
			}.Encode()
			resp, err := browser.Get(back)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			page = string(body)
			return err
		},
	})
	if want := "shhh has the sign-in. You can close this tab; the terminal says how it went.\n"; page != want {
		t.Errorf("callback page = %q, want %q", page, want)
	}
	if want := "the ChatGPT sign-in came back without registering shhh on this host; run the sign-in again"; err == nil || err.Error() != want {
		t.Errorf("Login = %v, want %q", err, want)
	}
}

// With no data directory to be found, opening the account says why: the
// home directory's own error, not a store with nothing in it.
func TestChatGPTAccount_SaysWhyThereIsNoDataDirectory(t *testing.T) {
	resetChatGPT(t)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	_, want := os.UserHomeDir()
	if want == nil {
		t.Skip("this platform finds a home directory without $HOME")
	}
	if _, err := chatGPTAccount(); err == nil || err.Error() != want.Error() {
		t.Fatalf("chatGPTAccount = %v, want %v", err, want)
	}
}
