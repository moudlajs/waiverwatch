package league

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestEligiblePositions(t *testing.T) {
	tests := []struct {
		name  string
		slots []string
		want  []string
	}{
		{"standard", []string{"QB", "RB", "RB", "WR", "TE", "FLEX", "K", "DEF", "BN"}, []string{"QB", "RB", "WR", "TE", "K", "DEF"}},
		{"superflex, no K or DEF", []string{"RB", "WR", "TE", "SUPER_FLEX", "BN"}, []string{"QB", "RB", "WR", "TE"}},
		{"WR/RB flex only adds nothing new", []string{"QB", "WRRB_FLEX"}, []string{"QB", "WR", "RB"}},
		{"receiver flex", []string{"REC_FLEX"}, []string{"WR", "TE"}},
		{"IDP slots are ignored", []string{"QB", "DL", "LB", "IDP_FLEX"}, []string{"QB"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := map[string]bool{}
			for _, p := range tt.want {
				want[p] = true
			}
			if got := EligiblePositions(tt.slots); !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestMatchLeagues(t *testing.T) {
	leagues := []sleeper.League{{LeagueID: "1", Name: "Dynasty 2025"}, {LeagueID: "2", Name: "Crazy Dynasty"}, {LeagueID: "3", Name: "Survival"}}
	tests := []struct {
		query   string
		want    []string
		wantErr bool
	}{
		{"", []string{"1", "2", "3"}, false},
		{"dynasty", []string{"1", "2"}, false},
		{"SURV", []string{"3"}, false},
		{"3", []string{"3"}, false}, // by league ID
		{"bowling", nil, true},
	}
	for _, tt := range tests {
		got, err := matchLeagues(leagues, tt.query)
		if (err != nil) != tt.wantErr {
			t.Fatalf("matchLeagues(%q) err = %v", tt.query, err)
		}
		if tt.wantErr {
			if !strings.Contains(err.Error(), "Crazy Dynasty") {
				t.Errorf("error should list the leagues: %v", err)
			}
			continue
		}
		var ids []string
		for _, l := range got {
			ids = append(ids, l.LeagueID)
		}
		if !reflect.DeepEqual(ids, tt.want) {
			t.Errorf("matchLeagues(%q) = %v, want %v", tt.query, ids, tt.want)
		}
	}
}

func TestWaivers(t *testing.T) {
	me := sleeper.Roster{Settings: sleeper.RosterSettings{WaiverPosition: 4, WaiverBudgetUsed: 37}}
	faab := waivers(sleeper.League{Settings: sleeper.LeagueSettings{WaiverType: 2, WaiverBudget: 100}}, me)
	if faab.Type != "faab" || faab.FAABRemaining == nil || *faab.FAABRemaining != 63 || faab.FAABBudget != 100 || faab.Priority != 0 {
		t.Errorf("faab = %+v", faab)
	}
	if got := waivers(sleeper.League{}, me); got != (Waivers{Type: "rolling", Priority: 4}) {
		t.Errorf("rolling = %+v", got)
	}
	if got := waivers(sleeper.League{Settings: sleeper.LeagueSettings{WaiverType: 1}}, me); got.Type != "reverse_standings" {
		t.Errorf("reverse = %+v", got)
	}
}

func TestTargets(t *testing.T) {
	players := map[string]sleeper.Player{
		"hot":      {FullName: "Hot Pickup", Position: "RB", Team: "SEA", Active: true, SearchRank: 300},
		"ranked":   {FullName: "Good Rank", Position: "RB", Team: "KC", Active: true, SearchRank: 50},
		"unranked": {FullName: "Aaron Unranked", Position: "RB", Team: "KC", Active: true},
		"worse":    {FullName: "Worse Rank", Position: "WR", Team: "KC", Active: true, SearchRank: 80},
		"taken":    {FullName: "Taken", Position: "RB", Team: "KC", Active: true, SearchRank: 1},
		"retired":  {FullName: "Retired", Position: "RB", Team: "KC", SearchRank: 2},
		"nfl fa":   {FullName: "No Team", Position: "RB", Active: true, SearchRank: 3},
		"kicker":   {FullName: "Kicker", Position: "K", Team: "KC", Active: true, SearchRank: 4},
	}
	rostered := map[string]bool{"taken": true}
	eligible := map[string]bool{"RB": true, "WR": true}
	adds := map[string]int{"hot": 5000}

	var got []string
	for _, tg := range targets(players, rostered, eligible, adds, 10) {
		got = append(got, tg.Name)
	}
	want := []string{"Hot Pickup", "Good Rank", "Worse Rank", "Aaron Unranked"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := len(targets(players, rostered, eligible, adds, 2)); n != 2 {
		t.Errorf("limit 2 gave %d", n)
	}
	if got := targets(players, rostered, map[string]bool{"TE": true}, adds, 5); got == nil || len(got) != 0 {
		t.Errorf("no candidates should be an empty list, got %#v", got)
	}
}

func TestWaiverTargets(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "F", Name: "FAAB Superflex", RosterPositions: []string{"SUPER_FLEX", "BN"},
				Settings: sleeper.LeagueSettings{WaiverType: 2, WaiverBudget: 1000}},
			{LeagueID: "G", Name: "Guillotine", RosterPositions: []string{"K"}, Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "R", Name: "Rolling", RosterPositions: []string{"QB", "K"}},
		},
		"/league/F/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"qb1"}, Settings: sleeper.RosterSettings{WaiverBudgetUsed: 250}},
		},
		"/league/G/rosters":         []sleeper.Roster{{OwnerID: "100"}}, // eliminated: empty roster
		"/league/R/rosters":         []sleeper.Roster{{OwnerID: "100", Settings: sleeper.RosterSettings{WaiverPosition: 2}}},
		"/players/nfl/trending/add": []sleeper.Trending{{PlayerID: "qb2", Count: 10}},
		"/players/nfl": map[string]sleeper.Player{
			"qb1": {FullName: "Qb One", Position: "QB", Team: "KC", Active: true, SearchRank: 1},
			"qb2": {FullName: "Qb Two", Position: "QB", Team: "NO", Active: true, SearchRank: 90},
			"k1":  {FullName: "Kick One", Position: "K", Team: "TB", Active: true, SearchRank: 150},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")
	ctx := context.Background()

	t.Run("all positions, all leagues", func(t *testing.T) {
		r, err := svc.WaiverTargets(ctx, "", "", 5)
		if err != nil {
			t.Fatal(err)
		}
		f, g, rl := r.Leagues[0], r.Leagues[1], r.Leagues[2]
		if len(f.Targets) != 1 || f.Targets[0].Name != "Qb Two" || f.Targets[0].Adds != 10 {
			t.Errorf("FAAB superflex targets = %+v (qb1 is mine, K has no slot)", f.Targets)
		}
		if f.Waivers.Type != "faab" || *f.Waivers.FAABRemaining != 750 {
			t.Errorf("FAAB waivers = %+v", f.Waivers)
		}
		if g.Note == "" || len(g.Targets) != 0 {
			t.Errorf("eliminated guillotine = %+v", g)
		}
		if rl.Waivers.Priority != 2 || len(rl.Targets) != 3 {
			t.Errorf("rolling = %+v", rl)
		}
	})

	t.Run("position with no slot in a league", func(t *testing.T) {
		r, err := svc.WaiverTargets(ctx, "K", "", 5)
		if err != nil {
			t.Fatal(err)
		}
		if f := r.Leagues[0]; !strings.Contains(f.Note, "no lineup slot for K") || len(f.Targets) != 0 {
			t.Errorf("FAAB superflex = %+v", f)
		}
		if rl := r.Leagues[2]; len(rl.Targets) != 1 || rl.Targets[0].Name != "Kick One" {
			t.Errorf("rolling kickers = %+v", rl.Targets)
		}
	})

	t.Run("one league by name", func(t *testing.T) {
		r, err := svc.WaiverTargets(ctx, "QB", "rolling", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Leagues) != 1 || r.Leagues[0].Name != "Rolling" {
			t.Errorf("leagues = %+v", r.Leagues)
		}
	})

	t.Run("unknown position", func(t *testing.T) {
		if _, err := svc.WaiverTargets(ctx, "LB", "", 5); err == nil || !strings.Contains(err.Error(), "QB, RB") {
			t.Errorf("err = %v", err)
		}
	})
}
