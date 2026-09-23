package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
)

// askInbox writes one raw line to an inbox and reads the answer, over an
// in-memory pair — the inbox's conversation is one message and one answer,
// and nothing about it needs a listener to be read.
func askInbox(t *testing.T, in *Inbox, raw string) response {
	t.Helper()
	var out bytes.Buffer
	in.ServeConn(strings.NewReader(raw+"\n"), &out)
	var resp response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("the inbox answered %q: %v", out.String(), err)
	}
	return resp
}

func TestInbox_TakesOneLineAndSaysWhatBecameOfIt(t *testing.T) {
	var got SendParams
	in := NewInbox(func(p SendParams) (string, error) { got = p; return TakenHeld, nil })
	resp := askInbox(t, in, `{"jsonrpc":"2.0","id":7,"method":"session/send","params":{"from":"a","to":"b","text":"rebase"}}`)
	if resp.Error != nil {
		t.Fatalf("a well-formed line was refused: %v", resp.Error)
	}
	if string(resp.ID) != "7" {
		t.Errorf("the answer is addressed to the request's own id, got %s", resp.ID)
	}
	if got != (SendParams{From: "a", To: "b", Text: "rebase"}) {
		t.Errorf("the line reached the session as %+v", got)
	}
	if res, _ := json.Marshal(resp.Result); !strings.Contains(string(res), `"taken":"held"`) {
		t.Errorf("result %s", res)
	}
}

func TestInbox_TakesNothingButTheOneVerb(t *testing.T) {
	called := false
	in := NewInbox(func(SendParams) (string, error) { called = true; return TakenDelivered, nil })
	for _, method := range []string{
		MethodApprovalAnswer, MethodTurnStart, MethodTurnSteer, MethodSessionStart,
		MethodAgentKill, "config/set", "command/run",
	} {
		resp := askInbox(t, in, `{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":{"text":"y"}}`)
		if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
			t.Errorf("%s was answered: %+v", method, resp)
		}
	}
	if called {
		t.Error("a method other than session/send reached the session")
	}
}

func TestInbox_RefusesWhatIsNotALine(t *testing.T) {
	in := NewInbox(func(SendParams) (string, error) { t.Fatal("reached the session"); return "", nil })
	for name, raw := range map[string]string{
		"not json":      `send it`,
		"wrong version": `{"jsonrpc":"1.0","method":"session/send","params":{"text":"x"}}`,
		"no text":       `{"jsonrpc":"2.0","method":"session/send","params":{"text":"  "}}`,
		"too long":      `{"jsonrpc":"2.0","method":"session/send","params":{"text":"` + strings.Repeat("x", MaxLineBytes+1) + `"}}`,
		"far too long":  strings.Repeat("x", MaxLineBytes*2),
	} {
		if resp := askInbox(t, in, raw); resp.Error == nil {
			t.Errorf("%s: answered %+v", name, resp)
		}
	}
}

func TestInbox_ARefusalIsItsOwnCode(t *testing.T) {
	in := NewInbox(func(SendParams) (string, error) { return "", ErrRefused })
	resp := askInbox(t, in, `{"jsonrpc":"2.0","id":1,"method":"session/send","params":{"text":"x"}}`)
	if resp.Error == nil || resp.Error.Code != CodeRefused {
		t.Fatalf("got %+v", resp)
	}
}

func TestSend_OneMessageOneAnswer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	in := NewInbox(func(p SendParams) (string, error) {
		if p.Text != "rebase onto master" || p.From != "lane-a" {
			return "", errors.New("wrong line")
		}
		return TakenDelivered, nil
	})
	go func() {
		defer server.Close()
		in.ServeConn(server, server)
	}()
	res, err := Send(client, SendParams{From: "lane-a", To: "lane-b", Text: "rebase onto master"})
	if err != nil || res.Taken != TakenDelivered {
		t.Fatalf("Send = %+v, %v", res, err)
	}
}

func TestSend_ARefusalComesBackAsTheWireError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	in := NewInbox(func(SendParams) (string, error) { return "", ErrRefused })
	go func() {
		defer server.Close()
		in.ServeConn(server, server)
	}()
	_, err := Send(client, SendParams{Text: "x"})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != CodeRefused {
		t.Fatalf("got %v", err)
	}
}
