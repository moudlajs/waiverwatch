package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func registerClient(t *testing.T, base string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(base+"/register", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestRegisteredFlow signs in the way Gemini CLI does: discover, register
// with a loopback redirect, authorize with PKCE, exchange, call /mcp.
func TestRegisteredFlow(t *testing.T) {
	base, _, _ := setup(t)

	resp, err := http.Get(base + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	if meta["registration_endpoint"] != base+"/register" {
		t.Fatalf("metadata = %v", meta)
	}

	status, reg := registerClient(t, base, map[string]any{
		"client_name": "Gemini CLI MCP Client", "redirect_uris": []string{"http://localhost:7777/oauth/callback"},
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"},
	})
	clientID, _ := reg["client_id"].(string)
	if status != http.StatusCreated || !strings.HasPrefix(clientID, registeredPrefix) || reg["token_endpoint_auth_method"] != "none" {
		t.Fatalf("register: %d %v", status, reg)
	}

	// The CLI picks its port when it signs in, not when it registers.
	params := authorizeParams(clientID)
	params.Set("redirect_uri", "http://localhost:51234/oauth/callback")
	page, err := http.Get(base + "/authorize?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(html), "An app on this computer") || !strings.Contains(string(html), "Gemini CLI MCP Client") {
		t.Fatalf("authorize page: %d\n%s", page.StatusCode, html)
	}

	params.Set("username", username)
	code, loc := signIn(t, base, params)
	if code != http.StatusFound || loc.Host != "localhost:51234" || loc.Query().Get("iss") != base {
		t.Fatalf("sign in: %d %v", code, loc)
	}
	status, tok := tokenRequest(t, base, url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "client_id": {clientID},
		"redirect_uri": {params.Get("redirect_uri")}, "code_verifier": {verifier},
	})
	access, _ := tok["access_token"].(string)
	if status != http.StatusOK || access == "" {
		t.Fatalf("token: %d %v", status, tok)
	}
	if status, _, body := mcpCall(t, base, access); status != http.StatusOK || body != "moudlajs" {
		t.Errorf("mcp: %d %q", status, body)
	}
}

func TestRegisterRejects(t *testing.T) {
	base, _, _ := setup(t)
	tests := []struct {
		name string
		body any
	}{
		{"no redirect", map[string]any{"client_name": "x"}},
		{"someone else's https site", map[string]any{"redirect_uris": []string{"https://evil.example/cb"}}},
		{"a lookalike of google", map[string]any{"redirect_uris": []string{"https://google.com.evil.example/cb"}}},
		{"plain http off this machine", map[string]any{"redirect_uris": []string{"http://example.com/cb"}}},
		{"one bad among good", map[string]any{"redirect_uris": []string{"http://localhost/cb", "https://evil.example/cb"}}},
		{"a confidential client", map[string]any{"redirect_uris": []string{"http://localhost/cb"}, "token_endpoint_auth_method": "client_secret_basic"}},
		{"too many", map[string]any{"redirect_uris": []string{"http://localhost/1", "http://localhost/2", "http://localhost/3", "http://localhost/4", "http://localhost/5", "http://localhost/6"}}},
		{"not JSON", "redirect_uris=http://localhost/cb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status, body := registerClient(t, base, tt.body); status != http.StatusBadRequest || body["error"] == nil {
				t.Errorf("got %d %v, want a 400 with an error", status, body)
			}
		})
	}

	status, body := registerClient(t, base, map[string]any{"redirect_uris": []string{"https://gemini.google.com/oauth/callback"}})
	if status != http.StatusCreated {
		t.Errorf("Google's host: %d %v", status, body)
	}
}

func TestRegisteredClientLimits(t *testing.T) {
	base, _, s := setup(t)
	_, reg := registerClient(t, base, map[string]any{"redirect_uris": []string{"https://gemini.google.com/cb"}})
	clientID := reg["client_id"].(string)

	authorize := func(clientID, redirect string) int {
		params := authorizeParams(clientID)
		params.Set("redirect_uri", redirect)
		resp, err := http.Get(base + "/authorize?" + params.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := authorize(clientID, "https://gemini.google.com/other"); got != http.StatusBadRequest { // not what it registered
		t.Errorf("unregistered redirect: %d, want 400", got)
	}
	if got := authorize(clientID[:len(clientID)-2]+"xx", "https://gemini.google.com/cb"); got != http.StatusBadRequest {
		t.Errorf("tampered client_id: %d, want 400", got)
	}

	if _, ok := s.registeredClient(clientID); !ok {
		t.Fatal("fresh client_id rejected")
	}
	s.now = func() time.Time { return time.Now().Add(clientTTL + time.Hour) }
	if _, ok := s.registeredClient(clientID); ok {
		t.Error("expired client_id accepted")
	}
}
