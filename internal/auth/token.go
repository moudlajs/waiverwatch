package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Token kinds: a token of one kind is never accepted as another.
const (
	kindCode    = "code"
	kindAccess  = "access"
	kindRefresh = "refresh"
)

type claims struct {
	Kind         string   `json:"k"`
	ClientID     string   `json:"c"`
	Subject      string   `json:"s,omitempty"`  // Sleeper user ID
	Username     string   `json:"u,omitempty"`  // Sleeper username
	Audience     string   `json:"a,omitempty"`  // access tokens: the MCP resource URL
	RedirectURI  string   `json:"r,omitempty"`  // codes
	Challenge    string   `json:"pc,omitempty"` // codes: PKCE S256 challenge
	ID           string   `json:"j,omitempty"`  // codes: single-use ID
	Name         string   `json:"n,omitempty"`
	RedirectURIs []string `json:"rs,omitempty"`
	Expires      int64    `json:"e"`
}

var errBadToken = errors.New("invalid token")

// signer makes and checks HMAC-SHA256 tokens: base64url(json) "." base64url(mac).
type signer struct{ key []byte }

func (s signer) sign(c claims) string {
	raw, _ := json.Marshal(c) // strings and ints only: cannot fail
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.mac(body))
}

func (s signer) verify(token, kind string, now time.Time) (claims, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return claims{}, errBadToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(body)) {
		return claims{}, errBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return claims{}, errBadToken
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return claims{}, errBadToken
	}
	if c.Kind != kind || now.Unix() >= c.Expires {
		return claims{}, errBadToken
	}
	return c, nil
}

func (s signer) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
