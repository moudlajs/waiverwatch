package league

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func user(id, display, team string) sleeper.LeagueUser {
	u := sleeper.LeagueUser{UserID: id, DisplayName: display}
	u.Metadata.TeamName = team
	return u
}

func TestFindOwner(t *testing.T) {
	rosters := []sleeper.Roster{
		{RosterID: 1, OwnerID: "a"},
		{RosterID: 2, OwnerID: "b", CoOwners: []string{"c"}},
		{RosterID: 3, OwnerID: "d"},
	}
	users := []sleeper.LeagueUser{
		user("a", "alice", "Waiver Wizards"),
		user("b", "bob", ""),
		user("c", "carol", ""),
		user("d", "bobby", "Bob's Burgers"),
	}
	tests := []struct {
		query    string
		want     int
		wantErr  string
		notFound bool
	}{
		{query: "Waiver Wizards", want: 1},
		{query: "wizards", want: 1}, // partial team name
		{query: "ALICE", want: 1},   // display name, any case
		{query: "carol", want: 2},   // co-owner
		{query: "bob", want: 2},     // exact beats the partial matches "bobby" and "Bob's Burgers"
		{query: "bur", want: 3},     // single partial
		{query: "o", wantErr: "more than one"},
		{query: "zed", wantErr: "Waiver Wizards", notFound: true}, // error lists the teams
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r, err := FindOwner(rosters, users, tt.query)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				var nf notFoundError
				if isNF := errors.As(err, &nf); isNF != tt.notFound {
					t.Errorf("notFoundError = %v, want %v", isNF, tt.notFound)
				}
				return
			}
			if err != nil || r.RosterID != tt.want {
				t.Errorf("got roster %d, err %v; want %d", r.RosterID, err, tt.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	players := map[string]sleeper.Player{
		"qb": {FullName: "Q B", Position: "QB", Team: "KC"},
		"rb": {FullName: "R B", Position: "RB", Team: "SF", InjuryStatus: "Out", InjuryBodyPart: "Knee"},
		"wr": {FullName: "W R", Position: "WR", Team: "MIN"},
		"ir": {FullName: "I R", Position: "TE", Team: "KC", InjuryStatus: "IR"},
		"tx": {FullName: "T X", Position: "WR", Team: "NYJ"},
	}
	r := sleeper.Roster{
		Players:  []string{"qb", "rb", "wr", "ir", "tx"},
		Starters: []string{"qb", "0", "rb"},
		Reserve:  []string{"ir"},
		Taxi:     []string{"tx"},
	}
	starters, bench, ir, taxi := split(r, []string{"QB", "WR", "FLEX", "BN"}, players)

	want := []RosterPlayer{
		{Slot: "QB", PlayerID: "qb", Name: "Q B", Position: "QB", NFLTeam: "KC"},
		{Slot: "WR", Name: "(empty)"},
		{Slot: "FLEX", PlayerID: "rb", Name: "R B", Position: "RB", NFLTeam: "SF", Injury: "Out", InjuryPart: "Knee"},
	}
	if !reflect.DeepEqual(starters, want) {
		t.Errorf("starters:\n got %+v\nwant %+v", starters, want)
	}
	if len(bench) != 1 || bench[0].Name != "W R" || bench[0].Slot != "" {
		t.Errorf("bench = %+v", bench)
	}
	if len(ir) != 1 || ir[0].Injury != "IR" || len(taxi) != 1 || taxi[0].Name != "T X" {
		t.Errorf("ir = %+v, taxi = %+v", ir, taxi)
	}
}

func TestRosters(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha", RosterPositions: []string{"QB", "BN"}},
			{LeagueID: "B", Name: "Beta", RosterPositions: []string{"QB", "BN"}},
		},
		"/league/A/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"qb", "wr"}, Starters: []string{"qb"}},
			{OwnerID: "200", Players: []string{"rb"}, Starters: []string{"rb"}},
		},
		"/league/A/users":   []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival FC")},
		"/league/B/rosters": []sleeper.Roster{{OwnerID: "100", Players: []string{"wr"}}},
		"/league/B/users":   []sleeper.LeagueUser{user("100", "me", "")},
		"/players/nfl": map[string]sleeper.Player{
			"qb": {FullName: "Q B", Position: "QB"}, "wr": {FullName: "W R", Position: "WR"}, "rb": {FullName: "R B", Position: "RB"},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")
	ctx := context.Background()

	t.Run("mine everywhere", func(t *testing.T) {
		r, err := svc.Rosters(ctx, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rosters) != 2 || r.Rosters[0].Team != "Mine" || r.Rosters[0].Starters[0].Name != "Q B" || r.Rosters[0].Bench[0].Name != "W R" {
			t.Errorf("got %+v", r.Rosters)
		}
		if r.Rosters[1].Team != "me" { // no team name: display name
			t.Errorf("Beta team = %q", r.Rosters[1].Team)
		}
	})

	t.Run("an owner across leagues skips leagues without them", func(t *testing.T) {
		r, err := svc.Rosters(ctx, "", "rival")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rosters) != 1 || r.Rosters[0].League != "Alpha" || r.Rosters[0].Owner != "rival" || r.Rosters[0].Starters[0].Name != "R B" {
			t.Errorf("got %+v", r.Rosters)
		}
	})

	t.Run("an owner missing from a named league is an error there", func(t *testing.T) {
		r, err := svc.Rosters(ctx, "beta", "rival")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rosters) != 1 || !strings.Contains(r.Rosters[0].Error, "no team matches") {
			t.Errorf("got %+v", r.Rosters)
		}
	})

	t.Run("an owner in no league", func(t *testing.T) {
		if _, err := svc.Rosters(ctx, "", "nobody"); err == nil {
			t.Error("want an error")
		}
	})
}
