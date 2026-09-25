package league

import (
	"context"
	"reflect"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestRostered(t *testing.T) {
	got := Rostered([]sleeper.Roster{
		{Players: []string{"1", "2"}, Taxi: []string{"3"}},
		{Players: []string{"4"}, Reserve: []string{"5"}},
		{}, // eliminated guillotine team
	})
	want := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// trendingService serves three leagues: A has RB "10" on another team,
// B has it on mine, C fails to load. Trending: RB 10, WR 20, RB 30.
func trendingService(t *testing.T) *Service {
	t.Helper()
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "League A"},
			{LeagueID: "B", Name: "League B"},
			{LeagueID: "C", Name: "League C"},
		},
		"/league/A/rosters": []sleeper.Roster{{OwnerID: "100"}, {OwnerID: "200", Players: []string{"10"}}},
		"/league/B/rosters": []sleeper.Roster{{OwnerID: "100", Taxi: []string{"10"}}},
		"/players/nfl/trending/add": []sleeper.Trending{
			{PlayerID: "10", Count: 900}, {PlayerID: "20", Count: 500}, {PlayerID: "30", Count: 100},
		},
		"/players/nfl": map[string]sleeper.Player{
			"10": {PlayerID: "10", FullName: "Rb One", Position: "RB", Team: "KC", InjuryStatus: "Questionable"},
			"20": {PlayerID: "20", FullName: "Wr Two", Position: "WR", Team: "SEA"},
			"30": {PlayerID: "30", FullName: "Rb Three", Position: "RB", Team: "BUF"},
		},
	}))
	return NewService(api, NewDirectory(store.NewMemory(), api.Players), "me")
}

func TestTrending(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		limit    int
		position string
		want     []TrendingPlayer
	}{
		{
			name: "all positions", limit: 10,
			want: []TrendingPlayer{
				{PlayerID: "10", Name: "Rb One", Position: "RB", NFLTeam: "KC", Injury: "Questionable", Adds: 900,
					AvailableIn: []string{}, OnMyRosterIn: []string{"League B"}},
				{PlayerID: "20", Name: "Wr Two", Position: "WR", NFLTeam: "SEA", Adds: 500,
					AvailableIn: []string{"League A", "League B"}},
				{PlayerID: "30", Name: "Rb Three", Position: "RB", NFLTeam: "BUF", Adds: 100,
					AvailableIn: []string{"League A", "League B"}},
			},
		},
		{
			name: "limit", limit: 1,
			want: []TrendingPlayer{
				{PlayerID: "10", Name: "Rb One", Position: "RB", NFLTeam: "KC", Injury: "Questionable", Adds: 900,
					AvailableIn: []string{}, OnMyRosterIn: []string{"League B"}},
			},
		},
		{
			name: "position filter skips the WR", limit: 2, position: "RB",
			want: []TrendingPlayer{
				{PlayerID: "10", Name: "Rb One", Position: "RB", NFLTeam: "KC", Injury: "Questionable", Adds: 900,
					AvailableIn: []string{}, OnMyRosterIn: []string{"League B"}},
				{PlayerID: "30", Name: "Rb Three", Position: "RB", NFLTeam: "BUF", Adds: 100,
					AvailableIn: []string{"League A", "League B"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := trendingService(t).Trending(ctx, 24, tt.limit, tt.position)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.Players, tt.want) {
				t.Errorf("players:\n got %+v\nwant %+v", r.Players, tt.want)
			}
			if len(r.FailedLeagues) != 1 || r.FailedLeagues[0].League != "League C" {
				t.Errorf("failed leagues = %+v, want League C", r.FailedLeagues)
			}
		})
	}
}
