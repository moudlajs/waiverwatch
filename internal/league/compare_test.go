package league

import (
	"context"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestSideBySide(t *testing.T) {
	players := map[string]sleeper.Player{
		"qb1": {FullName: "Qb Mine", Position: "QB"},
		"rb1": {FullName: "Rb Mine", Position: "RB"},
		"rb2": {FullName: "Rb Bench", Position: "RB"},
		"te1": {FullName: "Te Hurt", Position: "TE", InjuryStatus: "IR"},
		"qb2": {FullName: "Qb Theirs", Position: "QB"},
		"lb1": {FullName: "Lb Theirs", Position: "LB"},
	}
	mine := sleeper.Roster{Players: []string{"qb1", "rb1", "rb2", "te1"}, Starters: []string{"qb1", "rb1", "0"}, Reserve: []string{"te1"}}
	them := sleeper.Roster{Players: []string{"qb2", "lb1"}, Starters: []string{"qb2"}}

	got := sideBySide(mine, them, []string{"QB", "RB", "WR", "BN"}, players)

	var order []string
	for _, pc := range got {
		order = append(order, pc.Position)
	}
	if strings.Join(order, ",") != "QB,RB,TE,LB" { // known positions in fantasy order, then the rest
		t.Fatalf("positions = %v", order)
	}
	rb := got[1]
	if len(rb.Mine) != 2 || rb.Mine[0].Slot != "RB" || rb.Mine[1].Slot != "" || rb.Theirs == nil || len(rb.Theirs) != 0 {
		t.Errorf("RB = %+v (starter first, then bench; empty slot skipped; theirs is [] not null)", rb)
	}
	if te := got[2]; len(te.Mine) != 1 || te.Mine[0].Slot != "IR" {
		t.Errorf("TE = %+v, want the IR player marked", te)
	}
}

func TestCompare(t *testing.T) {
	me := user("100", "me", "Mine")
	rival := user("200", "rival", "Rival FC")
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "H", Name: "Head", RosterPositions: []string{"QB"}},
			{LeagueID: "B", Name: "Bye"},
			{LeagueID: "G", Name: "Guillotine", Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "M", Name: "Missing"},
		},
		"/league/H/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"qb1"}, Starters: []string{"qb1"}, Settings: sleeper.RosterSettings{Wins: 2, Fpts: 250}},
			{RosterID: 2, OwnerID: "200", Players: []string{"qb2"}, Starters: []string{"qb2"}, Settings: sleeper.RosterSettings{Losses: 2}},
			{RosterID: 3, OwnerID: "300"},
		},
		"/league/H/users":      []sleeper.LeagueUser{me, rival, user("300", "third", "")},
		"/league/H/matchups/3": []sleeper.Matchup{{RosterID: 1, MatchupID: 1}, {RosterID: 2, MatchupID: 1}, {RosterID: 3, MatchupID: 2}},
		"/league/B/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/B/users":      []sleeper.LeagueUser{me},
		"/league/B/matchups/3": []sleeper.Matchup{{RosterID: 1}},
		"/league/G/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}, {RosterID: 2, OwnerID: "200"}},
		"/league/G/users":      []sleeper.LeagueUser{me, rival},
		"/league/G/matchups/3": []sleeper.Matchup{{RosterID: 1, MatchupID: 1}, {RosterID: 2, MatchupID: 2}},
		"/league/M/rosters":    []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/M/users":      []sleeper.LeagueUser{me},
		"/league/M/matchups/3": []sleeper.Matchup{}, // my roster has no entry at all
		"/players/nfl": map[string]sleeper.Player{
			"qb1": {FullName: "Qb Mine", Position: "QB"}, "qb2": {FullName: "Qb Theirs", Position: "QB"},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")
	ctx := context.Background()

	t.Run("this week's opponents", func(t *testing.T) {
		c, err := svc.Compare(ctx, "", "")
		if err != nil {
			t.Fatal(err)
		}
		h, b, g := c.Leagues[0], c.Leagues[1], c.Leagues[2]
		if h.Them == nil || h.Them.Team != "Rival FC" || h.Me.Record != "2-0-0" || h.Me.PointsFor != 250 || h.Them.Record != "0-2-0" {
			t.Errorf("head = %+v me=%+v them=%+v", h, h.Me, h.Them)
		}
		if len(h.Positions) != 1 || h.Positions[0].Mine[0].Name != "Qb Mine" || h.Positions[0].Theirs[0].Name != "Qb Theirs" {
			t.Errorf("positions = %+v", h.Positions)
		}
		if !strings.Contains(b.Note, "bye") || b.Them != nil {
			t.Errorf("bye = %+v", b)
		}
		if !strings.Contains(g.Note, "guillotine") || g.Them != nil {
			t.Errorf("guillotine = %+v", g)
		}
		if m := c.Leagues[3]; !strings.Contains(m.Error, "no week 3 matchup") || m.Note != "" {
			t.Errorf("missing matchup = %+v, want an error, not a bye note", m)
		}
	})

	t.Run("a named owner across leagues", func(t *testing.T) {
		c, err := svc.Compare(ctx, "", "rival")
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Leagues) != 2 || c.Leagues[0].League != "Head" || c.Leagues[1].League != "Guillotine" || c.Leagues[1].Them.Owner != "rival" {
			t.Errorf("leagues = %+v", c.Leagues)
		}
	})

	t.Run("comparing with myself", func(t *testing.T) {
		c, err := svc.Compare(ctx, "head", "mine")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c.Leagues[0].Error, "my own team") {
			t.Errorf("got %+v", c.Leagues[0])
		}
	})
}
