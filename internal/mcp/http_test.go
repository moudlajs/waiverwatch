package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

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
	srv := httptest.NewServer(HTTPHandler(NewServer(svc, "test"), limit, burst))
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

func TestHealthz(t *testing.T) {
	srv := httpServer(t, 0, 0) // a closed limiter must not affect health checks
	resp, err := http.Get(srv.URL + "/healthz")
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
