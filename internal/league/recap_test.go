package league

import (
	"context"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestWeekRecap(t *testing.T) {
	me := sleeper.LeagueUser{UserID: "100", DisplayName: "me"}
	rival := sleeper.LeagueUser{UserID: "200", DisplayName: "rival"}
	slots := []string{"QB", "RB", "FLEX", "BN", "BN", "BN"}
	// Loss: the benched rb2 (30) beat the FLEX wr1 (5), and ir1 (40) is on IR so it can't start.
	lossMine := sleeper.Matchup{RosterID: 1, MatchupID: 1, Points: 35,
		Players: []string{"qb1", "rb1", "wr1", "rb2", "ir1"}, Starters: []string{"qb1", "rb1", "wr1"},
		PlayersPoints: map[string]float64{"qb1": 20, "rb1": 10, "wr1": 5, "rb2": 30, "ir1": 40}}
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 6, SeasonType: "regular"},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "L", Name: "Loss", RosterPositions: slots},
			{LeagueID: "W", Name: "Win", RosterPositions: slots},
			{LeagueID: "G", Name: "Guillotine", RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "E", Name: "Gone", RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 3}},
		},
		"/league/L/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100", Reserve: []string{"ir1"}}, {RosterID: 2, OwnerID: "200"}},
		"/league/L/users":      []sleeper.LeagueUser{me, rival},
		"/league/L/matchups/5": []sleeper.Matchup{lossMine, {RosterID: 2, MatchupID: 1, Points: 50, Starters: []string{"x"}}},
		"/league/W/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}, {RosterID: 2, OwnerID: "200"}},
		"/league/W/users":      []sleeper.LeagueUser{me, rival},
		"/league/W/matchups/5": []sleeper.Matchup{
			{RosterID: 1, MatchupID: 3, Points: 60, Players: []string{"qb1", "rb2"}, Starters: []string{"qb1", "rb2", "0"},
				PlayersPoints: map[string]float64{"qb1": 30, "rb2": 30}},
			{RosterID: 2, MatchupID: 3, Points: 40, Starters: []string{"x"}},
		},
		"/league/G/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"qb1"}}, {RosterID: 2}, {RosterID: 3}},
		"/league/G/users":   []sleeper.LeagueUser{me},
		"/league/G/matchups/5": []sleeper.Matchup{
			{RosterID: 1, Points: 20, Players: []string{"qb1"}, Starters: []string{"qb1"}, PlayersPoints: map[string]float64{"qb1": 20}},
			{RosterID: 2, Points: 25, Starters: []string{"x"}},
			{RosterID: 3, Points: 0, Starters: []string{"0"}}, // cut earlier: not alive
		},
		"/league/E/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/E/users":      []sleeper.LeagueUser{me},
		"/league/E/matchups/5": []sleeper.Matchup{{RosterID: 1, Starters: []string{"0"}}},
		"/players/nfl": map[string]sleeper.Player{
			"qb1": {FullName: "Quarter Back", Position: "QB"}, "rb1": {FullName: "Run One", Position: "RB"},
			"rb2": {FullName: "Run Two", Position: "RB"}, "wr1": {FullName: "Wide One", Position: "WR"},
			"ir1": {FullName: "Hurt Guy", Position: "RB"},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")

	r, err := svc.WeekRecap(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Week != 5 || len(r.Leagues) != 4 || r.Note != "" {
		t.Fatalf("week %d, %d leagues, note %q", r.Week, len(r.Leagues), r.Note)
	}

	l := r.Leagues[0]
	if l.Result != "loss" || l.Opponent != "rival" || l.OpponentPoints != 50 || l.Best != 60 || l.LeftOnBench != 25 || !l.CouldHaveWon {
		t.Errorf("loss = %+v", l)
	}
	if len(l.ShouldStart) != 1 || l.ShouldStart[0].Name != "Run Two" || len(l.ShouldSit) != 1 || l.ShouldSit[0].Name != "Wide One" {
		t.Errorf("swaps: start %+v, sit %+v", l.ShouldStart, l.ShouldSit)
	}
	if l.TopScorer == nil || l.TopScorer.Name != "Quarter Back" || l.TopScorer.Points != 20 {
		t.Errorf("top scorer %+v", l.TopScorer)
	}

	if w := r.Leagues[1]; w.Result != "win" || w.LeftOnBench != 0 || w.CouldHaveWon || w.ShouldStart != nil {
		t.Errorf("win with an empty FLEX and nobody to fill it = %+v", w)
	}
	if g := r.Leagues[2]; g.Result != "cut" || g.Rank != 2 || g.Alive != 2 {
		t.Errorf("guillotine = %+v", g)
	}
	if e := r.Leagues[3]; e.Result != "eliminated" || e.Error != "" {
		t.Errorf("eliminated earlier = %+v", e)
	}
	if r.Wins != 1 || r.Losses != 1 || r.Ties != 0 || r.LeftOnBench != 25 {
		t.Errorf("totals: %d-%d-%d, bench %v", r.Wins, r.Losses, r.Ties, r.LeftOnBench)
	}
}

func TestWeekRecapBeforeAnyWeek(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 1},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "L", Name: "L"}},
	}))
	r, err := NewService(api, nil, nil, "me").WeekRecap(context.Background(), 0, "")
	if err != nil || r.Note == "" || len(r.Leagues) != 0 {
		t.Errorf("got %+v, err %v; want a note and no leagues", r, err)
	}
}
