package league

import (
	"context"
	"reflect"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

// depthPlayers: id -> position (and injury after a colon).
func depthPlayers(spec map[string]string) map[string]sleeper.Player {
	ps := map[string]sleeper.Player{}
	for id, v := range spec {
		pos, injury := v, ""
		for i := range v {
			if v[i] == ':' {
				pos, injury = v[:i], v[i+1:]
			}
		}
		ps[id] = sleeper.Player{FullName: id, Position: pos, InjuryStatus: injury}
	}
	return ps
}

// at returns the depth for one position, if listed.
func at(ps []PositionDepth, position string) (PositionDepth, bool) {
	for _, p := range ps {
		if p.Position == position {
			return p, true
		}
	}
	return PositionDepth{}, false
}

func TestDepthChart(t *testing.T) {
	standard := []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "K", "DEF", "BN", "BN"}
	players := depthPlayers(map[string]string{
		"qb1": "QB", "rb1": "RB", "rb2": "RB:Questionable", "rb3": "RB:Out",
		"wr1": "WR", "wr2": "WR", "wr3": "WR", "te1": "TE", "k1": "K", "def": "DEF",
		"rbir": "RB", "wrtaxi": "WR", "lb1": "LB", "qb2": "QB",
	})

	t.Run("standard: flex takes the spare WR", func(t *testing.T) {
		me := sleeper.Roster{
			Players: []string{"qb1", "rb1", "rb2", "rb3", "wr1", "wr2", "wr3", "te1", "k1", "def", "rbir", "wrtaxi", "lb1"},
			Reserve: []string{"rbir"}, Taxi: []string{"wrtaxi"},
		}
		pos, flex, _ := depthChart(standard, me, players)

		rb, _ := at(pos, "RB")
		wantRB := PositionDepth{Position: "RB", Slots: 2, Healthy: 2, Starting: 2, Backups: 0, Status: "thin",
			Questionable: []string{"rb2"}}
		rb.Unavailable = nil // order of the two unavailable players is not the point here
		if !reflect.DeepEqual(rb, wantRB) {
			t.Errorf("RB = %+v\nwant %+v", rb, wantRB)
		}
		if wr, _ := at(pos, "WR"); wr.Healthy != 3 || wr.Starting != 3 || wr.Backups != 0 || wr.Status != "thin" {
			t.Errorf("WR = %+v (3 healthy, 2 slots, 1 to FLEX: no backup)", wr)
		}
		if len(flex) != 1 || flex[0] != (FlexDepth{Slot: "FLEX", Slots: 1, Filled: 1, Status: "thin"}) {
			t.Errorf("flex = %+v", flex)
		}
		if _, ok := at(pos, "LB"); ok {
			t.Error("LB isn't started in this league and shouldn't be listed")
		}
		if qb, _ := at(pos, "QB"); qb.Status != "thin" {
			t.Errorf("QB = %+v, one QB for one slot is thin", qb)
		}
	})

	t.Run("short: not enough healthy players", func(t *testing.T) {
		me := sleeper.Roster{Players: []string{"qb1", "rb1", "rb3", "wr1", "wr2", "te1", "k1", "def"}}
		pos, flex, _ := depthChart(standard, me, players)
		rb, _ := at(pos, "RB")
		if rb.Status != "short" || rb.Healthy != 1 || len(rb.Unavailable) != 1 || rb.Unavailable[0] != "rb3 (Out)" {
			t.Errorf("RB = %+v", rb)
		}
		if flex[0].Status != "short" || flex[0].Filled != 0 {
			t.Errorf("flex = %+v", flex)
		}
	})

	t.Run("superflex without a QB slot", func(t *testing.T) {
		me := sleeper.Roster{Players: []string{"qb1", "qb2", "rb1", "wr1", "te1", "k1"}}
		pos, flex, _ := depthChart([]string{"RB", "WR", "TE", "SUPER_FLEX", "BN"}, me, players)
		qb, ok := at(pos, "QB")
		if !ok || qb.Slots != 0 || qb.Healthy != 2 || qb.Backups != 1 || qb.Status != "ok" {
			t.Errorf("QB = %+v, want listed (superflex-eligible) with one QB spare", qb)
		}
		// One QB spare is left for SUPER_FLEX after it's filled: ok, not thin.
		if len(flex) != 1 || flex[0] != (FlexDepth{Slot: "SUPER_FLEX", Slots: 1, Filled: 1, Status: "ok"}) {
			t.Errorf("flex = %+v", flex)
		}
		if _, ok := at(pos, "K"); ok {
			t.Error("no K slot: K shouldn't be listed")
		}
	})

	t.Run("most restrictive flex fills first", func(t *testing.T) {
		// One spare WR and one spare RB. WRRB_FLEX could take either; REC_FLEX
		// can only take the WR, so it must get it.
		me := sleeper.Roster{Players: []string{"rb1", "rb2", "wr1", "wr2"}}
		_, flex, _ := depthChart([]string{"RB", "WR", "WRRB_FLEX", "REC_FLEX"}, me, players)
		for _, f := range flex {
			if f.Filled != 1 {
				t.Errorf("%s filled %d of 1 (flex %+v)", f.Slot, f.Filled, flex)
			}
		}
	})
}

