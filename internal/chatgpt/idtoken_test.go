package chatgpt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	rsaOnce sync.Once
	rsaTest *rsa.PrivateKey
)

// testRSAKey is generated once: a 2048-bit key takes long enough to make per
// test that the table would be slow for no gain.
func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	rsaOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		rsaTest = k
	})
	return rsaTest
}

// parseKeys turns JWK maps into what the verifier reads.
func parseKeys(t *testing.T, published ...map[string]string) []jwk {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"keys": published})
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatal(err)
	}
	return set.Keys
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": Issuer, "sub": testSubject, "aud": issuedClient, "email": testEmail,
		"exp": now.Add(time.Hour).Unix(), "nonce": "the-nonce",
	}
}

func TestVerifyIDToken_AcceptsASignedToken(t *testing.T) {
	now := newClock().Now()
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rs := testRSAKey(t)
	keys := parseKeys(t, ecJWK(&ec.PublicKey, "ec1"), rsaJWK(&rs.PublicKey, "rs1"))
	for name, raw := range map[string]string{
		"ES256": mintES256(t, ec, "ec1", goodClaims(now)),
		"RS256": mintRS256(t, rs, "rs1", goodClaims(now)),
	} {
		c, err := verifyIDToken(raw, keys, issuedClient, "the-nonce", now)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if c.Sub != testSubject || c.Email != testEmail {
			t.Errorf("%s: claims = %+v", name, c)
		}
	}
	// A token without a kid is tried against every key of its type.
	noKid := mintES256(t, ec, "", goodClaims(now))
	if _, err := verifyIDToken(noKid, keys, issuedClient, "the-nonce", now); err != nil {
		t.Errorf("a kid-less token: %v", err)
	}
	// aud may be a list, with azp naming this client.
	listed := goodClaims(now)
	listed["aud"], listed["azp"] = []string{issuedClient, "other"}, issuedClient
	if _, err := verifyIDToken(mintES256(t, ec, "ec1", listed), keys, issuedClient, "the-nonce", now); err != nil {
		t.Errorf("a listed audience: %v", err)
	}
}

func TestVerifyIDToken_RefusesEveryBadToken(t *testing.T) {
	now := newClock().Now()
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rs := testRSAKey(t)
	keys := parseKeys(t, ecJWK(&ec.PublicKey, "ec1"), rsaJWK(&rs.PublicKey, "rs1"))
	with := func(edit func(c map[string]any)) map[string]any {
		c := maps.Clone(goodClaims(now))
		edit(c)
		return c
	}
	unsigned := func(alg string) string {
		head := segment(t, map[string]string{"alg": alg, "kid": "ec1"})
		return head + "." + segment(t, goodClaims(now)) + "."
	}
	cases := map[string]string{
		"bad signature":   mintES256(t, other, "ec1", goodClaims(now)),
		"wrong issuer":    mintES256(t, ec, "ec1", with(func(c map[string]any) { c["iss"] = "https://evil.example" })),
		"wrong audience":  mintES256(t, ec, "ec1", with(func(c map[string]any) { c["aud"] = "oaiapp_other" })),
		"list, no azp":    mintES256(t, ec, "ec1", with(func(c map[string]any) { c["aud"] = []string{issuedClient, "x"} })),
		"expired":         mintES256(t, ec, "ec1", with(func(c map[string]any) { c["exp"] = now.Add(-2 * time.Minute).Unix() })),
		"no expiry":       mintES256(t, ec, "ec1", with(func(c map[string]any) { delete(c, "exp") })),
		"wrong nonce":     mintES256(t, ec, "ec1", with(func(c map[string]any) { c["nonce"] = "another" })),
		"no subject":      mintES256(t, ec, "ec1", with(func(c map[string]any) { c["sub"] = "" })),
		"unknown kid":     mintES256(t, ec, "nope", goodClaims(now)),
		"alg none":        unsigned("none"),
		"alg HS256":       unsigned("HS256"),
		"RS key, ES alg":  mintES256(t, ec, "rs1", goodClaims(now)),
		"not three parts": "abc.def",
	}
	// A signature checked against claims it was not made over.
	good := mintES256(t, ec, "ec1", goodClaims(now))
	parts := strings.Split(good, ".")
	tampered := with(func(c map[string]any) { c["sub"] = "someone-else" })
	cases["swapped claims"] = parts[0] + "." + segment(t, tampered) + "." + parts[2]
	// An ES256 signature of the wrong length.
	cases["short signature"] = parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString([]byte("short"))

	for name, raw := range cases {
		_, err := verifyIDToken(raw, keys, issuedClient, "the-nonce", now)
		if !errors.Is(err, errBadIDToken) {
			t.Errorf("%s: err = %v, want errBadIDToken", name, err)
		}
	}
}
