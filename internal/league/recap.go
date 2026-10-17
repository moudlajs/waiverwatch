package league

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Recap is how a finished week went in each league.
type Recap struct {
	Season      string       `json:"season"`
	Week        int          `json:"week"`
	Leagues     []WeekResult `json:"leagues"`
	Wins        int          `json:"wins"`
	Losses      int          `json:"losses"`
	Ties        int          `json:"ties"`
	LeftOnBench float64      `json:"left_on_bench" jsonschema:"points my best possible lineups would have added, summed over leagues"`
	Note        string       `json:"note,omitempty"`
}

// WeekResult is one league's week in hindsight.
type WeekResult struct {
	LeagueID       string        `json:"league_id"`
	League         string        `json:"league"`
	Kind           string        `json:"kind"`
	Result         string        `json:"result" jsonschema:"win, loss, tie, bye (no opponent), survived or cut (guillotine), or eliminated (cut in an earlier week)"`
	Points         float64       `json:"points"`
	Opponent       string        `json:"opponent,omitempty"`
	OpponentPoints float64       `json:"opponent_points,omitempty"`
	Rank           int           `json:"rank,omitempty" jsonschema:"guillotine: my rank among teams still alive that week, 1 is best"`
	Alive          int           `json:"alive,omitempty" jsonschema:"guillotine: teams still alive that week"`
	TopScorer      *RecapPlayer  `json:"top_scorer,omitempty"`
	Best           float64       `json:"best" jsonschema:"the best lineup in hindsight from that week's roster and actual points"`
	LeftOnBench    float64       `json:"left_on_bench" jsonschema:"best minus what my starters scored"`
	CouldHaveWon   bool          `json:"could_have_won,omitempty" jsonschema:"a loss the best lineup would have won"`
	ShouldStart    []RecapPlayer `json:"should_have_started,omitempty" jsonschema:"bench players the best lineup starts, with the slot they'd take"`
	ShouldSit      []RecapPlayer `json:"should_have_sat,omitempty" jsonschema:"starters the best lineup sits, with the slot they held"`
	Error          string        `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// RecapPlayer is a player and what he scored that week.
type RecapPlayer struct {
	Slot     string  `json:"slot,omitempty"`
	Name     string  `json:"name"`
	Position string  `json:"position,omitempty"`
	NFLTeam  string  `json:"nfl_team,omitempty"`
	Points   float64 `json:"points"`
}

// WeekRecap reviews a finished week (0 = last week) in leagues matching leagueQuery.
func (s *Service) WeekRecap(ctx context.Context, week int, leagueQuery string) (Recap, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return Recap{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return Recap{}, err
	}
	if week == 0 {
		week = state.Week - 1
	}
	out := Recap{Season: state.Season, Week: week, Leagues: []WeekResult{}}
	if week < 1 {
		out.Note = "no week has been played yet this season"
		return out, nil
	}
	if week >= state.Week {
		out.Note = "this week isn't finished: results and bench points can still change"
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return Recap{}, err
	}
	out.Leagues = make([]WeekResult, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		r, err := s.recap(ctx, l, user.UserID, week, players)
		if err != nil {
			r.Error = err.Error()
		}
		out.Leagues[i] = r
	})
	for _, r := range out.Leagues {
		switch r.Result {
		case "win":
			out.Wins++
		case "loss":
			out.Losses++
		case "tie":
			out.Ties++
		}
		out.LeftOnBench += r.LeftOnBench
	}
	out.LeftOnBench = math.Round(out.LeftOnBench*100) / 100
	return out, nil
}

func (s *Service) recap(ctx context.Context, l sleeper.League, userID string, week int, players map[string]sleeper.Player) (WeekResult, error) {
	out := WeekResult{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind()}
	var (
		rosters  []sleeper.Roster
		users    []sleeper.LeagueUser
		matchups []sleeper.Matchup
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { matchups, err = s.api.Matchups(gctx, l.LeagueID, week); return err })
	if err := g.Wait(); err != nil {
		return out, err
	}
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	me, ok := find(matchups, mine.RosterID)
	if l.Kind() == "guillotine" && (!ok || !fielded(me)) {
		out.Result = "eliminated"
		return out, nil
	}
	if !ok {
		return out, fmt.Errorf("no week %d matchup for my roster in league %s", week, l.LeagueID)
	}
	out.Points = me.Points

	pts := func(id string) float64 { return me.PlayersPoints[id] }
	rp := func(id, slot string) RecapPlayer {
		p := Lookup(players, id)
		return RecapPlayer{Slot: slot, Name: p.Name(), Position: p.Position, NFLTeam: p.Team, Points: pts(id)}
	}
	seats := seatsOf(l.RosterPositions, me.Starters)
	var current float64
	for _, st := range seats {
		current += pts(st.id)
		if st.id != "0" && st.id != "" && (out.TopScorer == nil || pts(st.id) > out.TopScorer.Points) {
			top := rp(st.id, st.slot)
			out.TopScorer = &top
		}
	}
	// IR and taxi as they are now: Sleeper doesn't keep them per week.
	var pool []string
	for _, id := range me.Players {
		if !slices.Contains(mine.Reserve, id) && !slices.Contains(mine.Taxi, id) {
			pool = append(pool, id)
		}
	}
	chosen, used, best := fill(seats, pool, players, pts)
	out.Best = math.Round(best*100) / 100
	out.LeftOnBench = math.Round(max(0, best-current)*100) / 100
	if out.LeftOnBench >= minGain {
		starting := make(map[string]bool)
		for _, st := range seats {
			starting[st.id] = true
		}
		for i, st := range seats {
			if id, ok := chosen[i]; ok && !starting[id] {
				out.ShouldStart = append(out.ShouldStart, rp(id, st.slot))
			}
			if st.id != "0" && st.id != "" && !used[st.id] {
				out.ShouldSit = append(out.ShouldSit, rp(st.id, st.slot))
			}
		}
	} else {
		out.LeftOnBench = 0
	}

	if l.Kind() == "guillotine" {
		var alive []sleeper.Matchup
		for _, m := range matchups {
			if fielded(m) {
				alive = append(alive, m)
			}
		}
		slices.SortStableFunc(alive, func(a, b sleeper.Matchup) int { return cmp.Compare(b.Points, a.Points) })
		out.Alive, out.Rank = len(alive), 1
		for _, m := range alive {
			if m.Points > me.Points {
				out.Rank++
			}
		}
		out.Result = "survived"
		if out.Alive > 1 && out.Rank == out.Alive {
			out.Result = "cut"
		}
		return out, nil
	}
	opp, ok := Opponent(matchups, me)
	if !ok {
		out.Result = "bye"
		return out, nil
	}
	for _, r := range rosters {
		if r.RosterID == opp.RosterID {
			out.Opponent = TeamName(users, r.OwnerID)
		}
	}
	out.OpponentPoints = opp.Points
	switch {
	case me.Points > opp.Points:
		out.Result = "win"
	case me.Points < opp.Points:
		out.Result = "loss"
		out.CouldHaveWon = out.Best > opp.Points
	default:
		out.Result = "tie"
	}
	return out, nil
}

// fielded reports whether a team had anyone in its lineup that week (eliminated guillotine teams don't).
func fielded(m sleeper.Matchup) bool {
	return slices.ContainsFunc(m.Starters, func(id string) bool { return id != "0" && id != "" })
}
