package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
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
			{LeagueID: "L1", Name: "Dynasty", TotalRosters: 1, RosterPositions: []string{"QB", "DEF"}, Settings: sleeper.LeagueSettings{Type: 2}},
		},
		"/league/L1/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"x"}, Settings: sleeper.RosterSettings{Wins: 3}}},
		"/league/L1/users":   []sleeper.LeagueUser{{UserID: "100", DisplayName: "me"}},
		"/league/L1/matchups/3": []sleeper.Matchup{
			{RosterID: 1, MatchupID: 1, Points: 42.5, Starters: []string{"4046"}, StartersPoints: []float64{42.5}},
		},
		"/players/nfl":              map[string]sleeper.Player{"4046": {PlayerID: "4046", FullName: "Patrick Mahomes", Position: "QB", Team: "KC", Active: true}, "SEA": {PlayerID: "SEA", FirstName: "Seattle", LastName: "Seahawks", Position: "DEF", Team: "SEA", Active: true}},
		"/players/nfl/trending/add": []sleeper.Trending{{PlayerID: "4046", Count: 7}},
		"/league/L1/drafts":         []sleeper.Draft{{DraftID: "D1", Season: "2026", Type: "snake", Status: "complete"}},
		"/draft/D1/picks":           []sleeper.Pick{{Round: 1, PickNo: 1, PlayerID: "4046", PickedBy: "100", RosterID: 1}},
	}))

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := NewServer(league.NewService(api, league.NewDirectory(store.NewMemory(), api.Players), username), "test").Connect(ctx, serverT, nil)
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
	var names []string
	for _, tl := range tools.Tools {
		if tl.OutputSchema == nil {
			t.Errorf("%s has no output schema", tl.Name)
		}
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	if want := []string{"compare_rosters", "draft_results", "get_matchups", "get_roster", "injury_report", "list_leagues", "trending_players", "waiver_targets"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
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

func TestGetMatchups(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{Name: "get_matchups", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var w league.Week
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	if w.Week != 3 || len(w.Leagues) != 1 || w.Leagues[0].Me == nil || w.Leagues[0].Me.Starters[0].Name != "Patrick Mahomes" {
		t.Errorf("unexpected week %+v", w)
	}
}

func TestTrendingPlayers(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{
		Name: "trending_players", Arguments: map[string]any{"position": "qb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var r league.TrendingReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	// Defaults applied and a lower-case position accepted. My L1 roster is
	// empty, so Mahomes is available there.
	if r.LookbackHours != 24 || len(r.Players) != 1 || r.Players[0].Name != "Patrick Mahomes" || len(r.Players[0].AvailableIn) != 1 {
		t.Errorf("unexpected report %+v", r)
	}
}

func TestWaiverTargetsTool(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{
		Name: "waiver_targets", Arguments: map[string]any{"position": "d/st"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var r league.WaiverReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Position != "DEF" || len(r.Leagues) != 1 || len(r.Leagues[0].Targets) != 1 || r.Leagues[0].Targets[0].Name != "Seattle Seahawks" {
		t.Errorf("unexpected report %+v", r)
	}
}

func TestNormalisePosition(t *testing.T) {
	for in, want := range map[string]string{"rb": "RB", " wr ": "WR", "DST": "DEF", "d/st": "DEF", "pk": "K", "": ""} {
		if got := normalisePosition(in); got != want {
			t.Errorf("normalisePosition(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetRoster(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{Name: "get_roster", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var r league.RosterReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Rosters) != 1 || r.Rosters[0].League != "Dynasty" || len(r.Rosters[0].Bench) != 1 {
		t.Errorf("unexpected report %+v", r)
	}
}

func TestInjuryReport(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{Name: "injury_report", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var r league.InjuryReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Players == nil || len(r.Players) != 0 { // nobody on the test roster is hurt
		t.Errorf("unexpected report %+v", r)
	}
}

func TestCompareRosters(t *testing.T) {
	// The only league has a single team, so there is no opponent this week.
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{Name: "compare_rosters", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var c league.Comparisons
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Week != 3 || len(c.Leagues) != 1 || !strings.Contains(c.Leagues[0].Note, "no opponent") {
		t.Errorf("unexpected comparisons %+v", c)
	}
}

func TestDraftResults(t *testing.T) {
	res, err := connect(t, "me").CallTool(context.Background(), &sdk.CallToolParams{
		Name: "draft_results", Arguments: map[string]any{"player": "mahomes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var r league.DraftReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.MyPicks != 1 || len(r.Leagues) != 1 || r.Leagues[0].Drafts[0].Picks[0].Name != "Patrick Mahomes" {
		t.Errorf("unexpected report %+v", r)
	}
}
