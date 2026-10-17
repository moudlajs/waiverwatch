package league

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func exposureRoutes() sleepertest.Routes {
	return sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 6, SeasonType: "regular"},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha"}, {LeagueID: "B", Name: "Beta"}, {LeagueID: "C", Name: "Cut"},
		},
		// star starts in both; bench1 is rostered twice but never starts; solo starts once; spare is benched once.
		"/league/A/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"star", "bench1", "solo"}, Starters: []string{"star", "solo"}}},
		"/league/B/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"star", "bench1", "spare"}, Starters: []string{"star"}}},
		"/league/C/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100"}}, // eliminated guillotine team
		"/players/nfl": map[string]sleeper.Player{
			"star":   {FullName: "Star Back", Position: "RB", Team: "DET"},
			"bench1": {FullName: "Bench Guy", Position: "WR", Team: "KC", InjuryStatus: "Out"},
			"solo":   {FullName: "Solo Wide", Position: "WR", Team: "MIA"},
			"spare":  {FullName: "Spare Part", Position: "TE", Team: "DET"},
			"det":    {FullName: "Det Def", Position: "DEF", Team: "DET"},
			"kc":     {FullName: "KC Def", Position: "DEF", Team: "KC"},
		},
		// MIA has nobody projected: on bye.
		"/projections/nfl/regular/2026/6": map[string]map[string]float64{"det": {"pts_ppr": 8}, "kc": {"pts_ppr": 7}},
	}
}

func TestPlayerExposure(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, exposureRoutes()))
	e, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").PlayerExposure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e.Leagues != 2 || e.Note != "" {
		t.Fatalf("leagues %d, note %q; want 2 (the eliminated team skipped) and no note", e.Leagues, e.Note)
	}
	var names []string
	for _, p := range e.Players {
		names = append(names, p.Name)
	}
	if want := []string{"Star Back", "Solo Wide", "Bench Guy"}; !slices.Equal(names, want) {
		t.Fatalf("players %v, want %v (Spare Part: one league, benched)", names, want)
	}
	star, solo, bench := e.Players[0], e.Players[1], e.Players[2]
	if star.Rostered != 2 || star.Starting != 2 || !slices.Equal(star.StartingIn, []string{"Alpha", "Beta"}) || star.OnBye {
		t.Errorf("star = %+v", star)
	}
	if !solo.OnBye || solo.Starting != 1 {
		t.Errorf("solo = %+v, want on bye", solo)
	}
	if bench.Rostered != 2 || bench.Starting != 0 || bench.Injury != "Out" {
		t.Errorf("bench = %+v", bench)
	}
	if len(e.NFLTeams) != 2 || e.NFLTeams[0] != (NFLTeamExposure{NFLTeam: "DET", Starters: 2}) ||
		e.NFLTeams[1] != (NFLTeamExposure{NFLTeam: "MIA", Starters: 1, OnBye: true}) {
		t.Errorf("nfl teams = %+v", e.NFLTeams)
	}
}

func TestPlayerExposureWithoutProjections(t *testing.T) {
	routes := exposureRoutes()
	delete(routes, "/projections/nfl/regular/2026/6")
	api := sleeper.New(sleepertest.NewServer(t, routes))
	e, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").PlayerExposure(context.Background())
	if err != nil || !strings.Contains(e.Note, "byes unknown") || len(e.Players) != 3 {
		t.Fatalf("got %+v, err %v; want players and a byes-unknown note", e, err)
	}
	for _, p := range e.Players {
		if p.OnBye {
			t.Errorf("%s marked on bye without projections", p.Name)
		}
	}
}
