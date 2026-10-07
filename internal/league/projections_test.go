package league

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestProjectionCache(t *testing.T) {
	players := map[string]sleeper.Player{"a": {Position: "RB"}, "b": {Position: "WR"}, "KC": {Position: "DEF"}}
	full := map[string]map[string]float64{"a": {"pts_ppr": 10}, "b": {"pts_ppr": 8}, "KC": {"pts_ppr": 7}}
	blank := map[string]map[string]float64{"a": {"adp_dd_ppr": 1000}, "b": {"adp_dd_ppr": 1000}, "KC": {"pts_ppr": 7}}
	c := projectionCache{min: 2} // a and b; the defense doesn't count
	at := time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)

	if _, note, ok := c.complete("regular/2026/5", blank, players, at); ok || !strings.Contains(note, "missing") {
		t.Errorf("blank with nothing remembered: ok %v, note %q", ok, note)
	}
	if got, note, ok := c.complete("regular/2026/5", full, players, at); !ok || note != "" || got["a"]["pts_ppr"] != 10 {
		t.Errorf("full: %v %q %v", got, note, ok)
	}
	got, note, ok := c.complete("regular/2026/5", blank, players, at.Add(time.Hour))
	if !ok || got["a"]["pts_ppr"] != 10 || !strings.Contains(note, "Wed 09:30") {
		t.Errorf("blank after full: %v %q %v; want the remembered set", got, note, ok)
	}
	if _, _, ok := c.complete("regular/2026/6", blank, players, at); ok {
		t.Error("another week must not borrow week 5's projections")
	}

	// A small slate: two teams playing need 24 players, not 150.
	slate := map[string]map[string]float64{"KC": {"pts_ppr": 7}, "BUF": {"pts_ppr": 6}}
	small := map[string]sleeper.Player{"KC": {Position: "DEF"}, "BUF": {Position: "DEF"}}
	for i := range 24 {
		id := fmt.Sprintf("p%d", i)
		slate[id] = map[string]float64{"pts_ppr": 5}
		small[id] = sleeper.Player{Position: "WR"}
	}
	if _, _, ok := (&projectionCache{}).complete("post/2026/21", slate, small, at); !ok {
		t.Error("a two-team slate with 24 projected players is complete")
	}
}

func TestLineupCheckBlankProjections(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 5, SeasonType: "regular"},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB", "RB", "BN"}}},
		"/league/A/rosters":          []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"q1", "r1"}, Starters: []string{"q1", "0"}}},
		"/players/nfl": map[string]sleeper.Player{
			"q1": {PlayerID: "q1", FullName: "Bye Week", Position: "QB", Team: "CIN"},
			"r1": {PlayerID: "r1", FullName: "Bench Back", Position: "RB", Team: "KC"},
			"KC": {PlayerID: "KC", Position: "DEF", Team: "KC"},
		},
		// Blank: only KC's defense has points, so KC plays and CIN is on bye.
		"/projections/nfl/regular/2026/5": map[string]map[string]float64{"KC": {"pts_ppr": 7}, "r1": {"adp_dd_ppr": 1000}},
	}))
	r, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").LineupCheck(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	a := r.Leagues[0]
	if !strings.Contains(r.Note, "missing") || len(a.Problems) != 2 || a.Start != nil || a.Best != 0 || a.Status != "fix" {
		t.Errorf("note %q, alpha %+v; want the bye and the empty slot, no swaps", r.Note, a)
	}
}

func TestMatchupsBlankProjections(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 5, SeasonType: "regular"},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB", "DEF"}}},
		"/league/A/rosters":          []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/A/users":            []sleeper.LeagueUser{{UserID: "100", DisplayName: "me"}},
		"/league/A/matchups/5":       []sleeper.Matchup{{RosterID: 1, Starters: []string{"q1", "KC"}}},
		"/players/nfl": map[string]sleeper.Player{
			"q1": {PlayerID: "q1", FullName: "Q B", Position: "QB", Team: "KC"},
			"KC": {PlayerID: "KC", Position: "DEF", Team: "KC"},
		},
		"/projections/nfl/regular/2026/5": map[string]map[string]float64{"KC": {"pts_ppr": 7}, "q1": {"adp_dd_ppr": 1000}},
	}))
	w, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").Matchups(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	me := w.Leagues[0].Me
	if !strings.Contains(w.Note, "missing") || me == nil || me.Projected != 0 || me.Starters[1].Projected != 0 {
		t.Errorf("note %q, me %+v; want no partial projections", w.Note, me)
	}
}
