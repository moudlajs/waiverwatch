package league

import (
	"context"
	"reflect"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func pick(round, no int, playerID, by string, roster int) sleeper.Pick {
	return sleeper.Pick{Round: round, PickNo: no, PlayerID: playerID, PickedBy: by, RosterID: roster}
}

func TestMyPicks(t *testing.T) {
	players := map[string]sleeper.Player{
		"walker": {FullName: "Kenneth Walker", Position: "RB", Team: "KC"},
		"other":  {FullName: "Someone Else", Position: "WR", Team: "SF"},
	}
	retired := pick(3, 30, "gone", "me", 1)
	retired.Metadata.FirstName, retired.Metadata.LastName, retired.Metadata.Position = "Old", "Timer", "TE"
	auction := pick(0, 5, "other", "me", 1)
	auction.Metadata.Amount = "42"
	keeper := pick(1, 2, "walker", "me", 1)
	keeper.IsKeeper = true

	picks := []sleeper.Pick{
		pick(1, 1, "walker", "me", 1),
		pick(1, 3, "other", "rival", 2),
		pick(2, 14, "other", "", 1),  // auto-pick for my roster
		pick(2, 15, "walker", "", 2), // auto-pick for someone else
		retired, auction, keeper,
	}
	onRoster := map[string]bool{"walker": true}

	got := myPicks(picks, "me", 1, onRoster, players)
	want := []MyPick{
		{Round: 1, PickNo: 1, PlayerID: "walker", Name: "Kenneth Walker", Position: "RB", NFLTeam: "KC", StillMine: true},
		{Round: 2, PickNo: 14, PlayerID: "other", Name: "Someone Else", Position: "WR", NFLTeam: "SF"},
		{Round: 3, PickNo: 30, PlayerID: "gone", Name: "Old Timer", Position: "TE"},
		{PickNo: 5, PlayerID: "other", Name: "Someone Else", Position: "WR", NFLTeam: "SF", Cost: 42},
		{Round: 1, PickNo: 2, PlayerID: "walker", Name: "Kenneth Walker", Position: "RB", NFLTeam: "KC", Keeper: true, StillMine: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}

	// In an earlier season's league roster IDs mean nothing: no guessing.
	if got := myPicks(picks, "me", 0, onRoster, players); len(got) != 4 {
		t.Errorf("without a roster ID: %d picks, want the 4 made by me", len(got))
	}
}

func TestFilterPicks(t *testing.T) {
	ld := DraftHistory{Drafts: []DraftSummary{
		{Season: "2026", Picks: []MyPick{{Name: "Kenneth Walker"}, {Name: "Josh Allen"}}},
		{Season: "2025", Picks: []MyPick{{Name: "Josh Allen"}}},
	}}
	if got := filterPicks(ld, ""); !reflect.DeepEqual(got, ld) {
		t.Errorf("empty filter changed the drafts: %+v", got)
	}
	got := filterPicks(ld, "  walker ")
	if len(got.Drafts) != 1 || got.Drafts[0].Season != "2026" || len(got.Drafts[0].Picks) != 1 {
		t.Errorf("walker: %+v", got.Drafts)
	}
	if got := filterPicks(ld, "nobody"); got.Drafts == nil || len(got.Drafts) != 0 {
		t.Errorf("no match should leave an empty list, got %#v", got.Drafts)
	}
}

func TestDrafts(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "DYN", Name: "Dynasty", PreviousID: "DYN25", Settings: sleeper.LeagueSettings{Type: 2}},
			{LeagueID: "RED", Name: "Redraft", PreviousID: "RED25"},
			{LeagueID: "BAD", Name: "Broken"},
		},
		// Dynasty: 2026 rookie draft here, startup in last season's league.
		"/league/DYN/rosters":  []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"rookie"}}},
		"/league/DYN/drafts":   []sleeper.Draft{{DraftID: "rookies", Season: "2026", Type: "linear", Status: "complete"}},
		"/draft/rookies/picks": []sleeper.Pick{pick(1, 4, "rookie", "100", 1)},
		"/league/DYN25":        sleeper.League{LeagueID: "DYN25", Settings: sleeper.LeagueSettings{Type: 2}},
		"/league/DYN25/drafts": []sleeper.Draft{{DraftID: "startup", Season: "2025", Type: "snake", Status: "complete"}},
		"/draft/startup/picks": []sleeper.Pick{pick(1, 7, "walker", "100", 9), pick(1, 8, "star", "200", 3)},
		// Redraft: last season's league must not be followed.
		"/league/RED/rosters": []sleeper.Roster{{RosterID: 2, OwnerID: "100", Players: []string{"walker"}}},
		"/league/RED/drafts":  []sleeper.Draft{{DraftID: "red26", Season: "2026", Type: "snake", Status: "complete"}},
		"/draft/red26/picks":  []sleeper.Pick{pick(2, 20, "walker", "100", 2)},
		"/league/BAD/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/players/nfl": map[string]sleeper.Player{
			"walker": {FullName: "Kenneth Walker", Position: "RB"},
			"rookie": {FullName: "Rookie Guy", Position: "WR"},
			"star":   {FullName: "Star Player", Position: "QB"},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")
	ctx := context.Background()

	t.Run("all my picks", func(t *testing.T) {
		r, err := svc.Drafts(ctx, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if r.LeaguesSearched != 3 || r.MyPicks != 3 || len(r.Leagues) != 3 {
			t.Fatalf("report %+v", r)
		}
		dyn := r.Leagues[0]
		if len(dyn.Drafts) != 2 || dyn.Drafts[0].Season != "2026" || dyn.Drafts[1].Season != "2025" {
			t.Fatalf("dynasty drafts %+v", dyn.Drafts)
		}
		startup := dyn.Drafts[1].Picks
		if len(startup) != 1 || startup[0].Name != "Kenneth Walker" || startup[0].StillMine {
			t.Errorf("startup picks %+v (drafted then gone)", startup)
		}
		if red := r.Leagues[1]; len(red.Drafts) != 1 || !red.Drafts[0].Picks[0].StillMine {
			t.Errorf("redraft %+v (only this season, still mine)", red.Drafts)
		}
		if bad := r.Leagues[2]; bad.Error == "" {
			t.Errorf("broken league %+v, want an error", bad)
		}
	})

	t.Run("where did I draft Walker", func(t *testing.T) {
		r, err := svc.Drafts(ctx, "", "walker")
		if err != nil {
			t.Fatal(err)
		}
		// Two leagues with Walker, plus the broken one, whose answer is unknown.
		if r.Player != "walker" || r.MyPicks != 2 || len(r.Leagues) != 3 {
			t.Fatalf("report %+v", r)
		}
		if r.Leagues[0].League != "Dynasty" || r.Leagues[1].League != "Redraft" || r.Leagues[2].Error == "" {
			t.Errorf("leagues %+v", r.Leagues)
		}
	})
}
