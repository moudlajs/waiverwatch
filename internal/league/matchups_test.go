package league

import (
	"context"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestOpponent(t *testing.T) {
	ms := []sleeper.Matchup{
		{RosterID: 1, MatchupID: 1},
		{RosterID: 2, MatchupID: 2},
		{RosterID: 3, MatchupID: 1},
		{RosterID: 4, MatchupID: 0}, // bye
		{RosterID: 5, MatchupID: 0}, // also a bye: not each other's opponent
	}
	tests := []struct {
		name     string
		me       int
		wantOpp  int
		wantPair bool
	}{
		{"paired", 1, 3, true},
		{"paired the other way", 3, 1, true},
		{"alone in its matchup", 2, 0, false},
		{"bye", 4, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			me, _ := find(ms, tt.me)
			opp, ok := Opponent(ms, me)
			if ok != tt.wantPair || opp.RosterID != tt.wantOpp {
				t.Errorf("Opponent(%d) = %d, %v; want %d, %v", tt.me, opp.RosterID, ok, tt.wantOpp, tt.wantPair)
			}
		})
	}
}

func TestSide(t *testing.T) {
	players := map[string]sleeper.Player{
		"4046": {PlayerID: "4046", FullName: "Patrick Mahomes", Position: "QB", Team: "KC", InjuryStatus: "Questionable"},
		"SEA":  {PlayerID: "SEA", FirstName: "Seattle", LastName: "Seahawks", Position: "DEF", Team: "SEA"},
	}
	m := sleeper.Matchup{
		Points:         30.5,
		Starters:       []string{"4046", "0", "99999", "SEA"},
		StartersPoints: []float64{20.5, 0, 4, 6},
	}
	got := side(m, "My Team", []string{"QB", "RB", "FLEX"}, players)

	want := []Starter{
		{Slot: "QB", Name: "Patrick Mahomes", Position: "QB", NFLTeam: "KC", Injury: "Questionable", Points: 20.5},
		{Slot: "RB", Name: "(empty)"},
		{Slot: "FLEX", Name: "99999", Points: 4}, // unknown ID degrades to the ID
		{Slot: "?", Name: "Seattle Seahawks", Position: "DEF", NFLTeam: "SEA", Points: 6},
	}
	if got.Team != "My Team" || got.Points != 30.5 || len(got.Starters) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got.Starters[i] != want[i] {
			t.Errorf("starter %d = %+v, want %+v", i, got.Starters[i], want[i])
		}
	}
}

func TestSurvival(t *testing.T) {
	full := []string{"p"}
	rosters := []sleeper.Roster{
		{RosterID: 1, Players: full},
		{RosterID: 2, Players: full},
		{RosterID: 3, Players: full},
		{RosterID: 4},                // eliminated earlier: ignored even though it scores 0
		{RosterID: 6, Players: full}, // survivor with no matchup entry: ignored, not 0
	}
	ms := []sleeper.Matchup{
		{RosterID: 1, Points: 80.1},
		{RosterID: 2, Points: 60.25},
		{RosterID: 3, Points: 90},
		{RosterID: 4, Points: 0},
	}
	tests := []struct {
		name string
		me   int
		want Survival
	}{
		{"safe in the middle", 1, Survival{Alive: 4, Rank: 2, Margin: 19.85}},
		{"currently last", 2, Survival{Alive: 4, Rank: 3, Margin: -19.85}},
		{"top", 3, Survival{Alive: 4, Rank: 1, Margin: 29.75}},
		{"eliminated", 4, Survival{Eliminated: true, Alive: 4}},
		{"last team standing", 5, Survival{Alive: 1, Rank: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rosters, ms := rosters, ms
			if tt.me == 5 {
				rosters = []sleeper.Roster{{RosterID: 5, Players: full}, {RosterID: 4}}
				ms = []sleeper.Matchup{{RosterID: 5, Points: 70}}
			}
			var mine sleeper.Roster
			for _, r := range rosters {
				if r.RosterID == tt.me {
					mine = r
				}
			}
			if got := survival(ms, rosters, mine); *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestMatchups(t *testing.T) {
	me := sleeper.LeagueUser{UserID: "100", DisplayName: "me"}
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "H", Name: "Head to head", RosterPositions: []string{"QB", "BN"}},
			{LeagueID: "G", Name: "Guillotine", RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "X", Name: "Broken"},
		},
		"/league/H/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}, {RosterID: 2, OwnerID: "200"}},
		"/league/H/users":      []sleeper.LeagueUser{me, {UserID: "200", DisplayName: "rival"}},
		"/league/H/matchups/2": []sleeper.Matchup{{RosterID: 1, MatchupID: 7, Points: 99, Starters: []string{"4046"}, StartersPoints: []float64{99}}, {RosterID: 2, MatchupID: 7, Points: 80}},
		"/league/G/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"4046"}}, {RosterID: 2, OwnerID: "200", Players: []string{"1"}}},
		"/league/G/users":      []sleeper.LeagueUser{me},
		"/league/G/matchups/2": []sleeper.Matchup{{RosterID: 1, MatchupID: 1, Points: 50}, {RosterID: 2, MatchupID: 2, Points: 40}},
		"/league/X/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/X/users":      []sleeper.LeagueUser{me},
		"/players/nfl":         map[string]sleeper.Player{"4046": {PlayerID: "4046", FullName: "Patrick Mahomes"}},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")

	w, err := svc.Matchups(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if w.Week != 2 || len(w.Leagues) != 3 {
		t.Fatalf("week %d, %d leagues", w.Week, len(w.Leagues))
	}

	h := w.Leagues[0]
	if h.Me == nil || h.Opponent == nil || h.Survival != nil || h.Error != "" {
		t.Fatalf("head to head = %+v", h)
	}
	if h.Me.Points != 99 || h.Me.Starters[0].Name != "Patrick Mahomes" || h.Opponent.Team != "rival" || h.Opponent.Points != 80 {
		t.Errorf("head to head me=%+v opp=%+v", h.Me, h.Opponent)
	}

	g := w.Leagues[1]
	if g.Opponent != nil || g.Survival == nil || *g.Survival != (Survival{Alive: 2, Rank: 1, Margin: 10}) {
		t.Errorf("guillotine = %+v, survival %+v", g, g.Survival)
	}

	if x := w.Leagues[2]; x.Error == "" || x.Me != nil {
		t.Errorf("broken league = %+v, want an error", x)
	}
}
