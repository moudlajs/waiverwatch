package league

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/dynastyprocess"
	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestDynastyProcessValues(t *testing.T) {
	fetch := DynastyProcessValues(func(context.Context) (map[string]dynastyprocess.Player, error) {
		return map[string]dynastyprocess.Player{
			"qb": {SleeperID: "qb", Name: "Q", Position: "QB", Value1QB: 5000, Value2QB: 9000},
			"wr": {SleeperID: "wr", Name: "W", Position: "WR", Value1QB: 8000, Value2QB: 7000},
			"rb": {SleeperID: "rb", Name: "R", Position: "RB", Value1QB: 6000, Value2QB: 5500},
		}, nil
	})
	oneQB, err := fetch(context.Background(), fantasycalc.Settings{QBs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if q := oneQB["qb"]; q.Value != 5000 || q.OverallRank != 3 || q.PositionRank != 1 || oneQB["wr"].OverallRank != 1 {
		t.Errorf("1QB: %+v", oneQB)
	}
	superflex, _ := fetch(context.Background(), fantasycalc.Settings{QBs: 2})
	if q := superflex["qb"]; q.Value != 9000 || q.OverallRank != 1 {
		t.Errorf("superflex: %+v", superflex)
	}
}

func TestFallback(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 5},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "D", Name: "Dynasty", TotalRosters: 2, RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 2}}},
		"/league/D/rosters":          []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"qb"}}, {RosterID: 2, OwnerID: "200", Players: []string{"wr"}}},
		"/league/D/users":            []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival")},
		"/league/D/traded_picks":     []sleeper.TradedPick{},
		"/players/nfl": map[string]sleeper.Player{
			"qb": {PlayerID: "qb", FullName: "Q B", Position: "QB", Active: true},
			"wr": {PlayerID: "wr", FullName: "W R", Position: "WR", Active: true},
		},
	}))
	down := func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		return nil, fantasycalc.ErrUnavailable
	}
	backup := func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		return map[string]fantasycalc.Value{"qb": {Value: 4000}, "wr": {Value: 3000}}, nil
	}
	ctx := context.Background()
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), down, "me").WithFallback(backup)

	r, err := svc.Values(ctx, "", "", nil)
	if err != nil || r.Leagues[0].Total != 4000 || !strings.Contains(r.Leagues[0].Note, "DynastyProcess") {
		t.Errorf("player_values: %+v, err %v", r.Leagues, err)
	}
	tr, err := svc.EvaluateTrade(ctx, "", []string{"q b"}, []string{"w r"})
	if err != nil || tr.GetValue != 3000 || !strings.Contains(strings.Join(tr.Notes, " "), "DynastyProcess") {
		t.Errorf("evaluate_trade: %+v, err %v", tr, err)
	}

	// Both down: FantasyCalc's error, the one people know.
	bothDown := NewService(api, NewDirectory(store.NewMemory(), api.Players), down, "me").
		WithFallback(func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
			return nil, dynastyprocess.ErrUnavailable
		})
	if _, err := bothDown.EvaluateTrade(ctx, "", []string{"q b"}, []string{"w r"}); !errors.Is(err, fantasycalc.ErrUnavailable) {
		t.Errorf("both down: err = %v, want FantasyCalc's", err)
	}
}
