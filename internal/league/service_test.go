package league

import (
	"context"
	"errors"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
)

func record(w, l int, pts float64) sleeper.RosterSettings {
	return sleeper.RosterSettings{Wins: w, Losses: l, Fpts: int(pts), FptsDecimal: int((pts - float64(int(pts))) * 100)}
}

func TestStanding(t *testing.T) {
	rosters := []sleeper.Roster{
		{RosterID: 1, Settings: record(2, 0, 200)},
		{RosterID: 2, Settings: record(1, 1, 300)},
		{RosterID: 3, Settings: record(1, 1, 250.5)},
		{RosterID: 4, Settings: record(1, 0, 150)}, // bye week: fewer losses ranks higher
		{RosterID: 5, Settings: record(0, 2, 250.5)},
	}
	tests := []struct {
		name     string
		rosterID int
		byPoints bool
		want     int
	}{
		{"most wins is first", 1, false, 1},
		{"fewer losses breaks a wins tie", 4, false, 2},
		{"points break a record tie", 2, false, 3},
		{"lower points in a record tie", 3, false, 4},
		{"last", 5, false, 5},
		{"guillotine ranks by points", 2, true, 1},
		{"guillotine ties share a rank", 3, true, 2},
		{"guillotine ties share a rank (other team)", 5, true, 2},
		{"guillotine ignores wins", 1, true, 4},
		{"unknown roster", 99, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Standing(rosters, tt.rosterID, tt.byPoints); got != tt.want {
				t.Errorf("Standing(%d, byPoints=%v) = %d, want %d", tt.rosterID, tt.byPoints, got, tt.want)
			}
		})
	}
}

func TestMyRoster(t *testing.T) {
	rosters := []sleeper.Roster{
		{RosterID: 1, OwnerID: "a"},
		{RosterID: 2, OwnerID: "b", CoOwners: []string{"me"}},
	}
	tests := []struct {
		user   string
		wantID int
		wantOK bool
	}{
		{"a", 1, true},
		{"me", 2, true}, // co-owner
		{"nobody", 0, false},
	}
	for _, tt := range tests {
		r, ok := MyRoster(rosters, tt.user)
		if ok != tt.wantOK || r.RosterID != tt.wantID {
			t.Errorf("MyRoster(%q) = %d, %v; want %d, %v", tt.user, r.RosterID, ok, tt.wantID, tt.wantOK)
		}
	}
}

func TestTeamName(t *testing.T) {
	named := sleeper.LeagueUser{UserID: "a", DisplayName: "alice"}
	named.Metadata.TeamName = "Waiver Wizards"
	plain := sleeper.LeagueUser{UserID: "b", DisplayName: "bob"}
	users := []sleeper.LeagueUser{named, plain}

	for id, want := range map[string]string{"a": "Waiver Wizards", "b": "bob", "zed": ""} {
		if got := TeamName(users, id); got != want {
			t.Errorf("TeamName(%q) = %q, want %q", id, got, want)
		}
	}
}

// fakeSleeper serves a user "me" (ID 100) with a dynasty league L1 that
// loads and a guillotine league L2 whose rosters are missing.
func fakeSleeper(t *testing.T) *sleeper.Client {
	t.Helper()
	me := sleeper.LeagueUser{UserID: "100", DisplayName: "me"}
	me.Metadata.TeamName = "My Team"
	return sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":  sleeper.State{Season: "2026", SeasonType: "regular", Week: 3},
		"/user/me":    sleeper.User{UserID: "100", DisplayName: "me"},
		"/user/ghost": nil,
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "L1", Name: "Dynasty", Status: "in_season", TotalRosters: 2, Settings: sleeper.LeagueSettings{Type: 2}},
			{LeagueID: "L2", Name: "Survival", Status: "in_season", TotalRosters: 18, Settings: sleeper.LeagueSettings{Type: 3}},
		},
		"/league/L1/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "200", Settings: record(2, 0, 250)},
			{RosterID: 2, OwnerID: "100", Settings: sleeper.RosterSettings{Wins: 1, Losses: 1, Fpts: 210, FptsDecimal: 44, FptsAgainst: 199, FptsAgainstDecimal: 5}},
		},
		"/league/L1/users": []sleeper.LeagueUser{me, {UserID: "200", DisplayName: "rival"}},
		"/league/L2/users": []sleeper.LeagueUser{me},
	}))
}

func TestOverview(t *testing.T) {
	svc := NewService(fakeSleeper(t), "me")
	ov, err := svc.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ov.Season != "2026" || ov.Week != 3 || len(ov.Leagues) != 2 {
		t.Fatalf("got season %q week %d with %d leagues", ov.Season, ov.Week, len(ov.Leagues))
	}

	want := Summary{
		LeagueID: "L1", Name: "Dynasty", Kind: "dynasty", Status: "in_season", Teams: 2,
		MyTeam: "My Team", Wins: 1, Losses: 1, PointsFor: 210.44, PointsAgainst: 199.05, Standing: 2,
	}
	if got := ov.Leagues[0]; got != want {
		t.Errorf("L1:\n got %+v\nwant %+v", got, want)
	}

	// L2's rosters 404: the league reports its error, the answer survives.
	if l2 := ov.Leagues[1]; l2.LeagueID != "L2" || l2.Kind != "guillotine" || l2.Error == "" || l2.Standing != 0 {
		t.Errorf("L2 = %+v, want an error and no standing", l2)
	}
}

func TestOverviewUnknownUser(t *testing.T) {
	_, err := NewService(fakeSleeper(t), "ghost").Overview(context.Background())
	if !errors.Is(err, sleeper.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
