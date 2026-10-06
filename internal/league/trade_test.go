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

func TestOnRoster(t *testing.T) {
	players := map[string]sleeper.Player{
		"1": {FullName: "Josh Allen"},
		"2": {FullName: "Josh Downs"},
		"3": {FullName: "Allen Lazard"},
	}
	roster := []string{"1", "2", "3"}
	tests := []struct {
		query, want string
		wantErr     error
	}{
		{query: "josh allen", want: "1"},
		{query: "downs", want: "2"},
		{query: "allen", wantErr: errAmbiguous}, // Josh Allen and Allen Lazard
		{query: "josh", wantErr: errAmbiguous},
		{query: "chase", wantErr: errors.New("not on the roster")},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, err := onRoster(roster, tt.query, players)
			switch {
			case tt.wantErr == nil && (err != nil || got != tt.want):
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			case errors.Is(tt.wantErr, errAmbiguous) && !errors.Is(err, errAmbiguous):
				t.Errorf("err = %v, want ambiguous", err)
			case tt.wantErr != nil && err == nil:
				t.Errorf("got %q, want an error", got)
			}
		})
	}
}

func TestFindPick(t *testing.T) {
	market := map[string]fantasycalc.Value{
		"a": {SleeperID: "a", Name: "2027 1st (Early)", Position: "PICK", Value: 4881},
		"b": {SleeperID: "b", Name: "2027 1st", Position: "PICK", Value: 2973},
		"c": {SleeperID: "c", Name: "2027 2nd", Position: "PICK", Value: 1200},
		"p": {SleeperID: "p", Name: "2027 1st Round Bust", Position: "WR", Value: 9},
	}
	for query, want := range map[string]string{"2027 1st": "b", "2027 1st early": "a", "2027 2": "c", "2028 1st": ""} {
		got, ok := findPick(market, query)
		if (want == "" && ok) || (want != "" && got.SleeperID != want) {
			t.Errorf("findPick(%q) = %+v, %v; want %q", query, got, ok, want)
		}
	}
}

func TestEvaluateTrade(t *testing.T) {
	api := sleeper.New(sleepertest.NewServer(t, sleepertest.Routes{
		"/state/nfl": sleeper.State{Season: "2026", Week: 5},
		"/user/me":   sleeper.User{UserID: "100"},
		"/user/100/leagues/nfl/2026": []sleeper.League{
			{LeagueID: "D", Name: "Dynasty", TotalRosters: 12, RosterPositions: []string{"QB"}, Settings: sleeper.LeagueSettings{Type: 2}, Scoring: sleeper.Scoring{Rec: 1}},
			{LeagueID: "R", Name: "Redraft", TotalRosters: 12, RosterPositions: []string{"QB"}, Scoring: sleeper.Scoring{Rec: 1}},
		},
		"/league/D/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"gibbs", "k"}},
			{RosterID: 2, OwnerID: "200", Players: []string{"chase", "wr2"}},
			{RosterID: 3, OwnerID: "300", Players: []string{"te"}},
		},
		"/league/D/users": []sleeper.LeagueUser{user("100", "me", "Mine"), user("200", "rival", "Rival FC"), user("300", "third", "Third")},
		"/league/R/rosters": []sleeper.Roster{
			{RosterID: 1, OwnerID: "100", Players: []string{"chase"}},
			{RosterID: 2, OwnerID: "200", Players: []string{"gibbs"}},
		},
		"/league/R/users": []sleeper.LeagueUser{user("100", "me", ""), user("200", "rival", "")},
		"/players/nfl": map[string]sleeper.Player{
			"gibbs": {PlayerID: "gibbs", FullName: "Jahmyr Gibbs", Position: "RB", Team: "DET", Active: true},
			"chase": {PlayerID: "chase", FullName: "Ja'Marr Chase", Position: "WR", Team: "CIN", Active: true},
			"wr2":   {PlayerID: "wr2", FullName: "Second Receiver", Position: "WR", Active: true},
			"te":    {PlayerID: "te", FullName: "Tight End", Position: "TE", Active: true},
			"fa":    {PlayerID: "fa", FullName: "Free Agent", Position: "RB", Active: true},
			"k":     {PlayerID: "k", FullName: "Kicker Guy", Position: "K", Active: true},
			"sm1":   {PlayerID: "sm1", FullName: "Joe Smith", Position: "WR", Active: true},
			"sm2":   {PlayerID: "sm2", FullName: "Bob Smith", Position: "TE", Active: true},
		},
	}))
	values := func(_ context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		m := map[string]fantasycalc.Value{"gibbs": {Value: 10000}, "chase": {Value: 8000}, "wr2": {Value: 4000}, "te": {Value: 3000}, "fa": {Value: 500}}
		if s.Dynasty {
			m["FP_2027_1"] = fantasycalc.Value{SleeperID: "FP_2027_1", Name: "2027 1st", Position: "PICK", Value: 3000}
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
		if r.League != "Dynasty" || r.Partner != "Rival FC" || r.Market != "dynasty 1QB 12-team PPR" {
			t.Errorf("league %q partner %q market %q", r.League, r.Partner, r.Market)
		}
		if r.GiveValue != 10000 || r.GetValue != 12000 || r.GiveAdjusted != 10000 || r.GetAdjusted != 10530 || r.Margin != 530 || r.Verdict != "I win slightly (5%)" && r.Verdict != "fair" {
			t.Errorf("got %+v", r)
		}
		if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "1 more player") }) {
			t.Errorf("want a roster spot note, got %v", r.Notes)
		}
	})

	t.Run("players from two teams and a pick", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "dynasty", []string{"gibbs", "2027 1st"}, []string{"chase", "tight end"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Partner != "Rival FC, Third" || r.Give[1].Position != "PICK" || r.Give[1].Value != 3000 {
			t.Errorf("got %+v", r)
		}
		if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "1 more player") }) {
			t.Errorf("2 players for 1 player and a pick needs a roster spot, got %v", r.Notes) // picks take none
		}
		if !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.Contains(n, "draft pick") }) {
			t.Errorf("want a pick ownership note, got %v", r.Notes)
		}
	})

	t.Run("a pick on the give side doesn't stop finding the league", func(t *testing.T) {
		r, err := svc.EvaluateTrade(ctx, "", []string{"jahmyr", "2027 1st"}, []string{"tight end"})
		if err != nil || r.League != "Dynasty" || r.Give[1].Position != "PICK" {
			t.Errorf("got %+v, err %v", r, err)
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
		{"already mine", "dynasty", []string{"gibbs"}, []string{"kicker guy"}, "already on my roster"},
		{"named twice", "dynasty", []string{"gibbs"}, []string{"chase", "ja'marr chase"}, "twice"},
		{"nobody", "dynasty", []string{"gibbs"}, []string{"zzz"}, "no player or pick"},
		{"empty side", "dynasty", []string{"gibbs"}, nil, "each side"},
		{"picks alone", "", []string{"2027 1st"}, []string{"chase"}, "name the league"},
		{"empty name", "dynasty", []string{"gibbs"}, []string{" "}, "empty"},
		{"two of mine match", "", []string{"jahmyr", "e"}, []string{"chase"}, "matches more than one"}, // Dynasty: Jahmyr Gibbs and Kicker Guy both contain "e"
		{"ambiguous outside the rosters", "dynasty", []string{"gibbs"}, []string{"smith"}, "Joe Smith"},
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
