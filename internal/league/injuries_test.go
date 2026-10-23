package league

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestSeverityRank(t *testing.T) {
	order := []string{"Out", "IR", "Doubtful", "Questionable", "COV"}
	for i := 1; i < len(order); i++ {
		if severityRank(order[i-1]) >= severityRank(order[i]) {
			t.Errorf("%s should rank before %s", order[i-1], order[i])
		}
	}
}

func TestIREligible(t *testing.T) {
	outOnly := sleeper.LeagueSettings{ReserveAllowOut: 1}
	all := sleeper.LeagueSettings{ReserveAllowOut: 1, ReserveAllowDoubtful: 1, ReserveAllowSus: 1, ReserveAllowNA: 1, ReserveAllowCOV: 1, ReserveAllowDNR: 1}
	tests := []struct {
		settings sleeper.LeagueSettings
		status   string
		want     bool
	}{
		{sleeper.LeagueSettings{}, "IR", true},
		{sleeper.LeagueSettings{}, "Out", false}, // Dynasty 2025 allows IR only
		{outOnly, "Out", true},
		{outOnly, "Doubtful", false},
		{outOnly, "Questionable", false},
		{outOnly, "", false},
		{all, "Sus", true},
		{all, "Questionable", false}, // Sleeper has no setting for it
	}
	for _, tt := range tests {
		if got := irEligible(tt.settings, tt.status); got != tt.want {
			t.Errorf("irEligible(%+v, %q) = %v, want %v", tt.settings, tt.status, got, tt.want)
		}
	}
}

func TestInjuriesFlagIllegalIR(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "T", Name: "TGIT", Settings: sleeper.LeagueSettings{ReserveAllowOut: 1}},
		},
		"/league/T/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"out", "q", "back"}, Reserve: []string{"out", "q", "back"}},
		},
		"/players/nfl/trending/add": []sleeper.Trending{},
		"/players/nfl": map[string]sleeper.Player{
			"out":  {FullName: "Still Out", Position: "RB", Team: "CHI", Active: true, InjuryStatus: "Out"},
			"q":    {FullName: "Now Questionable", Position: "WR", Team: "MIN", Active: true, InjuryStatus: "Questionable"},
			"back": {FullName: "Fully Back", Position: "TE", Team: "KC", Active: true},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")
	r, err := svc.Injuries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names, statuses []string
	for _, p := range r.Players {
		names, statuses = append(names, p.Name), append(statuses, p.Status)
	}
	// Illegal IR blocks roster moves, so it comes first; a healthy player on IR is listed too.
	if !slices.Equal(names, []string{"Now Questionable", "Fully Back", "Still Out"}) {
		t.Fatalf("players = %v", names)
	}
	for i, want := range []string{"Questionable; illegal IR in TGIT", "healthy; illegal IR in TGIT", "Out"} {
		if !strings.HasPrefix(statuses[i], want) {
			t.Errorf("%s status = %q, want prefix %q", names[i], statuses[i], want)
		}
	}
}

func TestInjuries(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha"}, {LeagueID: "B", Name: "Beta", Settings: sleeper.LeagueSettings{ReserveAllowOut: 1}}, {LeagueID: "C", Name: "Broken"},
		},
		// Alpha: out RB starting, questionable WR on bench, healthy QB starting.
		"/league/A/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"rbout", "wrq", "qb"}, Starters: []string{"rbout", "qb"}},
			{OwnerID: "200", Players: []string{"rbtaken"}},
		},
		// Beta: the same out RB on IR, the questionable WR starting.
		"/league/B/rosters": []sleeper.Roster{
			{OwnerID: "100", Players: []string{"rbout", "wrq"}, Starters: []string{"wrq"}, Reserve: []string{"rbout"}},
		},
		"/players/nfl/trending/add": []sleeper.Trending{{PlayerID: "rbhot", Count: 99}},
		"/players/nfl": map[string]sleeper.Player{
			"rbout":   {FullName: "Rb Out", Position: "RB", Team: "SF", Active: true, InjuryStatus: "Out", InjuryBodyPart: "Knee"},
			"wrq":     {FullName: "Wr Questionable", Position: "WR", Team: "MIN", Active: true, InjuryStatus: "Questionable"},
			"qb":      {FullName: "Healthy Qb", Position: "QB", Team: "KC", Active: true},
			"rbtaken": {FullName: "Rb Taken", Position: "RB", Team: "KC", Active: true, SearchRank: 1},
			"rbhot":   {FullName: "Rb Hot", Position: "RB", Team: "SEA", Active: true, SearchRank: 200},
			"wrfree":  {FullName: "Wr Free", Position: "WR", Team: "NE", Active: true, SearchRank: 60},
		},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")

	r, err := svc.Injuries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []InjuredPlayer{
		{
			PlayerID: "rbout", Name: "Rb Out", Position: "RB", NFLTeam: "SF", Status: "Out", BodyPart: "Knee", StartingIn: 1,
			Leagues: []InjuryLeague{
				{League: "Alpha", Starting: true, Replacement: &Target{PlayerID: "rbhot", Name: "Rb Hot", Position: "RB", NFLTeam: "SEA", Adds: 99, SearchRank: 200}},
				{League: "Beta", OnIR: true},
			},
		},
		{
			PlayerID: "wrq", Name: "Wr Questionable", Position: "WR", NFLTeam: "MIN", Status: "Questionable", StartingIn: 1,
			Leagues: []InjuryLeague{
				{League: "Alpha"},
				{League: "Beta", Starting: true, Replacement: &Target{PlayerID: "wrfree", Name: "Wr Free", Position: "WR", NFLTeam: "NE", SearchRank: 60}},
			},
		},
	}
	if !reflect.DeepEqual(r.Players, want) {
		t.Errorf("players:\n got %+v\nwant %+v", r.Players, want)
	}
	if len(r.FailedLeagues) != 1 || r.FailedLeagues[0].League != "Broken" {
		t.Errorf("failed = %+v", r.FailedLeagues)
	}
}
