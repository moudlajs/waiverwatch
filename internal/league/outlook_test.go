package league

import (
	"context"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func team(id, wins, losses int, ppg float64) sleeper.Roster {
	pf := ppg * float64(wins+losses)
	return sleeper.Roster{RosterID: id, OwnerID: string(rune('a' + id)), Settings: sleeper.RosterSettings{
		Wins: wins, Losses: losses, Fpts: int(pf), FptsDecimal: int((pf - float64(int(pf))) * 100),
	}}
}

func pairs(ids ...int) []sleeper.Matchup { // consecutive IDs play each other
	var ms []sleeper.Matchup
	for i, id := range ids {
		ms = append(ms, sleeper.Matchup{RosterID: id, MatchupID: i/2 + 1})
	}
	return ms
}

func TestSimulate(t *testing.T) {
	rosters := []sleeper.Roster{team(1, 3, 1, 150), team(2, 2, 2, 110), team(3, 2, 2, 105), team(4, 1, 3, 70)}
	weeks := [][]sleeper.Matchup{pairs(1, 4, 2, 3), pairs(1, 2, 3, 4), pairs(1, 3, 2, 4)}

	ctx := context.Background()
	r, _ := simulate(ctx, rosters, weeks, 2, false, 42)
	if r.playoffs[1] < 95 || r.playoffs[4] > 5 {
		t.Errorf("playoffs = %v: the 150-ppg team is in, the 70-ppg team out", r.playoffs)
	}
	if r.games[gameKey{week: 0, roster: 1}] < 90 {
		t.Errorf("150 vs 70: win %% = %v", r.games[gameKey{week: 0, roster: 1}])
	}
	// Every remaining game has one winner: 3 weeks × 2 games on top of today's 8 wins.
	if total := r.wins[1] + r.wins[2] + r.wins[3] + r.wins[4]; total < 13.9 || total > 14.1 {
		t.Errorf("expected wins add to %v, want 14", total)
	}
	if again, _ := simulate(ctx, rosters, weeks, 2, false, 42); again.playoffs[2] != r.playoffs[2] {
		t.Error("the same seed must give the same answer")
	}

	// A median game adds exactly half the league's teams' worth of wins per week.
	m, _ := simulate(ctx, rosters, weeks, 2, true, 42)
	if total := m.wins[1] + m.wins[2] + m.wins[3] + m.wins[4]; total < 19.9 || total > 20.1 {
		t.Errorf("with the median game, expected wins add to %v, want 20", total)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := simulate(cancelled, rosters, weeks, 2, false, 42); err == nil {
		t.Error("a cancelled request should stop the simulation")
	}
}

func TestPPG(t *testing.T) {
	st := sleeper.RosterSettings{Wins: 5, Losses: 3, Fpts: 480} // 8 results
	if got := ppg(st, false); got != 60 {
		t.Errorf("one game a week: %v, want 60", got)
	}
	if got := ppg(st, true); got != 120 { // 4 weeks with a median game
		t.Errorf("with a median game: %v, want 120", got)
	}
	if got := ppg(sleeper.RosterSettings{}, false); got != 0 {
		t.Errorf("no games: %v", got)
	}
}

func TestSeasonOutlook(t *testing.T) {
	week5 := pairs(1, 4, 2, 3)
	week6 := pairs(1, 2, 3, 4)
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5},
		"/user/me":   sleeper.User{UserID: "b"},
		"/user/b/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "H", Name: "Head", Settings: sleeper.LeagueSettings{PlayoffWeekStart: 7, PlayoffTeams: 2}},
			{LeagueID: "G", Name: "Guillotine", Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "N", Name: "No playoffs"},
			{LeagueID: "U", Name: "Unset", Settings: sleeper.LeagueSettings{PlayoffWeekStart: 7, PlayoffTeams: 1}},
		},
		"/league/U/rosters":    []sleeper.Roster{team(1, 1, 0, 100), team(2, 0, 1, 90)},
		"/league/U/users":      []sleeper.LeagueUser{user("b", "me", "")},
		"/league/U/matchups/5": pairs(1, 2),
		"/league/U/matchups/6": []sleeper.Matchup{}, // not set yet
		"/league/H/rosters":    []sleeper.Roster{team(1, 3, 1, 150), team(2, 2, 2, 110), team(3, 2, 2, 105), team(4, 1, 3, 70)},
		"/league/H/users":      []sleeper.LeagueUser{user("b", "me", "Mine"), user("c", "top", "Top Dogs"), user("e", "low", "Low")},
		"/league/H/matchups/5": week5,
		"/league/H/matchups/6": week6,
	}))
	r, err := NewService(api, NewDirectory(store.NewMemory(), nil), nil, "me").SeasonOutlook(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	h, g, n := r.Leagues[0], r.Leagues[1], r.Leagues[2]
	// I'm roster 1 (owner "b"): 3-1 at 150 ppg; weeks 5 and 6 remain, against Low (70) and roster 2 (110).
	if h.Record != "3-1-0" || h.Standing != 1 || len(h.Schedule) != 2 || h.Schedule[0].Opponent != "Low" || h.Schedule[0].PPG != 70 {
		t.Fatalf("head = %+v", h)
	}
	if *h.PlayoffChance < 95 || *h.ExpectedWins < 4.5 || h.Schedule[0].WinPct < 90 || h.ScheduleRating != "easier" {
		t.Errorf("head chances = %+v", h)
	}
	if !strings.Contains(g.Note, "guillotine") || g.PlayoffChance != nil || !strings.Contains(n.Note, "no playoffs") {
		t.Errorf("guillotine %+v, no playoffs %+v", g, n)
	}
	if u := r.Leagues[3]; !strings.Contains(u.Note, "future matchups") || len(u.Schedule) != 1 {
		t.Errorf("unset weeks = %+v", u)
	}
	if !strings.Contains(r.Method, "simulated seasons") {
		t.Errorf("method = %q", r.Method)
	}
}
