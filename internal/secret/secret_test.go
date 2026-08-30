package secret

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestAdd_ValidatesNameAndValue(t *testing.T) {
	v := New()
	cases := []struct {
		name, value string
		wantErr     bool
	}{
		{"API_KEY", "x", false},
		{"lower_ok", "x", false},
		{"1BAD", "x", true},
		{"WITH-DASH", "x", true},
		{"PATH", "x", true},
		{"EMPTY", "", true},
	}
	for _, tc := range cases {
		err := v.Add(tc.name, tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("Add(%q, %q) err=%v, wantErr=%v", tc.name, tc.value, err, tc.wantErr)
		}
	}
	if got := v.Names(); strings.Join(got, ",") != "API_KEY,lower_ok" {
		t.Fatalf("Names = %v", got)
	}
}

func TestAdd_ReplacesSameName(t *testing.T) {
	v := New()
	_ = v.Add("K", "first-value")
	_ = v.Add("K", "second-value")
	if v.Len() != 1 {
		t.Fatalf("Len = %d, want 1", v.Len())
	}
	if got := v.Scrub("first-value second-value"); got != "first-value [secret:K]" {
		t.Fatalf("Scrub = %q", got)
	}
	if !v.Remove("K") || v.Remove("K") {
		t.Fatal("Remove should succeed once")
	}
}

func TestScrub_WholeValueAndEncodings(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	v := New()
	if err := v.Add("STRIPE", key); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		key,
		base64.StdEncoding.EncodeToString([]byte(key)),
		base64.RawURLEncoding.EncodeToString([]byte(key)),
		hex.EncodeToString([]byte(key)),
		strings.ToUpper(hex.EncodeToString([]byte(key))),
	}
	for _, in := range cases {
		got := v.Scrub("out: " + in + " done")
		if got != "out: [secret:STRIPE] done" {
			t.Errorf("Scrub(%q) = %q", in, got)
		}
	}
}

func TestScrub_Fragments(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	v := New()
	_ = v.Add("STRIPE", key)
	cases := map[string]string{
		"prefix " + key[:12] + " end":          "prefix [secret:STRIPE] end",
		"tail " + key[len(key)-10:]:            "tail [secret:STRIPE]",
		key[:14] + "\n" + key[14:]:             "[secret:STRIPE]\n[secret:STRIPE]",
		"short " + key[:7] + " stays":          "short " + key[:7] + " stays",
		"nothing to see":                       "nothing to see",
		"twice " + key[:9] + " and " + key[3:]: "twice [secret:STRIPE] and [secret:STRIPE]",
	}
	for in, want := range cases {
		if got := v.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrub_ShortSecretsMatchWhole(t *testing.T) {
	v := New()
	_ = v.Add("PIN", "1234")
	if got := v.Scrub("pin is 1234, not 123"); got != "pin is [secret:PIN], not 123" {
		t.Fatalf("Scrub = %q", got)
	}
}

func TestScrub_NilVaultIsIdentity(t *testing.T) {
	var v *Vault
	if got := v.Scrub("anything"); got != "anything" {
		t.Fatal("nil vault must not change text")
	}
	if v.Len() != 0 || v.Names() != nil || v.Environ() != nil {
		t.Fatal("nil vault holds nothing")
	}
}

func TestEnviron(t *testing.T) {
	v := New()
	_ = v.Add("B", "2")
	_ = v.Add("A", "1")
	if got := strings.Join(v.Environ(), " "); got != "B=2 A=1" {
		t.Fatalf("Environ = %q", got)
	}
}

func TestWrapExecutor_ScrubsResultsAndErrors(t *testing.T) {
	v := New()
	_ = v.Add("TOKEN", "hunter2hunter2")
	exec := v.WrapExecutor(func(name string, args json.RawMessage) (string, error) {
		if name == "fail" {
			return "", errors.New("bad hunter2hunter2")
		}
		return "token=hunter2hunter2", nil
	})
	out, err := exec("ok", nil)
	if err != nil || out != "token=[secret:TOKEN]" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := exec("fail", nil); err == nil || err.Error() != "bad [secret:TOKEN]" {
		t.Fatalf("err=%v", err)
	}
}

func TestScrubMessages_CopiesAndScrubsEveryText(t *testing.T) {
	v := New()
	_ = v.Add("TOKEN", "hunter2hunter2")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "key hunter2hunter2", Attachments: []provider.Attachment{
			{Kind: provider.AttachmentText, Data: []byte("hunter2hunter2")},
		}},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Arguments: `{"c":"echo hunter2hunter2"}`}}},
	}
	got := v.ScrubMessages(msgs)
	if got[0].Content != "key [secret:TOKEN]" || string(got[0].Attachments[0].Data) != "[secret:TOKEN]" {
		t.Fatalf("user message not scrubbed: %+v", got[0])
	}
	if got[1].ToolCalls[0].Arguments != `{"c":"echo [secret:TOKEN]"}` {
		t.Fatalf("tool call not scrubbed: %+v", got[1])
	}
	if msgs[0].Content != "key hunter2hunter2" || msgs[1].ToolCalls[0].Arguments != `{"c":"echo hunter2hunter2"}` {
		t.Fatal("original messages must be untouched")
	}
}

func TestPromptBlock(t *testing.T) {
	if PromptBlock(nil) != "" || PromptBlock(New()) != "" {
		t.Fatal("no secrets, no block")
	}
	v := New()
	_ = v.Add("API_KEY", "value-value-value")
	block := PromptBlock(v)
	for _, want := range []string{"$API_KEY", "[secret:API_KEY]", "## Secrets"} {
		if !strings.Contains(block, want) {
			t.Errorf("block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "value-value-value") {
		t.Fatal("the prompt block must never carry a value")
	}
}
