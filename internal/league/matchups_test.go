package league

import (
	"context"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestOpponent(t *testing.T) {
	ms := []sleeper.Matchup{
		{RosterID: 1, MatchupID: 1},
		{RosterID: 2, MatchupID: 2},
		{RosterID: 3, MatchupID: 1},
		{RosterID: 4, MatchupID: 0}, // bye
		{RosterID: 5, MatchupID: 0}, // also a bye: not each other's opponent
	}
	tests := []struct {
		name     string
		me       int
		wantOpp  int
		wantPair bool
	}{
		{"paired", 1, 3, true},
		{"paired the other way", 3, 1, true},
		{"alone in its matchup", 2, 0, false},
		{"bye", 4, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			me, _ := find(ms, tt.me)
			opp, ok := Opponent(ms, me)
			if ok != tt.wantPair || opp.RosterID != tt.wantOpp {
				t.Errorf("Opponent(%d) = %d, %v; want %d, %v", tt.me, opp.RosterID, ok, tt.wantOpp, tt.wantPair)
			}
		})
	}
}

func TestSide(t *testing.T) {
	players := map[string]sleeper.Player{
		"4046": {PlayerID: "4046", FullName: "Patrick Mahomes", Position: "QB", Team: "KC", InjuryStatus: "Questionable"},
		"SEA":  {PlayerID: "SEA", FirstName: "Seattle", LastName: "Seahawks", Position: "DEF", Team: "SEA"},
	}
	m := sleeper.Matchup{
		Points:         30.5,
		Starters:       []string{"4046", "0", "99999", "SEA"},
		StartersPoints: []float64{20.5, 0, 4, 6},
	}
	proj := map[string]map[string]float64{
		"4046": {"pts_ppr": 22.4, "pts_std": 21.1},
		"SEA":  {"pts_ppr": 7.25, "pts_std": 7.25},
		"0":    {"pts_ppr": 99}, // never counted: an empty slot
	}
	got := side(m, "My Team", []string{"QB", "RB", "FLEX"}, players, proj, "pts_ppr")

	want := []Starter{
		{Slot: "QB", Name: "Patrick Mahomes", Position: "QB", NFLTeam: "KC", Injury: "Questionable", Points: 20.5, Projected: 22.4},
		{Slot: "RB", Name: "(empty)"},
		{Slot: "FLEX", Name: "99999", Points: 4}, // unknown ID degrades to the ID; no projection
		{Slot: "?", Name: "Seattle Seahawks", Position: "DEF", NFLTeam: "SEA", Points: 6, Projected: 7.25},
	}
	if got.Team != "My Team" || got.Points != 30.5 || got.Projected != 29.65 || len(got.Starters) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got.Starters[i] != want[i] {
			t.Errorf("starter %d = %+v, want %+v", i, got.Starters[i], want[i])
		}
	}
}

func TestProjectionKey(t *testing.T) {
	for rec, want := range map[float64]string{1: "pts_ppr", 0.5: "pts_half_ppr", 0: "pts_std", 1.5: "pts_ppr", 0.25: "pts_half_ppr"} {
		if got := projectionKey(rec); got != want {
			t.Errorf("projectionKey(%v) = %q, want %q", rec, got, want)
		}
	}
}

