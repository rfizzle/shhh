package chatgpt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
)

// discovery is the part of the OpenID configuration a sign-in and a
// sign-out read.
type discovery struct {
	Issuer             string `json:"issuer"`
	JWKSURI            string `json:"jwks_uri"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

// statusError is a document the server answered with something other than
// 200.
type statusError struct {
	what   string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("the ChatGPT sign-in server's %s answered HTTP %d", e.what, e.status)
}

// discover reads the OpenID configuration. A document naming another issuer,
// or pointing its keys or its revocation anywhere but https, is refused: the
// keys it names are the ones every ID token is checked against.
func (a *Account) discover(ctx context.Context) (discovery, error) {
	status, body, err := a.get(ctx, DiscoveryURL)
	if err != nil {
		return discovery{}, err
	}
	if status != http.StatusOK {
		return discovery{}, &statusError{what: "configuration", status: status}
	}
	var d discovery
	if err := json.Unmarshal(body, &d); err != nil {
		return discovery{}, fmt.Errorf("the ChatGPT sign-in server's configuration would not parse: %w", err)
	}
	if d.Issuer != Issuer {
		return discovery{}, errors.New("the ChatGPT sign-in server's configuration names another issuer")
	}
	if !strings.HasPrefix(d.JWKSURI, "https://") {
		return discovery{}, errors.New("the ChatGPT sign-in server's configuration names no https key set")
	}
	if d.RevocationEndpoint != "" && !strings.HasPrefix(d.RevocationEndpoint, "https://") {
		d.RevocationEndpoint = ""
	}
	return d, nil
}

// jwk is one key of a JSON Web Key Set, the fields RS256 and ES256 need.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// fetchKeys reads the key set the configuration named.
func (a *Account) fetchKeys(ctx context.Context, uri string) ([]jwk, error) {
	status, body, err := a.get(ctx, uri)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &statusError{what: "key set", status: status}
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("the ChatGPT sign-in server's key set would not parse: %w", err)
	}
	return set.Keys, nil
}

// idClaims are the ID token claims a sign-in reads.
type idClaims struct {
	Iss   string   `json:"iss"`
	Sub   string   `json:"sub"`
	Aud   audience `json:"aud"`
	Azp   string   `json:"azp"`
	Exp   float64  `json:"exp"`
	Nonce string   `json:"nonce"`
	Email string   `json:"email"`
}

// audience is the aud claim, which is a string or a list of them.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("aud is neither a string nor a list")
	}
	*a = many
	return nil
}

// clockSkew is how far past its expiry an ID token is still taken. The token
// is checked seconds after it was minted; a minute covers a host clock that
// drifts, and no more.
const clockSkew = time.Minute

// errBadIDToken is every reason an ID token is refused, so the message says
// which check failed and nothing of the token itself.
var errBadIDToken = errors.New("the ChatGPT sign-in returned an ID token that does not check out")

// verifyIDToken checks an ID token's signature against the server's keys and
// its claims against this sign-in: issued by OpenAI, for this client, not
// expired, and carrying the nonce this attempt sent — the last is what stops
// a token minted for another sign-in being replayed into this one.
//
// Only RS256 and ES256 are accepted, and the key must be of the algorithm's
// type: an `alg` the token chooses for itself is how `none` and HMAC-with-
// the-public-key forgeries get in. A key is found by kid; a token without
// one is tried against every key of its type.
// See docs/capabilities/chatgpt-plan.md#what-the-sign-in-checks.
func verifyIDToken(raw string, keys []jwk, clientID, nonce string, now time.Time) (idClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return idClaims{}, fmt.Errorf("%w: not a signed token", errBadIDToken)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return idClaims{}, fmt.Errorf("%w: the header would not decode", errBadIDToken)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return idClaims{}, fmt.Errorf("%w: the signature would not decode", errBadIDToken)
	}
	kty := map[string]string{"RS256": "RSA", "ES256": "EC"}[header.Alg]
	if kty == "" {
		return idClaims{}, fmt.Errorf("%w: signed with %q, not RS256 or ES256", errBadIDToken, cleanCode(header.Alg))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified, candidates := false, 0
	for _, k := range keys {
		if k.Kty != kty || (header.Kid != "" && k.Kid != header.Kid) ||
			(k.Alg != "" && k.Alg != header.Alg) || (k.Use != "" && k.Use != "sig") {
			continue
		}
		candidates++
		if verifyWith(k, header.Alg, digest[:], sig) {
			verified = true
			break
		}
	}
	switch {
	case candidates == 0:
		return idClaims{}, fmt.Errorf("%w: signed by a key the server does not publish", errBadIDToken)
	case !verified:
		return idClaims{}, fmt.Errorf("%w: the signature does not match", errBadIDToken)
	}
	var c idClaims
	if err := decodeSegment(parts[1], &c); err != nil {
		return idClaims{}, fmt.Errorf("%w: the claims would not decode", errBadIDToken)
	}
	switch {
	case c.Iss != Issuer:
		return idClaims{}, fmt.Errorf("%w: issued by someone else", errBadIDToken)
	case !slices.Contains(c.Aud, clientID) || (len(c.Aud) > 1 && c.Azp != clientID):
		return idClaims{}, fmt.Errorf("%w: issued to another client", errBadIDToken)
	case c.Exp == 0 || now.After(time.Unix(int64(c.Exp), 0).Add(clockSkew)):
		return idClaims{}, fmt.Errorf("%w: expired", errBadIDToken)
	case subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1:
		return idClaims{}, fmt.Errorf("%w: minted for another sign-in", errBadIDToken)
	case c.Sub == "":
		return idClaims{}, fmt.Errorf("%w: it names no account", errBadIDToken)
	}
	return c, nil
}

// verifyWith checks one signature with one key.
func verifyWith(k jwk, alg string, digest, sig []byte) bool {
	switch alg {
	case "RS256":
		pub, err := rsaKey(k)
		if err != nil {
			return false
		}
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest, sig) == nil
	case "ES256":
		pub, err := ecKey(k)
		if err != nil || len(sig) != 64 {
			return false
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(pub, digest, r, s)
	}
	return false
}

// minRSABits refuses a key too short to trust; 2048 is the floor every
// current signing guideline names.
const minRSABits = 2048

func rsaKey(k jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil || len(e) == 0 || len(e) > 4 {
		return nil, errors.New("bad exponent")
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	if pub.N.BitLen() < minRSABits || pub.E < 3 {
		return nil, errors.New("weak key")
	}
	return pub, nil
}

// ecKey builds a P-256 key, refusing a point that is not on the curve.
func ecKey(k jwk) (*ecdsa.PublicKey, error) {
	if k.Crv != "P-256" {
		return nil, errors.New("not P-256")
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil || len(x) != 32 {
		return nil, errors.New("bad x")
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil || len(y) != 32 {
		return nil, errors.New("bad y")
	}
	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
}

// decodeSegment decodes one base64url JSON segment of a token.
func decodeSegment(seg string, into any) error {
	data, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}
