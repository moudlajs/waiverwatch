package league

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestValueSettings(t *testing.T) {
	tests := []struct {
		name   string
		league sleeper.League
		want   fantasycalc.Settings
	}{
		{"1QB redraft", sleeper.League{TotalRosters: 12, RosterPositions: []string{"QB", "RB", "FLEX", "BN"}, Scoring: sleeper.Scoring{Rec: 1}},
			fantasycalc.Settings{QBs: 1, Teams: 12, PPR: 1}},
		{"superflex dynasty without a QB slot", sleeper.League{TotalRosters: 10, RosterPositions: []string{"RB", "SUPER_FLEX", "SUPER_FLEX"}, Settings: sleeper.LeagueSettings{Type: 2}},
			fantasycalc.Settings{Dynasty: true, QBs: 2, Teams: 10}},
		{"one superflex slot only", sleeper.League{TotalRosters: 32, RosterPositions: []string{"RB", "WR", "SUPER_FLEX"}, Scoring: sleeper.Scoring{Rec: 0.5}},
			fantasycalc.Settings{QBs: 1, Teams: 32, PPR: 0.5}},
		{"keeper is not dynasty", sleeper.League{TotalRosters: 12, RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 1}},
			fantasycalc.Settings{QBs: 1, Teams: 12}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValueSettings(tt.league); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFindPlayers(t *testing.T) {
	players := map[string]sleeper.Player{
		"1": {PlayerID: "1", FullName: "Ja'Marr Chase", Position: "WR", Active: true, SearchRank: 3},
		"2": {PlayerID: "2", FullName: "Josh Allen", Position: "QB", Active: true, SearchRank: 1},
		"3": {PlayerID: "3", FullName: "Josh Allen", Position: "LB", Active: true}, // IDP: not a fantasy position here
		"4": {PlayerID: "4", FullName: "Kenneth Walker", Position: "RB", Active: true, SearchRank: 30},
		"5": {PlayerID: "5", FullName: "Walker Little", Position: "OL"},
		"6": {PlayerID: "6", FullName: "Old Walker", Position: "RB"}, // retired: last
		"7": {PlayerID: "7", FullName: "Joshua Allenby", Position: "TE", Active: true, SearchRank: 400},
	}
	tests := []struct {
		query string
		want  []string
	}{
		{"jamarr", []string{"1"}},            // punctuation and case ignored
		{"  JA'MARR  chase ", []string{"1"}}, // exact after folding
		{"josh allen", []string{"2"}},        // exact beats Joshua Allenby
		{"walker", []string{"4", "6"}},       // active first
		{"nobody", nil},
		{"'", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var got []string
			for _, p := range findPlayers(players, tt.query) {
				got = append(got, p.PlayerID)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValues(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "D", Name: "Dynasty", TotalRosters: 10, RosterPositions: []string{"SUPER_FLEX", "SUPER_FLEX"}, Settings: sleeper.LeagueSettings{Type: 2}, Scoring: sleeper.Scoring{Rec: 1}},
			{LeagueID: "R", Name: "Redraft", TotalRosters: 32, RosterPositions: []string{"QB"}, Scoring: sleeper.Scoring{Rec: 1}},
		},
		"/league/D/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"wr", "qb", "k"}},
			{RosterID: 2, OwnerID: "200", Players: []string{"rb"}},
		},
		"/league/D/users": []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival FC")},
		"/league/R/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"rb"}},
		},
		"/league/R/users": []sleeper.LeagueUser{user("100", "me", "")},
		"/players/nfl": map[string]sleeper.Player{
			"qb": {PlayerID: "qb", FullName: "Q Back", Position: "QB", Active: true},
			"rb": {PlayerID: "rb", FullName: "R Back", Position: "RB", Active: true},
			"wr": {PlayerID: "wr", FullName: "W Out", Position: "WR", Active: true},
			"k":  {PlayerID: "k", FullName: "K Foot", Position: "K", Active: true},
		},
	}))
	markets := func(_ context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		if s.Dynasty {
			return map[string]fantasycalc.Value{"qb": {Value: 9000, Tier: 1}, "wr": {Value: 6000}, "rb": {Value: 3000}}, nil
		}
		return map[string]fantasycalc.Value{"rb": {Value: 5000}}, nil
	}
	// Indirect, so a subtest can swap the market out.
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), func(ctx context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		return markets(ctx, s)
	}, "me")
	ctx := context.Background()

	t.Run("my roster", func(t *testing.T) {
		r, err := svc.Values(ctx, "dynasty", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		d := r.Leagues[0]
		if d.Market != "dynasty superflex 10-team PPR" || d.Team != "Mine" || d.Total != 15000 || r.Source == "" {
			t.Errorf("got %+v", d)
		}
		if len(d.Players) != 3 || d.Players[0].Name != "Q Back" || d.Players[0].Tier != 1 || d.Players[2].Name != "K Foot" || d.Players[2].Value != 0 {
			t.Errorf("want players by value, unrated kicker last: %+v", d.Players)
		}
	})

	t.Run("an owner's roster", func(t *testing.T) {
		r, err := svc.Values(ctx, "", "rival", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Leagues) != 1 || r.Leagues[0].Owner != "rival" || r.Leagues[0].Total != 3000 {
			t.Errorf("got %+v", r.Leagues)
		}
	})

	t.Run("named players in every league", func(t *testing.T) {
		r, err := svc.Values(ctx, "", "", []string{"r back", "zzz"})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Leagues) != 2 || len(r.NotFound) != 1 || r.NotFound[0] != "zzz" {
			t.Fatalf("got %+v", r)
		}
		dyn, red := r.Leagues[0].Players[0], r.Leagues[1].Players[0]
		if dyn.Value != 3000 || dyn.Team != "Rival FC" || dyn.Mine {
			t.Errorf("dynasty: %+v", dyn)
		}
		if red.Value != 5000 || !red.Mine || r.Leagues[1].Market != "redraft 1QB 14-team PPR" {
			t.Errorf("redraft: %+v in %q", red, r.Leagues[1].Market)
		}
	})

	t.Run("nobody matches", func(t *testing.T) {
		if _, err := svc.Values(ctx, "", "", []string{"zzz"}); err == nil || !strings.Contains(err.Error(), "zzz") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("source down fails per league", func(t *testing.T) {
		markets = func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
			return nil, fantasycalc.ErrUnavailable
		}
		r, err := svc.Values(ctx, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Leagues) != 2 || !strings.Contains(r.Leagues[0].Error, "FantasyCalc") {
			t.Errorf("got %+v", r.Leagues)
		}
	})

	t.Run("not set up", func(t *testing.T) {
		_, err := NewService(api, nil, nil, "me").Values(ctx, "", "", nil)
		if err == nil || errors.Is(err, fantasycalc.ErrUnavailable) {
			t.Errorf("err = %v", err)
		}
	})
}
