package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/moudlajs/waiverwatch/internal/auth"
	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func httpServer(t *testing.T, limit rate.Limit, burst int) *httptest.Server {
	t.Helper()
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 3},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{},
	}))
	svc := league.NewService(api, league.NewDirectory(store.NewMemory(), api.Players), "me")
	srv := httptest.NewServer(HTTPHandler(NewServer(svc, "test"), "test", nil, limit, burst))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPToolCall(t *testing.T) {
	srv := httpServer(t, rate.Inf, 1)
	ctx := context.Background()

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).
		Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_leagues", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); !ok || !strings.Contains(tc.Text, `"week":3`) {
		t.Errorf("unexpected content %+v", res.Content)
	}
}

func TestHealth(t *testing.T) {
	srv := httpServer(t, 0, 0) // a closed limiter must not affect health checks
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestRateLimit(t *testing.T) {
	srv := httpServer(t, 0, 1) // one request, never refilled

	statuses := make([]int, 3)
	for i := range statuses {
		resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		statuses[i] = resp.StatusCode
	}
	if statuses[0] == http.StatusTooManyRequests || statuses[1] != http.StatusTooManyRequests || statuses[2] != http.StatusTooManyRequests {
		t.Errorf("statuses = %v, want the first allowed and the rest 429", statuses)
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "127.0.0.1:0", http.NotFoundHandler()) }()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v after cancel, want nil", err)
	}
}

func TestServeReportsListenErrors(t *testing.T) {
	if err := Serve(context.Background(), "not-an-address", http.NotFoundHandler()); err == nil {
		t.Error("want a listen error")
	}
}

func TestHTTPRequiresSignIn(t *testing.T) {
	signIn, err := auth.New(auth.Config{
		BaseURL: "https://waiverwatch.example", SigningKey: []byte("0123456789abcdef0123456789abcdef"),
		Lookup: func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, auth.ErrNoSuchUser },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := league.NewService(sleeper.New("http://unused.invalid"), nil, "me")
	srv := httptest.NewServer(HTTPHandler(NewServer(svc, "test"), "v9.9.9", signIn, rate.Inf, 1))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Errorf("unauthenticated /mcp: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	for path, want := range map[string]string{
		"/health": "ok v9.9.9",
		"/.well-known/oauth-protected-resource/mcp": `"resource":"https://waiverwatch.example/mcp"`,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("%s: %d %q, want %q", path, resp.StatusCode, body, want)
		}
	}
}

// Two signed-in users, one server with no default user: each tool call
// answers for the user in its own token.
func TestToolsAnswerForTheSignedInUser(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":               sleeper.State{Season: "2026", Week: 3},
		"/user/alice":              sleeper.User{UserID: "1", Username: "alice"},
		"/user/bob":                sleeper.User{UserID: "2", Username: "bob"},
		"/user/1/leagues/nfl/2026": []sleeper.League{{LeagueID: "A", Name: "Alice League"}},
		"/user/2/leagues/nfl/2026": []sleeper.League{{LeagueID: "B", Name: "Bob League"}},
		"/league/A/rosters":        []sleeper.Roster{{RosterID: 1, OwnerID: "1"}},
		"/league/A/users":          []sleeper.LeagueUser{{UserID: "1", DisplayName: "alice"}},
		"/league/B/rosters":        []sleeper.Roster{{RosterID: 1, OwnerID: "2"}},
		"/league/B/users":          []sleeper.LeagueUser{{UserID: "2", DisplayName: "bob"}},
	}))
	svc := league.NewService(api, nil, "") // hosted: no default user

	as := func(id, name string) *sdk.CallToolRequest {
		return &sdk.CallToolRequest{Extra: &sdk.RequestExtra{TokenInfo: &sdkauth.TokenInfo{
			UserID: id, Extra: map[string]any{"sleeper_username": name},
		}}}
	}
	for _, tt := range []struct{ id, name, league string }{{"1", "alice", "Alice League"}, {"2", "bob", "Bob League"}} {
		ov, err := svc.Overview(forUser(context.Background(), as(tt.id, tt.name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(ov.Leagues) != 1 || ov.Leagues[0].Name != tt.league {
			t.Errorf("%s got %+v", tt.name, ov.Leagues)
		}
	}

	// No token and no default user: an error, never someone else's leagues.
	if _, err := svc.Overview(forUser(context.Background(), &sdk.CallToolRequest{})); err == nil {
		t.Error("want an error without a user")
	}
}
