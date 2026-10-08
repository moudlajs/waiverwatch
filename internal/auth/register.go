package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Stateless Dynamic Client Registration (RFC 7591), e.g. for Gemini: the client_id is a signed token.
// Registration is open, but redirect URIs must be loopback or HTTPS on redirectHosts.

const kindClient = "client"

const registeredPrefix = "reg."

const clientTTL = 5 * 365 * 24 * time.Hour

const (
	maxRedirects  = 5
	maxRedirect   = 512
	maxClientName = 100
)

// redirectHosts (and subdomains) are the only HTTPS hosts a registered client may redirect to.
var redirectHosts = []string{"google.com"}

type registration struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var reg registration
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&reg); err != nil {
		registerError(w, "invalid_client_metadata", "expected a JSON body")
		return
	}
	if m := reg.TokenEndpointAuthMethod; m != "" && m != "none" {
		registerError(w, "invalid_client_metadata", "only public clients (token_endpoint_auth_method none, with PKCE) are supported")
		return
	}
	if err := checkRedirects(reg.RedirectURIs); err != nil {
		registerError(w, "invalid_redirect_uri", err.Error())
		return
	}
	name := []rune(strings.TrimSpace(reg.ClientName))
	name = name[:min(len(name), maxClientName)] // by character: never split one
	now := s.now()
	id := registeredPrefix + s.signer.sign(claims{
		Kind: kindClient, Name: string(name), RedirectURIs: reg.RedirectURIs, Expires: now.Add(clientTTL).Unix(),
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        now.Unix(),
		"client_name":                string(name),
		"redirect_uris":              reg.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

func (s *Server) registeredClient(clientID string) (clientMetadata, bool) {
	token, ok := strings.CutPrefix(clientID, registeredPrefix)
	if !ok {
		return clientMetadata{}, false
	}
	c, err := s.signer.verify(token, kindClient, s.now())
	if err != nil {
		return clientMetadata{}, false
	}
	return clientMetadata{ClientID: clientID, ClientName: c.Name, RedirectURIs: c.RedirectURIs}, true
}

func checkRedirects(uris []string) error {
	if len(uris) == 0 || len(uris) > maxRedirects {
		return fmt.Errorf("give 1 to %d redirect_uris", maxRedirects)
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		switch {
		case err != nil || len(raw) > maxRedirect || u.Fragment != "" || u.User != nil:
			return fmt.Errorf("redirect_uri %q is not a valid redirect", raw)
		case u.Scheme == "http" && isLoopback(u):
		case u.Scheme == "https" && googleHost(u.Hostname()):
		default:
			return fmt.Errorf("redirect_uri %q is not allowed: only loopback (http://localhost or 127.0.0.1) or Google's HTTPS hosts", raw)
		}
	}
	return nil
}

func googleHost(host string) bool {
	host = strings.ToLower(host)
	for _, h := range redirectHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func registerError(w http.ResponseWriter, code, description string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code, "error_description": description})
}

var errUnknownClient = errors.New("unknown client: sign in from Claude, or from an app that registers itself with this server")
