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

// Dynamic Client Registration (RFC 7591), for MCP clients without a Client
// ID Metadata Document: Gemini (the app and the CLI) registers this way.
//
// Stateless like everything else here: the client_id is a signed token
// carrying the registered redirect URIs and name, so registering stores
// nothing and survives restarts. Registration is open, but the redirect URIs
// are not: loopback (CLIs on the user's machine, any port) or HTTPS on a
// host in redirectHosts. A token can only ever go back to one of those.

// kindClient marks a registered client's ID.
const kindClient = "client"

// registeredPrefix starts every registered client_id, to tell them from
// Claude's URL client IDs at a glance.
const registeredPrefix = "reg."

// clientTTL is how long a registered client_id stays valid. Clients
// register once and keep the ID; long, so nobody has to reconnect.
const clientTTL = 5 * 365 * 24 * time.Hour

// Registration limits: generous for real clients, small for the signed ID.
const (
	maxRedirects  = 5
	maxRedirect   = 512
	maxClientName = 100
)

// redirectHosts are the HTTPS hosts a registered client may redirect to,
// and their subdomains: Google's, for the Gemini app.
var redirectHosts = []string{"google.com"}

// registration is the part of an RFC 7591 request waiverwatch reads.
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

// registeredClient reads a client_id issued by register.
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

// checkRedirects allows loopback http URIs and HTTPS URIs on redirectHosts.
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