func TestSurvival(t *testing.T) {
	full := []string{"p"}
	rosters := []sleeper.Roster{
		{RosterID: 1, Players: full},
		{RosterID: 2, Players: full},
		{RosterID: 3, Players: full},
		{RosterID: 4},                // eliminated earlier: ignored even though it scores 0
		{RosterID: 6, Players: full}, // survivor with no matchup entry: ignored, not 0
	}
	ms := []sleeper.Matchup{
		{RosterID: 1, Points: 80.1},
		{RosterID: 2, Points: 60.25},
		{RosterID: 3, Points: 90},
		{RosterID: 4, Points: 0},
	}
	tests := []struct {
		name string
		me   int
		want Survival
	}{
		{"safe in the middle", 1, Survival{Alive: 4, Rank: 2, Margin: 19.85}},
		{"currently last", 2, Survival{Alive: 4, Rank: 3, Margin: -19.85}},
		{"top", 3, Survival{Alive: 4, Rank: 1, Margin: 29.75}},
		{"eliminated", 4, Survival{Eliminated: true, Alive: 4}},
		{"last team standing", 5, Survival{Alive: 1, Rank: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rosters, ms := rosters, ms
			if tt.me == 5 {
				rosters = []sleeper.Roster{{RosterID: 5, Players: full}, {RosterID: 4}}
				ms = []sleeper.Matchup{{RosterID: 5, Points: 70}}
			}
			var mine sleeper.Roster
			for _, r := range rosters {
				if r.RosterID == tt.me {
					mine = r
				}
			}
			if got := survival(ms, rosters, mine); *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestCutLine(t *testing.T) {
	full := []string{"p"}
	rosters := []sleeper.Roster{
		{RosterID: 1, Players: full}, {RosterID: 2, Players: full}, {RosterID: 3, Players: full},
		{RosterID: 4, Players: full}, {RosterID: 5}, // 5 was cut earlier
	}
	// Live points are all 0 before kickoff; projections rank 3 > 1 > 2 > 4. An empty slot ("0") counts 0.
	ms := []sleeper.Matchup{
		{RosterID: 1, Starters: []string{"a", "0"}}, {RosterID: 2, Starters: []string{"b"}},
		{RosterID: 3, Starters: []string{"c"}}, {RosterID: 4, Starters: []string{"d"}}, {RosterID: 5},
	}
	proj := map[string]map[string]float64{"a": {"pts_ppr": 100}, "b": {"pts_ppr": 90}, "c": {"pts_ppr": 120}, "d": {"pts_ppr": 70}}
	const by = " by Sleeper's whole-game projections "
	tests := []struct {
		name    string
		rosters []sleeper.Roster
		me      int
		proj    map[string]map[string]float64
		want    string
	}{
		{"safe", rosters, 3, proj, "L: projected 1st of 4" + by + "(120.0 pts), 50.0 pts above the lowest projected team"},
		{"second to last is at risk", rosters, 2, proj, "L: projected 3rd of 4" + by + "(90.0 pts), 20.0 pts above the lowest projected team; at risk of the cut, check the lineup"},
		{"projected last", rosters, 4, proj, "L: projected 4th of 4" + by + "(70.0 pts), 20.0 pts behind the next team; at risk of the cut, check the lineup"},
		{"final two: second isn't safe", rosters[:2], 2, proj, "L: projected 2nd of 2" + by + "(90.0 pts), 10.0 pts behind the next team; at risk of the cut, check the lineup"},
		{"final two: leader isn't at risk", rosters[:2], 1, proj, "L: projected 1st of 2" + by + "(100.0 pts), 10.0 pts above the lowest projected team"},
		{"no projections", rosters, 1, nil, ""},
		{"eliminated", rosters, 5, proj, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cutLine("L", ms, tt.rosters, rosters[tt.me-1], tt.proj, "pts_ppr"); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestOrdinal(t *testing.T) {
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd", 23: "23rd", 32: "32nd", 111: "111th"} {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestMatchups(t *testing.T) {
	me := sleeper.LeagueUser{UserID: "100", DisplayName: "me"}
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 3, SeasonType: "regular"},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "H", Name: "Head to head", RosterPositions: []string{"QB", "BN"}, Scoring: sleeper.Scoring{Rec: 0}},
			{LeagueID: "G", Name: "Guillotine", RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 3}},
			{LeagueID: "X", Name: "Broken"},
		},
		"/league/H/rosters":               []sleeper.Roster{{RosterID: 1, OwnerID: "100"}, {RosterID: 2, OwnerID: "200"}},
		"/league/H/users":                 []sleeper.LeagueUser{me, {UserID: "200", DisplayName: "rival"}},
		"/league/H/matchups/2":            []sleeper.Matchup{{RosterID: 1, MatchupID: 7, Points: 99, Starters: []string{"4046"}, StartersPoints: []float64{99}}, {RosterID: 2, MatchupID: 7, Points: 80}},
		"/league/G/rosters":               []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"4046"}}, {RosterID: 2, OwnerID: "200", Players: []string{"1"}}},
		"/league/G/users":                 []sleeper.LeagueUser{me},
		"/league/G/matchups/2":            []sleeper.Matchup{{RosterID: 1, MatchupID: 1, Points: 50, Starters: []string{"4046"}}, {RosterID: 2, MatchupID: 2, Points: 40, Starters: []string{"1"}}},
		"/league/X/rosters":               []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/X/users":                 []sleeper.LeagueUser{me},
		"/players/nfl":                    map[string]sleeper.Player{"4046": {PlayerID: "4046", FullName: "Patrick Mahomes"}},
		"/projections/nfl/regular/2026/2": map[string]map[string]float64{"4046": {"pts_ppr": 24, "pts_std": 21}},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")
	svc.projections.min = 1 // a one-player feed counts as complete here

	w, err := svc.Matchups(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if w.Week != 2 || len(w.Leagues) != 3 {
		t.Fatalf("week %d, %d leagues", w.Week, len(w.Leagues))
	}

	h := w.Leagues[0]
	if h.Me == nil || h.Opponent == nil || h.Survival != nil || h.Error != "" {
		t.Fatalf("head to head = %+v", h)
	}
	if h.Me.Points != 99 || h.Me.Starters[0].Name != "Patrick Mahomes" || h.Opponent.Team != "rival" || h.Opponent.Points != 80 ||
		h.Me.Projected != 21 || h.Me.Starters[0].Projected != 21 { // standard scoring
		t.Errorf("head to head me=%+v opp=%+v", h.Me, h.Opponent)
	}

	g := w.Leagues[1]
	if g.Opponent != nil || g.Survival == nil || *g.Survival != (Survival{Alive: 2, Rank: 1, Margin: 10}) {
		t.Errorf("guillotine = %+v, survival %+v", g, g.Survival)
	}

	if want := "Guillotine: projected 1st of 2 by Sleeper's whole-game projections (21.0 pts), 21.0 pts above the lowest projected team"; w.Note != want {
		t.Errorf("note = %q, want %q", w.Note, want)
	}

	if x := w.Leagues[2]; x.Error == "" || x.Me != nil {
		t.Errorf("broken league = %+v, want an error", x)
	}
}

func TestMatchupsWithoutProjections(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 3},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "H", Name: "H", RosterPositions: []string{"QB"}}},
		"/league/H/rosters":          []sleeper.Roster{{RosterID: 1, OwnerID: "100"}},
		"/league/H/users":            []sleeper.LeagueUser{{UserID: "100", DisplayName: "me"}},
		"/league/H/matchups/3":       []sleeper.Matchup{{RosterID: 1, Points: 12}},
		"/players/nfl":               map[string]sleeper.Player{},
		// No projections route: Sleeper fails for them.
	}))
	w, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me").Matchups(context.Background(), 0)
	if err != nil || w.Leagues[0].Me == nil || w.Leagues[0].Me.Points != 12 || !strings.Contains(w.Note, "no projections") {
		t.Errorf("got %+v, err %v; want live points and a note", w, err)
	}
}

