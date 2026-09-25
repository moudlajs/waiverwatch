package league

import (
	"context"
	"reflect"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestSeverityRank(t *testing.T) {
	order := []string{"Out", "IR", "Doubtful", "Questionable", "COV"}
	for i := 1; i < len(order); i++ {
		if severityRank(order[i-1]) >= severityRank(order[i]) {
			t.Errorf("%s should rank before %s", order[i-1], order[i])
		}
	}
}

func TestInjuries(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha"}, {LeagueID: "B", Name: "Beta"}, {LeagueID: "C", Name: "Broken"},
		},
		// Alpha: out RB starting, questionable WR on bench, healthy QB starting.
		"/league/A/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"rbout", "wrq", "qb"}, Starters: []string{"rbout", "qb"}},
			{OwnerID: "200", Players: []string{"rbtaken"}},
		},
		// Beta: the same out RB on IR, the questionable WR starting.
		"/league/B/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"rbout", "wrq"}, Starters: []string{"wrq"}, Reserve: []string{"rbout"}},
		},
		"/players/nfl/trending/add": []sleeper.Trending{{PlayerID: "rbhot", Count: 99}},
		"/players/nfl": map[string]sleeper.Player{
			"rbout":   {FullName: "Rb Out", Position: "RB", Team: "SF", Active: true, InjuryStatus: "Out", InjuryBodyPart: "Knee"},
			"wrq":     {FullName: "Wr Questionable", Position: "WR", Team: "MIN", Active: true, InjuryStatus: "Questionable"},
			"qb":      {FullName: "Healthy Qb", Position: "QB", Team: "KC", Active: true},
			"rbtaken": {FullName: "Rb Taken", Position: "RB", Team: "KC", Active: true, SearchRank: 1},
			"rbhot":   {FullName: "Rb Hot", Position: "RB", Team: "SEA", Active: true, SearchRank: 200},
			"wrfree":  {FullName: "Wr Free", Position: "WR", Team: "NE", Active: true, SearchRank: 60},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")

	r, err := svc.Injuries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InjuredPlayer{
		{
			PlayerID: "rbout", Name: "Rb Out", Position: "RB", NFLTeam: "SF", Status: "Out", BodyPart: "Knee", StartingIn: 1,
			Leagues: []InjuryLeague{
				{League: "Alpha", Starting: true, Replacement: &Target{PlayerID: "rbhot", Name: "Rb Hot", Position: "RB", NFLTeam: "SEA", Adds: 99, SearchRank: 200}},
				{League: "Beta", OnIR: true},
			},
		},
		{
			PlayerID: "wrq", Name: "Wr Questionable", Position: "WR", NFLTeam: "MIN", Status: "Questionable", StartingIn: 1,
			Leagues: []InjuryLeague{
				{League: "Alpha"},
				{League: "Beta", Starting: true, Replacement: &Target{PlayerID: "wrfree", Name: "Wr Free", Position: "WR", NFLTeam: "NE", SearchRank: 60}},
			},
		},
	}
	if !reflect.DeepEqual(r.Players, want) {
		t.Errorf("players:\n got %+v\nwant %+v", r.Players, want)
	}
	if len(r.FailedLeagues) != 1 || r.FailedLeagues[0].League != "Broken" {
		t.Errorf("failed = %+v", r.FailedLeagues)
	}
}
