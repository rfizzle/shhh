package secret

import (
	"encoding/base64"
	"strings"
	"testing"
)

// hiddenToken is shaped like a refresh token: opaque, no issuer prefix, so
// the pattern table has nothing to find it by and only the value can.
const hiddenToken = "rt_3qXv9KpLm2Zs7WcYd4Hn8BfTj6RgUe1A"

// A hidden value is scrubbed in every form a declared secret is: whole, in
// each encoding, and as any eight-byte run of it. What it becomes names the
// kind and never a variable.
func TestHide_ScrubsLikeADeclaredSecret(t *testing.T) {
	v := New()
	v.Hide("chatgpt-token", hiddenToken)
	want := Redacted("chatgpt-token")
	for _, text := range []string{
		hiddenToken,
		"refresh_token=" + hiddenToken + "&x=1",
		base64.StdEncoding.EncodeToString([]byte(hiddenToken)),
		base64.RawURLEncoding.EncodeToString([]byte(hiddenToken)),
		"prefix " + hiddenToken[5:13] + " suffix",
		hiddenToken[:20] + "\n" + hiddenToken[20:],
	} {
		got := v.Scrub(text)
		if !strings.Contains(got, want) {
			t.Errorf("Scrub(%q) = %q, want the %s placeholder", text, got, want)
		}
		for i := 0; i+minFragment <= len(hiddenToken); i++ {
			if strings.Contains(got, hiddenToken[i:i+minFragment]) {
				t.Errorf("Scrub(%q) = %q still holds %q", text, got, hiddenToken[i:i+minFragment])
			}
		}
	}
	// Hiding the same value twice is one entry, and a second value is
	// scrubbed beside the first: a rotated-out token stays hidden.
	v.Hide("chatgpt-token", hiddenToken)
	v.Hide("chatgpt-token", "rt_second_value_after_rotation")
	if got := v.Scrub(hiddenToken + " rt_second_value_after_rotation"); got != want+" "+want {
		t.Errorf("Scrub after a rotation = %q", got)
	}
	v.Hide("chatgpt-token", "")
	var nilVault *Vault
	nilVault.Hide("chatgpt-token", hiddenToken) // a nil vault hides nothing and does not panic
}

// A hidden value is never named to the model and never handed to a
// command: it is outside Names, Environ, Len and the prompt block, and
// /secret's remove cannot reach it.
func TestHide_IsNotNamedOrExported(t *testing.T) {
	v := New()
	if err := v.Add("DECLARED", "declared-value-1234"); err != nil {
		t.Fatal(err)
	}
	v.Hide("chatgpt-token", hiddenToken)
	if got := v.Names(); len(got) != 1 || got[0] != "DECLARED" {
		t.Errorf("Names = %v, want only DECLARED", got)
	}
	if v.Len() != 1 {
		t.Errorf("Len = %d, want 1", v.Len())
	}
	for _, kv := range v.Environ() {
		if strings.Contains(kv, hiddenToken) {
			t.Errorf("Environ carries the hidden value: %q", kv)
		}
	}
	if v.Remove("chatgpt-token") || v.Remove("") {
		t.Error("Remove reached a hidden value")
	}
	if got := v.Scrub(hiddenToken); got != Redacted("chatgpt-token") {
		t.Errorf("Scrub after Remove = %q", got)
	}
}
