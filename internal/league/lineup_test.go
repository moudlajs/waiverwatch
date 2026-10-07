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

func TestCheckLineup(t *testing.T) {
	p := func(name, pos, team, injury string) sleeper.Player {
		return sleeper.Player{FullName: name, Position: pos, Team: team, InjuryStatus: injury}
	}
	players := map[string]sleeper.Player{
		"qb":   p("Q B", "QB", "KC", ""),
		"rb1":  p("R One", "RB", "DET", "Out"),
		"rb2":  p("R Two", "RB", "SF", ""),
		"rb3":  p("R Three", "RB", "SF", ""),
		"wr1":  p("W One", "WR", "CIN", ""), // CIN on bye
		"wr2":  p("W Two", "WR", "MIN", "Questionable"),
		"wr3":  p("W Three", "WR", "MIN", ""),
		"te":   p("T E", "TE", "KC", ""),
		"ir":   p("I R", "RB", "KC", ""),
		"taxi": p("T X", "WR", "KC", ""),
		"dl":   p("D L", "DL", "KC", ""),
	}
	proj := map[string]float64{"qb": 20, "rb1": 15, "rb2": 12, "rb3": 4, "wr1": 14, "wr2": 9, "wr3": 11, "te": 6, "ir": 30, "taxi": 25, "dl": 8}
	wk := week{playing: map[string]bool{"KC": true, "DET": true, "SF": true, "MIN": true}}
	pts := func(id string) float64 { return proj[id] }
	slots := []string{"QB", "RB", "WR", "WR", "FLEX", "DL", "TE", "BN"}

	t.Run("problems and a better lineup", func(t *testing.T) {
		r := sleeper.Roster{
			Players:  []string{"qb", "rb1", "rb2", "rb3", "wr1", "wr2", "wr3", "ir", "taxi", "dl"},
			Starters: []string{"qb", "rb1", "wr1", "wr2", "rb3", "dl", "0"},
			Reserve:  []string{"ir"}, Taxi: []string{"taxi"},
		}
		problems, start, bench, current, best := checkLineup(slots, r, players, wk, pts)
		wantProblems := []string{"R One (RB) is Out", "W One (WR) is on bye", "W Two (WR) is questionable", "TE slot is empty"}
		if !slices.Equal(problems, wantProblems) {
			t.Errorf("problems = %q", problems)
		}
		// Now: QB 20 + WR2 9 + FLEX rb3 4 = 33 (Out, bye and the DL slot don't count).
		// Best: QB 20, RB rb2 12, WR wr3 11 + wr2 9, FLEX rb3 4; no TE on the roster. IR and taxi never start.
		if current != 33 || best != 56 {
			t.Errorf("current %v, best %v", current, best)
		}
		names := func(ps []LineupPlayer) (out []string) {
			for _, p := range ps {
				out = append(out, p.Slot+":"+p.Name)
			}
			return out
		}
		if got := names(start); !slices.Equal(got, []string{"RB:R Two", "WR:W Three"}) {
			t.Errorf("start = %v", got)
		}
		if got := names(bench); !slices.Equal(got, []string{"RB:R One", "WR:W One"}) {
			t.Errorf("bench = %v", got)
		}
	})

	t.Run("a lineup that is already right", func(t *testing.T) {
		r := sleeper.Roster{Players: []string{"qb", "rb2", "wr3", "rb3", "te"}, Starters: []string{"qb", "rb2", "wr3", "0"}}
		problems, start, bench, current, best := checkLineup([]string{"QB", "RB", "WR"}, r, players, wk, pts)
		if len(problems) != 0 || start != nil || bench != nil || current != 43 || best != 43 {
			t.Errorf("got %v %v %v %v %v", problems, start, bench, current, best)
		}
	})

	t.Run("fewer starters than slots", func(t *testing.T) {
		r := sleeper.Roster{Players: []string{"qb", "rb2"}, Starters: []string{"qb"}}
		problems, start, _, _, _ := checkLineup([]string{"QB", "RB", "BN"}, r, players, wk, pts)
		if !slices.Equal(problems, []string{"RB slot is empty"}) || len(start) != 1 || start[0].Name != "R Two" {
			t.Errorf("problems %v, start %v", problems, start)
		}
	})

	t.Run("any fill beats a hole", func(t *testing.T) {
		tiny := map[string]float64{"te": 0.6}
		r := sleeper.Roster{Players: []string{"te"}, Starters: []string{"0"}}
		_, start, _, _, _ := checkLineup([]string{"TE"}, r, players, wk, func(id string) float64 { return tiny[id] })
		if len(start) != 1 || start[0].Name != "T E" {
			t.Errorf("start = %v, want the 0.6-point TE in the empty slot", start)
		}
	})

	t.Run("a tiny gain isn't worth a swap", func(t *testing.T) {
		near := map[string]float64{"wr3": 11, "rb2": 11.5}
		r := sleeper.Roster{Players: []string{"wr3", "rb2"}, Starters: []string{"wr3"}}
		_, start, _, current, best := checkLineup([]string{"FLEX"}, r, players, wk, func(id string) float64 { return near[id] })
		if start != nil || current != 11 || best != 11.5 {
			t.Errorf("start %v, %v → %v", start, current, best)
		}
	})
}

func TestLineupCheck(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5, SeasonType: "regular"},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB", "BN"}, Scoring: sleeper.Scoring{Rec: 0}},
			{LeagueID: "G", Name: "Gone", RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 3}},
		},
		"/league/A/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"q1", "q2"}, Starters: []string{"q1"}}},
		"/league/G/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/players/nfl": map[string]sleeper.Player{
			"q1": {PlayerID: "q1", FullName: "Bye Week", Position: "QB", Team: "CIN"},
			"q2": {PlayerID: "q2", FullName: "Plays Today", Position: "QB", Team: "KC"},
		},
		// CIN has nobody projected: on bye. Standard scoring reads pts_std.
		"/projections/nfl/regular/2026/5": map[string]map[string]float64{"q2": {"pts_ppr": 20, "pts_std": 18}},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")
	svc.projections.min = 1 // a one-player feed counts as complete here
	r, err := svc.LineupCheck(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	a, g := r.Leagues[0], r.Leagues[1]
	if a.Status != "fix" || !slices.Equal(a.Problems, []string{"Bye Week (QB) is on bye"}) || len(a.Start) != 1 || a.Start[0].Name != "Plays Today" || a.Best != 18 {
		t.Errorf("alpha = %+v", a)
	}
	if !strings.Contains(g.Note, "eliminated") || g.Status != "ok" {
		t.Errorf("guillotine = %+v", g)
	}
}

func TestLineupCheckNoGames(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                  sleeper.State{Season: "2026", Week: 0, SeasonType: "pre"},
		"/user/me":                    sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026":  []sleeper.League{{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB"}}},
		"/players/nfl":                map[string]sleeper.Player{},
		"/projections/nfl/pre/2026/0": map[string]map[string]float64{},
	}))
	r, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").LineupCheck(context.Background(), "")
	if err != nil || len(r.Leagues) != 0 || !strings.Contains(r.Note, "no NFL games") {
		t.Errorf("got %+v, err %v", r, err)
	}
}
