package league

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
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
		"/projections/nfl/regular/2026/3": map[string]map[string]float64{"k1": {"pts_std": 9}, "qb2": {"pts_std": 15}},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")
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

	t.Run("with values and projections", func(t *testing.T) {
		valued := NewService(api, NewDirectory(store.NewMemory(), api.Players), func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
			return map[string]fantasycalc.Value{"k1": {Value: 400}}, nil
		}, "me")
		valued.projections.min = 1 // a two-player feed counts as complete here
		r, err := valued.WaiverTargets(ctx, "", "", 5)
		if err != nil {
			t.Fatal(err)
		}
		f, rl := r.Leagues[0], r.Leagues[2]
		// FAAB: 750 left; Qb Two has no value, so no bid.
		if q := f.Targets[0]; q.Projected != 15 || q.Bid != nil || f.Note != "" {
			t.Errorf("FAAB = %+v, note %q", f.Targets, f.Note)
		}
		// Rolling: Kick One's value puts him above the more-added Qb Two; no bids.
		if rl.Targets[0].Name != "Kick One" || rl.Targets[0].Value != 400 || rl.Targets[0].Bid != nil {
			t.Errorf("rolling = %+v", rl.Targets)
		}
	})

	t.Run("unknown position", func(t *testing.T) {
		if _, err := svc.WaiverTargets(ctx, "LB", "", 5); err == nil || !strings.Contains(err.Error(), "QB, RB") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRankTargets(t *testing.T) {
	cands := []Target{ // as targets() returns them: by adds, then Sleeper rank
		{PlayerID: "hot", Name: "Hot Pickup", Adds: 5000},
		{PlayerID: "stash", Name: "Dynasty Stash", Adds: 10},
		{PlayerID: "stream", Name: "Streamer"},
		{PlayerID: "nobody", Name: "Nobody"},
	}
	market := map[string]fantasycalc.Value{"hot": {Value: 1200}, "stash": {Value: 2500}}
	proj := map[string]map[string]float64{"hot": {"pts_ppr": 14}, "stream": {"pts_ppr": 9}, "stash": {"pts_ppr": 2}}
	left := 80
	got := rankTargets(cands, market, proj, "pts_ppr", Waivers{Type: "faab", FAABRemaining: &left}, "dynasty", 3)

	var names []string
	for _, tg := range got {
		names = append(names, tg.Name)
	}
	// Value first, then projection; Nobody (no value, no projection) is cut by the limit.
	if strings.Join(names, ",") != "Dynasty Stash,Hot Pickup,Streamer" {
		t.Fatalf("order = %v", names)
	}
	// Stash: 25% of 80 = 20. Hot: 12% + 5 (trending) = 17% of 80 = 13. Streamer: no value, no bid.
	if *got[0].Bid != 20 || *got[1].Bid != 13 || got[2].Bid != nil || got[1].Projected != 14 || got[0].Value != 2500 {
		t.Errorf("got %+v", got)
	}

	if got := rankTargets([]Target{{PlayerID: "hot"}}, market, proj, "pts_ppr", Waivers{Type: "rolling"}, "dynasty", 5); got[0].Bid != nil {
		t.Errorf("no FAAB, no bid: %+v", got[0])
	}
	if got := rankTargets([]Target{{Name: "B"}, {Name: "A"}}, nil, nil, "pts_ppr", Waivers{}, "redraft", 5); got[0].Name != "B" {
		t.Errorf("without values or projections the incoming order stands: %+v", got)
	}
	survive := rankTargets([]Target{{PlayerID: "stash"}, {PlayerID: "hot"}, {PlayerID: "stream"}}, market, proj, "pts_ppr", Waivers{}, "guillotine", 3)
	if survive[0].PlayerID != "hot" || survive[1].PlayerID != "stream" { // guillotine: this week's points first
		t.Errorf("guillotine order = %+v", survive)
	}
	hurt := []Target{{PlayerID: "stash", Injury: "IR"}, {PlayerID: "hot"}}
	if got := rankTargets(slices.Clone(hurt), market, proj, "pts_ppr", Waivers{}, "redraft", 2); got[0].PlayerID != "hot" {
		t.Errorf("redraft: the IR player should go last: %+v", got)
	}
	if got := rankTargets(slices.Clone(hurt), market, proj, "pts_ppr", Waivers{}, "dynasty", 2); got[0].PlayerID != "stash" {
		t.Errorf("dynasty: an IR stash keeps his value rank: %+v", got)
	}
}

func TestFAABBid(t *testing.T) {
	for _, tt := range []struct {
		value, adds, left, want int
	}{
		{1000, 0, 100, 10},
		{9000, 0, 100, 50},    // capped at half
		{9000, 2000, 100, 50}, // the trending bump, then the cap
		{300, 0, 7, 1},        // 3% of 7 rounds to 0: floor of 1
		{2000, 0, 0, 0},       // nothing left
	} {
		if got := faabBid(Target{Value: tt.value, Adds: tt.adds}, tt.left); got != tt.want {
			t.Errorf("faabBid(value %d, adds %d, left %d) = %d, want %d", tt.value, tt.adds, tt.left, got, tt.want)
		}
	}
}