func TestDepth(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB", "RB", "BN"}},
			{LeagueID: "G", Name: "Guillotine", Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "X", Name: "Broken"},
		},
		"/league/A/rosters": []sleeper.Roster{{OwnerID: "100", Players: []string{"qb1", "qb2", "rb3"}}},
		"/league/G/rosters": []sleeper.Roster{{OwnerID: "100"}},
		"/players/nfl": map[string]sleeper.Player{
			"qb1": {FullName: "Qb One", Position: "QB"}, "qb2": {FullName: "Qb Two", Position: "QB"},
			"rb3": {FullName: "Rb Out", Position: "RB", InjuryStatus: "Out"},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")

	r, err := svc.Depth(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Alpha: RB short (0 healthy, 0 starting, 0 backups)"}; !reflect.DeepEqual(r.ThinSpots, want) {
		t.Errorf("thin spots = %q, want %q", r.ThinSpots, want)
	}
	if qb, _ := at(r.Leagues[0].Positions, "QB"); qb.Status != "ok" || qb.Backups != 1 {
		t.Errorf("QB = %+v", qb)
	}
	if r.Leagues[1].Note == "" || r.Leagues[2].Error == "" {
		t.Errorf("guillotine %+v / broken %+v", r.Leagues[1], r.Leagues[2])
	}
}

func TestThinSpotsSkipStreamedPositions(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 3},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"K", "DEF", "TE"}}},
		"/league/A/rosters":          []sleeper.Roster{{OwnerID: "100", Players: []string{"k1", "te1"}}},
		"/players/nfl": map[string]sleeper.Player{
			"k1": {FullName: "Kicker", Position: "K"}, "te1": {FullName: "Tight End", Position: "TE"},
		},
	}))
	r, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me").Depth(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// K with one kicker is thin but not worth reporting; DEF with none is
	// short and is; TE with one is thin and is.
	want := []string{"Alpha: TE thin (1 healthy, 1 starting, 0 backups)", "Alpha: DEF short (0 healthy, 0 starting, 0 backups)"}
	if !reflect.DeepEqual(r.ThinSpots, want) {
		t.Errorf("thin spots = %q\nwant %q", r.ThinSpots, want)
	}
}

func TestDepthChartKeepsUnknownPlayers(t *testing.T) {
	players := depthPlayers(map[string]string{"rb1": "RB"})
	me := sleeper.Roster{Players: []string{"rb1", "brand-new-signing"}}
	pos, _, unresolved := depthChart([]string{"RB", "RB"}, me, players)
	if len(unresolved) != 1 || unresolved[0] != "brand-new-signing" {
		t.Errorf("unresolved = %v, want the unknown ID listed, not dropped", unresolved)
	}
	if rb, _ := at(pos, "RB"); rb.Healthy != 1 || rb.Status != "short" {
		t.Errorf("RB = %+v", rb)
	}

	// A league that starts nothing we track still gets a list, not null.
	if pos, _, _ := depthChart([]string{"DL", "LB"}, me, players); pos == nil || len(pos) != 0 {
		t.Errorf("IDP-only league: %#v, want []", pos)
	}
}
