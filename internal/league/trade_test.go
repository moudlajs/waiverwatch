package league

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/sleeper/sleepertest"
	"github.com/moudlajs/waiverwatch/internal/store"
)

func TestAdjust(t *testing.T) {
	assets := func(vs ...int) []TradeAsset {
		var out []TradeAsset
		for _, v := range vs {
			out = append(out, TradeAsset{Value: v})
		}
		return out
	}
	adjusted := func(as []TradeAsset) []int {
		var out []int
		for _, a := range as {
			out = append(out, a.Adjusted)
		}
		return out
	}
	tests := []struct {
		name              string
		give, get         []TradeAsset
		wantGive, wantGet []int
	}{
		{"1-for-1 is unchanged", assets(10000), assets(8000), []int{10000}, []int{8000}},
		// 4000 × √(4000/10000) = 2530
		{"2-for-1 discounts the lesser piece", assets(10000), assets(8000, 4000), []int{10000}, []int{8000, 2530}},
		{"each side's best counts in full", assets(3000, 9000), assets(10000), []int{1643, 9000}, []int{10000}},
		{"nothing of value", assets(0), assets(0, 0), []int{0}, []int{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adjust(tt.give, tt.get)
			if g, h := adjusted(tt.give), adjusted(tt.get); !slices.Equal(g, tt.wantGive) || !slices.Equal(h, tt.wantGet) {
				t.Errorf("give %v get %v, want %v %v", g, h, tt.wantGive, tt.wantGet)
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	tests := []struct {
		give, get int
		want      string
	}{
		{10000, 10000, "fair"},
		{10000, 9600, "fair"},
		{10000, 9500, "fair"}, // exactly 5%
		{10000, 9499, "I lose slightly (5%)"},
		{10000, 9000, "I lose slightly (10%)"},
		{9000, 10000, "I win slightly (10%)"},
		{10000, 5000, "I lose clearly (50%)"},
		{0, 0, "neither side has any value"},
	}
	for _, tt := range tests {
		if got := verdict(tt.give, tt.get); got != tt.want {
			t.Errorf("verdict(%d, %d) = %q, want %q", tt.give, tt.get, got, tt.want)
		}
	}
}

func TestRosterMatches(t *testing.T) {
	players := map[string]sleeper.Player{
		"1": {FullName: "Josh Allen"},
		"2": {FullName: "Josh Downs"},
		"3": {FullName: "Allen Lazard"},
	}
	roster := []string{"1", "2", "3"}
	for query, want := range map[string]string{
		"josh allen": "1",   // exact beats Allen Lazard
		"downs":      "2",   // single partial
		"allen":      "1,3", // both partial
		"josh":       "1,2",
		"chase":      "",
	} {
		if got := strings.Join(rosterMatches(roster, query, players), ","); got != want {
			t.Errorf("rosterMatches(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestParsePick(t *testing.T) {
	tests := []struct {
		in   string
		want pickQuery
		ok   bool
	}{
		{"2027 1st", pickQuery{season: "2027", round: 1}, true},
		{"2027 1st (Early)", pickQuery{season: "2027", round: 1, slot: "early"}, true},
		{"2028 round 2", pickQuery{season: "2028", round: 2}, true},
		{"2027 2nd round from CHGO", pickQuery{season: "2027", round: 2, team: "chgo"}, true},
		{"2027 1st Rival FC's own", pickQuery{season: "2027", round: 1, team: "rival fcs"}, true},
		{"Ja'Marr Chase", pickQuery{}, false},
		{"2027", pickQuery{}, false},
	}
	for _, tt := range tests {
		got, ok := parsePick(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parsePick(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestProjectSlot(t *testing.T) {
	rec := func(id, wins, losses int) sleeper.Roster {
		return sleeper.Roster{RosterID: id, Settings: sleeper.RosterSettings{Wins: wins, Losses: losses}}
	}
	rosters := []sleeper.Roster{rec(1, 5, 0), rec(2, 4, 1), rec(3, 3, 2), rec(4, 2, 3), rec(5, 1, 4), rec(6, 0, 5)}
	for id, want := range map[int]string{1: "late", 2: "late", 3: "mid", 4: "mid", 5: "early", 6: "early"} {
		if got := projectSlot(rosters, id); got != want {
			t.Errorf("roster %d: %q, want %q", id, got, want)
		}
	}
	if got := projectSlot(rosters, 99); got != "" {
		t.Errorf("unknown team: %q, want none", got)
	}
	if got := projectSlot([]sleeper.Roster{rec(1, 0, 0), rec(2, 0, 0)}, 1); got != "" {
		t.Errorf("before any games: %q, want none", got)
	}
}

func TestLeaguePicks(t *testing.T) {
	rosters := []sleeper.Roster{{RosterID: 1}, {RosterID: 2}}
	traded := []sleeper.TradedPick{
		{Season: "2027", Round: 1, RosterID: 2, OwnerID: 1},
		{Season: "2026", Round: 1, RosterID: 1, OwnerID: 2}, // a past draft: not listed
	}
	got := leaguePicks(rosters, traded, []string{"2027"}, 2)
	want := []draftPick{
		{season: "2027", round: 1, origin: 1, holder: 1},
		{season: "2027", round: 1, origin: 2, holder: 1}, // traded to me
		{season: "2027", round: 2, origin: 1, holder: 1},
		{season: "2027", round: 2, origin: 2, holder: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestEvaluateTrade(t *testing.T) {
	record := func(w, l int) sleeper.RosterSettings { return sleeper.RosterSettings{Wins: w, Losses: l} }
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "D", Name: "Dynasty", TotalRosters: 12, RosterPositions: []string{"QB", "RB", "WR", "BN"},
				Settings: sleeper.LeagueSettings{Type: 2, DraftRounds: 2}, Scoring: sleeper.Scoring{Rec: 1}},
			{LeagueID: "R", Name: "Redraft", TotalRosters: 12, RosterPositions: []string{"QB"}, Scoring: sleeper.Scoring{Rec: 1}},
		},
		// Standings: Rival FC first (late picks), me second (mid), Third last (early).
		"/league/D/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"gibbs", "k"}, Settings: record(3, 2)},
			{RosterID: 2, OwnerID: "200", Players: []string{"chase", "wr2"}, Settings: record(5, 0)},
			{RosterID: 3, OwnerID: "300", Players: []string{"te", "cb"}, Settings: record(0, 5)},
		},
		"/league/D/users": []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival FC"), user("300", "third", "Third")},
		// Third's 2027 1st now belongs to Rival FC.
		"/league/D/traded_picks": []sleeper.TradedPick{{Season: "2027", Round: 1, RosterID: 3, OwnerID: 2, PreviousOwnerID: 3}},
		"/league/R/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"chase"}},
			{RosterID: 2, OwnerID: "200", Players: []string{"gibbs"}},
		},
		"/league/R/users": []sleeper.LeagueUser{user("100", "me", ""), user("200", "rival", "")},
		"/players/nfl": map[string]sleeper.Player{
			"gibbs": {PlayerID: "gibbs", FullName: "Jahmyr Gibbs", Position: "RB", Team: "DET", Active: true, Age: 24},
			"chase": {PlayerID: "chase", FullName: "Ja'Marr Chase", Position: "WR", Team: "CIN", Active: true, InjuryStatus: "Questionable"},
			"cb":    {PlayerID: "cb", FullName: "Chase Brown", Position: "RB", Team: "CIN", Active: true},
			"wr2":   {PlayerID: "wr2", FullName: "Second Receiver", Position: "WR", Active: true},
			"te":    {PlayerID: "te", FullName: "Tight End", Position: "TE", Active: true},
			"fa":    {PlayerID: "fa", FullName: "Free Agent", Position: "RB", Active: true},
			"k":     {PlayerID: "k", FullName: "Kicker Guy", Position: "K", Active: true},
			"sm1":   {PlayerID: "sm1", FullName: "Joe Smith", Position: "WR", Active: true},
			"sm2":   {PlayerID: "sm2", FullName: "Bob Smith", Position: "TE", Active: true},
		},
	}))
	values := func(_ context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		m := map[string]fantasycalc.Value{"gibbs": {Value: 10000}, "chase": {Value: 8000}, "wr2": {Value: 4000}, "te": {Value: 3000}, "fa": {Value: 500}, "cb": {Value: 3500}}
		if s.Dynasty {
			for _, p := range []fantasycalc.Value{
				{Name: "2027 1st (Early)", Value: 5000}, {Name: "2027 1st", Value: 3000}, {Name: "2027 1st (Late)", Value: 2000},
				{Name: "2028 1st", Value: 2500},
			} {
				p.Position, p.SleeperID = "PICK", p.Name
				m[p.Name] = p
			}
		}
		return m, nil
	}
	svc := NewService(api, NewDirectory(store.NewMemory(), api.Players), values, "me")
	ctx := context.Background()

	t.Run("2-for-1 in the only league where I have him", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "", []string{"gibbs"}, []string{"jamarr chase", "second receiver"})
		if err != nil {
			t.Fatal(err)
		}
		if r.League != "Dynasty" || r.Partner != "Rival FC" || r.Market != "dynasty 1QB 12-team PPR" || r.Give[0].Age != 24 || r.Get[0].Injury != "Questionable" {
			t.Errorf("league %q partner %q market %q give %+v", r.League, r.Partner, r.Market, r.Give)
		}
		if r.GiveValue != 10000 || r.GetValue != 12000 || r.GiveAdjusted != 10000 || r.GetAdjusted != 10530 || r.Margin != 530 ||
			r.MarginPct != 5 || r.Leans != "me" || r.Verdict != "I win slightly (5%)" {
			t.Errorf("got %+v", r)
		}
		if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "1 more player") }) {
			t.Errorf("want a roster spot note, got %v", r.Notes)
		}
		want := []DepthChange{
			{Position: "RB", Before: "thin (1 healthy, 0 backups)", After: "short (0 healthy, 0 backups)"},
			{Position: "WR", Before: "short (0 healthy, 0 backups)", After: "ok (2 healthy, 1 backups)"},
		}
		if !slices.Equal(r.Depth, want) {
			t.Errorf("depth %+v\nwant %+v", r.Depth, want)
		}
	})

	t.Run("an ambiguous name is settled by the partner's roster", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs"}, []string{"second receiver", "chase"})
		if err != nil || r.Get[1].Name != "Ja'Marr Chase" {
			t.Errorf("got %+v, err %v", r.Get, err)
		}
		_, err = svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs"}, []string{"chase"})
		if err == nil || !strings.Contains(err.Error(), "Chase Brown (RB CIN, Third)") || !strings.Contains(err.Error(), "Ja'Marr Chase (WR CIN, Rival FC)") {
			t.Errorf("err = %v, want both candidates with their teams", err)
		}
	})

	t.Run("my own pick, projected from my standing", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs", "2027 1st"}, []string{"ja'marr chase", "tight end"})
		if err != nil {
			t.Fatal(err)
		}
		p := r.Give[1]
		// Mid has no FantasyCalc value of its own here: a generic 1st, no projection claimed.
		if r.Partner != "Rival FC, Third" || p.Position != "PICK" || p.Team != "Mine" || p.OriginalTeam != "Mine" || p.Projected != "" || p.Value != 3000 || p.Name != "2027 1st" {
			t.Errorf("got %+v", r)
		}
		if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "1 more player") }) {
			t.Errorf("2 players for 1 player and a pick needs a roster spot, got %v", r.Notes) // picks take none
		}
	})

	t.Run("the partner's picks", func(t *testing.T) {
		_, err := svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs"}, []string{"ja'marr chase", "2027 1st"})
		if err == nil || !strings.Contains(err.Error(), "2027 1st (Third's, held by Rival FC)") {
			t.Errorf("err = %v, want both of Rival FC's 1sts listed", err)
		}
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs"}, []string{"ja'marr chase", "2027 1st third"})
		if err != nil {
			t.Fatal(err)
		}
		if p := r.Get[1]; p.Team != "Rival FC" || p.OriginalTeam != "Third" || p.Projected != "early" || p.Value != 5000 || p.Name != "2027 1st (early)" {
			t.Errorf("got %+v", p)
		}
		if r, err = svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs"}, []string{"ja'marr chase", "2027 1st rival"}); err != nil || r.Get[1].Value != 2000 {
			t.Errorf("Rival FC's own (late): %+v, err %v", r.Get, err)
		}
	})

	t.Run("a named slot and a later draft", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"2027 1st late", "2028 1st"}, []string{"tight end"})
		if err != nil {
			t.Fatal(err)
		}
		if g := r.Give; g[0].Value != 2000 || g[0].Projected != "" || g[1].Value != 2500 || g[1].Projected != "" || r.Partner != "Third" {
			t.Errorf("got %+v", r)
		}
	})

	t.Run("free agent and unrated player", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"kicker"}, []string{"free agent"})
		if err != nil {
			t.Fatal(err)
		}
		if !r.Give[0].Unrated || r.Get[0].Team != "" || len(r.Notes) != 2 {
			t.Errorf("got %+v", r)
		}
	})

	errs := []struct {
		name      string
		league    string
		give, get []string
		want      string
	}{
		{"in more than one league", "", []string{"chase"}, []string{"gibbs"}, ""}, // chase is mine only in Redraft
		{"in no league", "", []string{"zzz"}, []string{"tight end"}, "name the league"},
		{"mine in two leagues", "", []string{"j"}, []string{"tight end"}, "more than one league"}, // Jahmyr in Dynasty, Ja'Marr in Redraft
		{"not mine to give", "dynasty", []string{"tight end"}, []string{"chase"}, "not on my roster"},
		{"already mine", "dynasty", []string{"gibbs"}, []string{"kicker guy"}, "already on my roster"},
		{"named twice", "dynasty", []string{"gibbs"}, []string{"second receiver", "second"}, "twice"},
		{"nobody", "dynasty", []string{"gibbs"}, []string{"zzz"}, "no player or pick"},
		{"empty side", "dynasty", []string{"gibbs"}, nil, "each side"},
		{"picks alone", "", []string{"2027 1st"}, []string{"chase"}, "name the league"},
		{"empty name", "dynasty", []string{"gibbs"}, []string{" "}, "empty"},
		{"two of mine match", "", []string{"jahmyr", "y"}, []string{"second receiver"}, "matches more than one"}, // Dynasty: Jahmyr Gibbs and Kicker Guy both contain "y"
		{"ambiguous outside the rosters", "dynasty", []string{"gibbs"}, []string{"smith"}, "Joe Smith (WR no NFL team, free agent)"},
		{"a pick I don't hold", "dynasty", []string{"2027 1st third"}, []string{"tight end"}, "no 2027 1st held by Mine"},
		{"a draft FantasyCalc doesn't value", "dynasty", []string{"2031 1st"}, []string{"tight end"}, "2027, 2028 only"},
		{"picks in redraft", "redraft", []string{"chase", "2027 1st"}, []string{"gibbs"}, "only valued in dynasty"},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			r, err := svc.EvaluateTrade(ctx, tt.league, tt.give, tt.get)
			if tt.want == "" {
				if err != nil || r.League != "Redraft" {
					t.Errorf("league %q, err %v; want Redraft", r.League, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEvaluateTradeLeagueDown(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "A", Name: "Alpha"}, {LeagueID: "B", Name: "Beta"},
		},
		"/league/A/rosters": []sleeper.Roster{{RosterID: 1, OwnerID: "100", Players: []string{"x"}}}, // matches, but Beta might too
		// Beta's rosters are missing: Sleeper fails for that league.
		"/players/nfl": map[string]sleeper.Player{"x": {PlayerID: "x", FullName: "Some Player", Position: "WR"}},
	}))
	none := func(context.Context, fantasycalc.Settings) (map[string]fantasycalc.Value, error) { return nil, nil }
	_, err := NewService(api, NewDirectory(store.NewMemory(), api.Players), none, "me").
		EvaluateTrade(context.Background(), "", []string{"some player"}, []string{"other"})
	if err == nil || !strings.Contains(err.Error(), "couldn't check every league") || !errors.Is(err, sleeper.ErrNotFound) {
		t.Errorf("err = %v, want the league error passed on", err)
	}
}
