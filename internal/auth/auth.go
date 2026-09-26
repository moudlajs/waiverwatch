// Package auth is waiverwatch's own small OAuth 2.1 authorization server.
// It knows OAuth, not MCP or football.
//
// Claude identifies itself with a Client ID Metadata Document (its client_id
// is an HTTPS URL); only Claude's documents are accepted. People sign in with
// their Sleeper username: Sleeper data is public, so identity only says which
// user to answer for (docs/multi-user.md). Codes and tokens are HMAC-signed
// and carry that identity, so nothing is stored and restarts don't sign
// anyone out.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/time/rate"
)

// Claude's published client identities.
const (
	ClaudeClient     = "https://claude.ai/oauth/mcp-oauth-client-metadata"   // claude.ai, desktop and mobile apps
	ClaudeCodeClient = "https://claude.ai/oauth/claude-code-client-metadata" // Claude Code
)

const (
	codeTTL    = 2 * time.Minute
	accessTTL  = time.Hour
	refreshTTL = 90 * 24 * time.Hour
	scope      = "mcp"
)

// Identity is who a token speaks for: a Sleeper user.
type Identity struct {
	UserID   string
	Username string
}

// ErrNoSuchUser is what a Lookup returns for a username Sleeper doesn't know.
var ErrNoSuchUser = errors.New("no such Sleeper user")

// Lookup resolves a Sleeper username, returning ErrNoSuchUser if it doesn't
// exist.
type Lookup func(ctx context.Context, username string) (Identity, error)

// Config configures a Server.
type Config struct {
	BaseURL    string   // public origin, e.g. https://waiverwatch-xyz.a.run.app
	SigningKey []byte   // HMAC key for codes and tokens; at least 32 bytes
	Lookup     Lookup   // checks the username given at sign-in
	Allowed    []string // if set, only these Sleeper usernames may sign in
	Clients    []string // accepted client_id URLs; default Claude's two
	HTTPClient *http.Client
}

// Server serves the OAuth endpoints and guards the MCP endpoint.
type Server struct {
	base       string
	signer     signer
	lookup     Lookup
	allowed    map[string]bool // empty: anyone
	clients    map[string]bool
	httpClient *http.Client
	now        func() time.Time

	// Sign-in attempts across everyone: each costs a Sleeper lookup.
	logins *rate.Limiter

	mu        sync.Mutex
	usedCodes map[string]time.Time // single-use codes, kept until they expire
	docs      map[string]cachedDoc // client metadata documents
}

// New checks cfg and returns a Server.
func New(cfg Config) (*Server, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !isLoopback(u)) {
		return nil, fmt.Errorf("base URL %q must be an https origin", cfg.BaseURL)
	}
	if cfg.Lookup == nil {
		return nil, errors.New("a username lookup is required")
	}
	if len(cfg.SigningKey) < 32 {
		return nil, errors.New("signing key must be at least 32 bytes")
	}
	clients := cfg.Clients
	if len(clients) == 0 {
		clients = []string{ClaudeClient, ClaudeCodeClient}
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	s := &Server{
		base:       base,
		signer:     signer{key: cfg.SigningKey},
		lookup:     cfg.Lookup,
		allowed:    make(map[string]bool),
		clients:    make(map[string]bool),
		httpClient: hc,
		now:        time.Now,
		logins:     rate.NewLimiter(5, 20),
		usedCodes:  make(map[string]time.Time),
		docs:       make(map[string]cachedDoc),
	}
	for _, c := range clients {
		s.clients[c] = true
	}
	for _, u := range cfg.Allowed {
		if u = strings.ToLower(strings.TrimSpace(u)); u != "" {
			s.allowed[u] = true
		}
	}
	return s, nil
}

// Resource is the protected MCP endpoint's URL, as the owner adds it in Claude.
func (s *Server) Resource() string { return s.base + "/mcp" }

func (s *Server) resourceMetadataURL() string {
	return s.base + "/.well-known/oauth-protected-resource/mcp"
}

// Routes registers the discovery documents and the OAuth endpoints.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationServer)
	mux.HandleFunc("GET /authorize", s.authorizeForm)
	mux.HandleFunc("POST /authorize", s.authorizeSubmit)
	mux.HandleFunc("POST /token", s.token)
}

// Protect lets requests with a valid access token for Resource through,
// with the token's identity available to MCP tools (see UserFrom), and
// answers the rest with the 401 challenge that starts Claude's sign-in.
func (s *Server) Protect(next http.Handler) http.Handler {
	return sdkauth.RequireBearerToken(s.verifyAccess, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.resourceMetadataURL(),
		Scopes:              []string{scope},
	})(next)
}

func (s *Server) verifyAccess(_ context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	c, err := s.signer.verify(token, kindAccess, s.now())
	// Tokens from before usernames (owner passphrase era) carry no subject:
	// refusing them makes Claude sign in again.
	if err != nil || c.Audience != s.Resource() || c.Subject == "" {
		return nil, sdkauth.ErrInvalidToken
	}
	return &sdkauth.TokenInfo{
		Scopes:     []string{scope},
		Expiration: time.Unix(c.Expires, 0),
		UserID:     c.Subject,
		Extra:      map[string]any{usernameKey: c.Username},
	}, nil
}

const usernameKey = "sleeper_username"

// UserFrom returns the identity Protect verified for a request, from the
// token info MCP passes to tools. ok is false without a token (stdio).
func UserFrom(ti *sdkauth.TokenInfo) (Identity, bool) {
	if ti == nil || ti.UserID == "" {
		return Identity{}, false
	}
	name, _ := ti.Extra[usernameKey].(string)
	return Identity{UserID: ti.UserID, Username: name}, name != ""
}

// allowedUser reports whether username may sign in: anyone, unless an
// allowlist is configured.
func (s *Server) allowedUser(username string) bool {
	return len(s.allowed) == 0 || s.allowed[strings.ToLower(username)]
}

// useCode marks a code ID as spent. It reports false if it already was.
func (s *Server) useCode(id string, expires time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, exp := range s.usedCodes {
		if now.After(exp) {
			delete(s.usedCodes, k)
		}
	}
	if _, used := s.usedCodes[id]; used {
		return false
	}
	s.usedCodes[id] = expires
	return true
}

// pkceOK checks an S256 code verifier against its challenge.
func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}
