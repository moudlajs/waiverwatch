package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
)

// connect runs the server against a fake Sleeper for username and returns a
// connected client session.
func connect(t *testing.T, username string) *sdk.ClientSession {
	t.Helper()
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":  sleeper.State{Season: "2026", Week: 3},
		"/user/me":    sleeper.User{UserID: "100", DisplayName: "me"},
		"/user/ghost": nil,
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "L1", Name: "Dynasty", TotalRosters: 1, Settings: sleeper.LeagueSettings{Type: 2}},
		},
		"/league/L1/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Settings: sleeper.RosterSettings{Wins: 3}}},
		"/league/L1/users":   []sleeper.LeagueUser{{UserID: "100", DisplayName: "me"}},
	}))

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := NewServer(league.NewService(api, username), "test").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestListLeagues(t *testing.T) {
	cs := connect(t, "me")
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "list_leagues" || tools.Tools[0].OutputSchema == nil {
		t.Fatalf("unexpected tools %+v", tools.Tools)
	}

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_leagues"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var ov league.Overview
	if err := json.Unmarshal(raw, &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Week != 3 || len(ov.Leagues) != 1 || ov.Leagues[0].Wins != 3 || ov.Leagues[0].Kind != "dynasty" {
		t.Errorf("unexpected overview %+v", ov)
	}
	if len(res.Content) == 0 {
		t.Error("want a text fallback for clients without structured content")
	}
}

func TestListLeaguesUnknownUser(t *testing.T) {
	res, err := connect(t, "ghost").CallTool(context.Background(), &sdk.CallToolParams{Name: "list_leagues"})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*sdk.TextContent); ok {
			text = tc.Text
		}
	}
	if !res.IsError || !strings.Contains(text, "not found") {
		t.Errorf("IsError = %v, text %q; want a not-found tool error", res.IsError, text)
	}
}
