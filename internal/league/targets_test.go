package league

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestTradeTargets(t *testing.T) {
	p := func(id, name, pos, injury string) sleeper.Player {
		return sleeper.Player{PlayerID: id, FullName: name, Position: pos, InjuryStatus: injury, Active: true}
	}
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "D", Name: "Dynasty", TotalRosters: 12, RosterPositions: []string{"QB", "RB", "WR", "BN"}, Settings: sleeper.LeagueSettings{Type: 2}},
			{LeagueID: "S", Name: "Settled", TotalRosters: 12, RosterPositions: []string{"QB", "BN"}},
		},
		// Me: RB thin (one healthy), WR deep (two spares), QB one spare.
		"/league/D/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"qb1", "qb2", "rb1", "wr1", "wr2", "wr3"}},
			{RosterID: 2, OwnerID: "200", Players: []string{"rbA", "rbB", "rbC", "wrX"}},
			{RosterID: 3, OwnerID: "300", Players: []string{"rbD", "rbE"}, Reserve: []string{"rbE"}},
		},
		"/league/D/users":   []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival FC"), user("300", "third", "Third")},
		"/league/S/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"qb1", "qb2"}}},
		"/league/S/users":   []sleeper.LeagueUser{user("100", "me", "")},
		"/players/nfl": map[string]sleeper.Player{
			"qb1": p("qb1", "Q One", "QB", ""), "qb2": p("qb2", "Q Two", "QB", ""), "rb1": p("rb1", "R One", "RB", ""),
			"wr1": p("wr1", "W One", "WR", ""), "wr2": p("wr2", "W Two", "WR", ""), "wr3": p("wr3", "W Three", "WR", ""),
			"rbA": p("rbA", "R A", "RB", ""), "rbB": p("rbB", "R B", "RB", ""), "rbC": p("rbC", "R C", "RB", "Out"),
			"rbD": p("rbD", "R D", "RB", ""), "rbE": p("rbE", "R E", "RB", ""), "wrX": p("wrX", "W X", "WR", ""),
		},
	}))
	market := map[string]fantasycalc.Value{
		"qb1": {Value: 7000}, "qb2": {Value: 500}, "rb1": {Value: 4000},
		"wr1": {Value: 5000}, "wr2": {Value: 3000}, "wr3": {Value: 1000},
		"rbA": {Value: 3500}, "rbB": {Value: 8000}, "rbC": {Value: 2000}, "rbD": {Value: 900}, "rbE": {Value: 1500}, "wrX": {Value: 2000},
	}
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		return market, nil
	}, "me")
	ctx := context.Background()

	t.Run("thin positions", func(t *testing.T) {
		r, err := svc.TradeTargets(ctx, "", "", 5)
		if err != nil {
			t.Fatal(err)
		}
		d, s := r.Leagues[0], r.Leagues[1]
		if !slices.Equal(d.Needs, []string{"RB thin (1 healthy, 0 backups)"}) {
			t.Errorf("needs = %v", d.Needs)
		}
		var spares []string
		for _, pv := range d.Spares {
			spares = append(spares, pv.Name)
		}
		if !slices.Equal(spares, []string{"W Two", "W Three", "Q Two"}) {
			t.Errorf("spares = %v", spares)
		}
		// R B is too expensive, R C is Out, R E is on IR, W X isn't needed.
		// R A: no single spare reaches 3500; W Two + W Three = 3000 + 1000×√(1000/3500) = 3535.
		want := []TradeTarget{
			{PlayerValue: PlayerValue{PlayerID: "rbA", Name: "R A", Position: "RB", Team: "Rival FC", Value: 3500}, Offer: []string{"W Two", "W Three"}, OfferValue: 3535},
			{PlayerValue: PlayerValue{PlayerID: "rbD", Name: "R D", Position: "RB", Team: "Third", Value: 900}, Offer: []string{"W Three"}, OfferValue: 1000},
		}
		if len(d.Targets) != 2 {
			t.Fatalf("targets = %+v", d.Targets)
		}
		for i := range want {
			g := d.Targets[i]
			if g.PlayerID != want[i].PlayerID || g.Team != want[i].Team || g.Value != want[i].Value || !slices.Equal(g.Offer, want[i].Offer) || g.OfferValue != want[i].OfferValue {
				t.Errorf("target %d = %+v, want %+v", i, g, want[i])
			}
		}
		if !strings.Contains(s.Note, "no thin spots") || len(s.Targets) != 0 {
			t.Errorf("settled league = %+v", s)
		}
	})

	t.Run("a named position", func(t *testing.T) {
		r, err := svc.TradeTargets(ctx, "dynasty", "WR", 5)
		if err != nil {
			t.Fatal(err)
		}
		d := r.Leagues[0]
		// WR isn't spare when it's what I'm after: only Q Two (500) to offer, W X (2000) is out of reach.
		if len(d.Spares) != 1 || d.Spares[0].Name != "Q Two" || len(d.Targets) != 0 || !strings.Contains(d.Note, "within reach") {
			t.Errorf("got %+v", d)
		}
		if r, err = svc.TradeTargets(ctx, "dynasty", "K", 5); err != nil || !strings.Contains(r.Leagues[0].Note, "doesn't start a K") || r.Leagues[0].Error != "" {
			t.Errorf("K: %+v, err %v", r.Leagues[0], err)
		}
	})

	t.Run("limit", func(t *testing.T) {
		r, err := svc.TradeTargets(ctx, "dynasty", "", 1)
		if err != nil || len(r.Leagues[0].Targets) != 1 || r.Leagues[0].Targets[0].PlayerID != "rbA" {
			t.Errorf("got %+v, err %v", r.Leagues[0].Targets, err)
		}
	})
}

func TestCheapestOffer(t *testing.T) {
	spares := []PlayerValue{{Name: "A", Value: 5000}, {Name: "B", Value: 3000}, {Name: "C", Value: 1000}}
	tests := []struct {
		want      int
		offer     string
		wantValue int
	}{
		{900, "C", 1000},    // the cheapest single that covers it
		{2500, "B", 3000},   // not A
		{6000, "A,B", 7121}, // 5000 + 3000×√(3000/6000)
		{9000, "A,B", 0},    // out of reach: the two best, whatever they're worth
	}
	for _, tt := range tests {
		offer, v := cheapestOffer(spares, tt.want)
		if strings.Join(offer, ",") != tt.offer || (tt.wantValue != 0 && v != tt.wantValue) {
			t.Errorf("want %d: offer %v (%d), expected %s (%d)", tt.want, offer, v, tt.offer, tt.wantValue)
		}
	}
}