func TestIdleNote(t *testing.T) {
	opp := &Side{Team: "rival", Projected: 50, Starters: []Starter{
		{Slot: "QB", Name: "Plays", NFLTeam: "KC", Projected: 20},
		{Slot: "RB", Name: "Hurt", Position: "RB", NFLTeam: "SF", Injury: "Out", Projected: 8},
		{Slot: "WR", Name: "Off", Position: "WR", NFLTeam: "CIN"},
		{Slot: "TE", Name: "Maybe", NFLTeam: "KC", Injury: "Doubtful", Projected: 4},
		{Slot: "FLEX", Name: "(empty)"},
	}}
	playing := map[string]bool{"KC": true, "SF": true}
	want := "TGIT: rival starts 3 who won't play (Hurt RB Out, Off WR bye, empty FLEX); projected without them: 42.0 (raw 50.0)"
	if got := idleNote("TGIT", opp, playing); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := idleNote("TGIT", &Side{Starters: []Starter{{Slot: "QB", Name: "Plays", NFLTeam: "KC"}}}, playing); got != "" {
		t.Errorf("everyone plays: got %q", got)
	}
	// Without projections there's no way to tell a bye: only injuries count.
	if got := idleNote("TGIT", opp, nil); !strings.Contains(got, "starts 2") {
		t.Errorf("no projections: got %q", got)
	}
}

func TestMatchupsNoteIdleOpponentStarters(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl":                 sleeper.State{Season: "2026", Week: 3, SeasonType: "regular"},
		"/user/me":                   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{{LeagueID: "H", Name: "H2H", RosterPositions: []string{"QB", "RB"}, Scoring: sleeper.Scoring{Rec: 1}}},
		"/league/H/rosters":          []sleeper.Roster{{RosterID: 1, OwnerID: "100"}, {RosterID: 2, OwnerID: "200"}},
		"/league/H/users":            []sleeper.LeagueUser{{UserID: "100", DisplayName: "me"}, {UserID: "200", DisplayName: "rival"}},
		"/league/H/matchups/3": []sleeper.Matchup{
			{RosterID: 1, MatchupID: 1, Starters: []string{"q1", "r1"}},
			{RosterID: 2, MatchupID: 1, Starters: []string{"q2", "r2"}},
		},
		"/players/nfl": map[string]sleeper.Player{
			"q1": {PlayerID: "q1", FullName: "My Qb", Position: "QB", Team: "KC"},
			"r1": {PlayerID: "r1", FullName: "My Rb", Position: "RB", Team: "KC"},
			"q2": {PlayerID: "q2", FullName: "Their Qb", Position: "QB", Team: "KC"},
			"r2": {PlayerID: "r2", FullName: "Their Rb", Position: "RB", Team: "SF", InjuryStatus: "IR"},
		},
		"/projections/nfl/regular/2026/3": map[string]map[string]float64{"q1": {"pts_ppr": 20}, "r1": {"pts_ppr": 10}, "q2": {"pts_ppr": 18}, "r2": {"pts_ppr": 6}},
	}))
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), nil, "me")
	svc.projections.min = 1
	w, err := svc.Matchups(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Note, "H2H: rival starts 1 who won't play (Their Rb RB IR); projected without them: 18.0 (raw 24.0)") {
		t.Errorf("note = %q", w.Note)
	}
}
