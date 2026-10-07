package league

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Season simulation.
const (
	simulations = 5000
	spreadShare = 0.2  // a team's weekly score varies by about a fifth of its average
	minSpread   = 15.0 // points: even steady teams swing this much
)

// OutlookReport is my season outlook in each league.
type OutlookReport struct {
	Week    int       `json:"week"`
	Leagues []Outlook `json:"leagues"`
	Method  string    `json:"method"`
}

// Outlook is one league's remaining schedule and playoff chances.
type Outlook struct {
	LeagueID       string     `json:"league_id"`
	League         string     `json:"league"`
	Record         string     `json:"record,omitempty" jsonschema:"wins-losses-ties now"`
	Standing       int        `json:"standing,omitempty"`
	PlayoffTeams   int        `json:"playoff_teams,omitempty"`
	PlayoffChance  *float64   `json:"playoff_chance,omitempty" jsonschema:"percent of simulated seasons in which I make the playoffs"`
	ExpectedWins   *float64   `json:"expected_wins,omitempty" jsonschema:"average final wins across simulated seasons (with a weekly median game, two results a week)"`
	Schedule       []Upcoming `json:"schedule,omitempty" jsonschema:"my remaining regular-season games"`
	ScheduleRating string     `json:"schedule_rating,omitempty" jsonschema:"harder, average or easier: remaining opponents' points per game against the league's"`
	Note           string     `json:"note,omitempty"`
	Error          string     `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// Upcoming is one remaining game.
type Upcoming struct {
	Week     int     `json:"week"`
	Opponent string  `json:"opponent"`
	Record   string  `json:"record"`
	PPG      float64 `json:"ppg" jsonschema:"opponent's points per game so far"`
	WinPct   float64 `json:"win_pct" jsonschema:"percent of simulated games I win"`
}

// SeasonOutlook simulates the rest of the regular season in every
// head-to-head league matching leagueQuery (empty = all).
func (s *Service) SeasonOutlook(ctx context.Context, leagueQuery string) (OutlookReport, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return OutlookReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return OutlookReport{}, err
	}
	out := OutlookReport{Week: state.Week, Leagues: make([]Outlook, len(leagues)),
		Method: fmt.Sprintf("%d simulated seasons: each team scores around its points per game so far (spread about %.0f%%), "+
			"standings by wins then points; divisions and custom tiebreakers are ignored", simulations, spreadShare*100)}
	eachLeague(leagues, func(i int, l sleeper.League) {
		o, err := s.outlook(ctx, l, user.UserID, state.Week)
		if err != nil {
			o.Error = err.Error()
		}
		out.Leagues[i] = o
	})
	return out, nil
}

func (s *Service) outlook(ctx context.Context, l sleeper.League, userID string, week int) (Outlook, error) {
	out := Outlook{LeagueID: l.LeagueID, League: l.Name, PlayoffTeams: l.Settings.PlayoffTeams}
	switch {
	case l.Kind() == "guillotine":
		out.Note = "guillotine league: no schedule or playoffs; the lowest score each week is cut"
		return out, nil
	case l.Settings.PlayoffWeekStart == 0 || l.Settings.PlayoffTeams == 0:
		out.Note = "this league has no playoffs set"
		return out, nil
	}

	var (
		rosters []sleeper.Roster
		users   []sleeper.LeagueUser
	)
	weeks := make([]int, 0)
	for w := max(week, 1); w < l.Settings.PlayoffWeekStart; w++ {
		weeks = append(weeks, w)
	}
	games := make([][]sleeper.Matchup, len(weeks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanOut)
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	for i, w := range weeks {
		g.Go(func() (err error) { games[i], err = s.api.Matchups(gctx, l.LeagueID, w); return err })
	}
	if err := g.Wait(); err != nil {
		return out, err
	}
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	st := mine.Settings
	out.Record = fmt.Sprintf("%d-%d-%d", st.Wins, st.Losses, st.Ties)
	out.Standing = Standing(rosters, mine.RosterID, false)

	median := l.Settings.MedianMatch == 1
	sim := simulate(rosters, games, l.Settings.PlayoffTeams, median, seed(l.LeagueID))
	chance, wins := sim.playoffs[mine.RosterID], sim.wins[mine.RosterID]
	out.PlayoffChance, out.ExpectedWins = &chance, &wins
	if len(weeks) == 0 {
		addNote(&out.Note, "the regular season is over: the chance follows the final standings")
	}

	byID := make(map[int]sleeper.Roster, len(rosters))
	for _, r := range rosters {
		byID[r.RosterID] = r
	}
	var oppPPG, sum float64
	for i, w := range weeks {
		me, ok := find(games[i], mine.RosterID)
		if !ok {
			continue
		}
		opp, ok := Opponent(games[i], me)
		if !ok {
			continue
		}
		r := byID[opp.RosterID]
		up := Upcoming{Week: w, Opponent: TeamName(users, r.OwnerID), PPG: round1(ppg(r.Settings, median)),
			Record: fmt.Sprintf("%d-%d-%d", r.Settings.Wins, r.Settings.Losses, r.Settings.Ties),
			WinPct: round1(sim.games[gameKey{week: i, roster: mine.RosterID}])}
		oppPPG += up.PPG
		out.Schedule = append(out.Schedule, up)
	}
	for _, r := range rosters {
		sum += ppg(r.Settings, median)
	}
	if n := len(out.Schedule); n > 0 && sum > 0 {
		ratio := (oppPPG / float64(n)) / (sum / float64(len(rosters)))
		switch {
		case ratio > 1.03:
			out.ScheduleRating = "harder"
		case ratio < 0.97:
			out.ScheduleRating = "easier"
		default:
			out.ScheduleRating = "average"
		}
	}
	return out, nil
}

// ppg is a team's points per week played so far; 0 before any games. With a
// weekly median game, each week adds two results to the record.
func ppg(st sleeper.RosterSettings, median bool) float64 {
	n := st.Wins + st.Losses + st.Ties
	if median {
		n /= 2
	}
	if n == 0 {
		return 0
	}
	return st.PointsFor() / float64(n)
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

// seed makes a league's simulation repeatable: the same answer each time.
func seed(leagueID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(leagueID)) // never fails
	return h.Sum64()
}

// gameKey is one roster's game in one remaining week (an index into weeks).
type gameKey struct{ week, roster int }

type simResult struct {
	playoffs map[int]float64     // roster -> percent of seasons in the playoffs
	wins     map[int]float64     // roster -> average final wins
	games    map[gameKey]float64 // percent of simulations in which that roster wins that game
}

// simulate plays the remaining weeks many times. Each team scores from a
// normal distribution around its points per game (the league's average
// before any games), wins and points carry over from now, and the top
// playoffTeams by wins, then points, make the playoffs. With median, each
// team also plays the week's median score. Ties are half a win now and
// can't happen in simulated games.
func simulate(rosters []sleeper.Roster, weeks [][]sleeper.Matchup, playoffTeams int, median bool, seedValue uint64) simResult {
	rng := rand.New(rand.NewPCG(seedValue, seedValue^0x9e3779b97f4a7c15)) //nolint:gosec // a repeatable simulation, not a secret
	mean := make(map[int]float64, len(rosters))
	var league float64
	for _, r := range rosters {
		league += ppg(r.Settings, median)
	}
	league /= float64(max(1, len(rosters)))
	if league == 0 {
		league = 100
	}
	for _, r := range rosters {
		mean[r.RosterID] = cmp.Or(ppg(r.Settings, median), league)
	}

	res := simResult{playoffs: map[int]float64{}, wins: map[int]float64{}, games: map[gameKey]float64{}}
	type team struct {
		id        int
		wins, pts float64
	}
	teams := make([]team, len(rosters))
	scores := make(map[int]float64, len(rosters))
	for range simulations {
		for i, r := range rosters {
			teams[i] = team{id: r.RosterID, wins: float64(r.Settings.Wins) + float64(r.Settings.Ties)/2, pts: r.Settings.PointsFor()}
		}
		idx := func(id int) int { return slices.IndexFunc(teams, func(t team) bool { return t.id == id }) }
		for wi, ms := range weeks {
			var all []float64
			for _, m := range ms {
				mu := mean[m.RosterID]
				scores[m.RosterID] = max(0, mu+rng.NormFloat64()*max(minSpread, mu*spreadShare))
				all = append(all, scores[m.RosterID])
			}
			med := 0.0
			if n := len(all); median && n > 0 {
				slices.Sort(all)
				med = all[n/2]
				if n%2 == 0 { // between the halves: the top half beats it
					med = (all[n/2-1] + all[n/2]) / 2
				}
			}
			for _, m := range ms {
				i := idx(m.RosterID)
				if i < 0 {
					continue
				}
				teams[i].pts += scores[m.RosterID]
				if opp, ok := Opponent(ms, m); ok && scores[m.RosterID] > scores[opp.RosterID] {
					teams[i].wins++
					res.games[gameKey{week: wi, roster: m.RosterID}] += 100.0 / simulations
				}
				if median && scores[m.RosterID] > med {
					teams[i].wins++
				}
			}
		}
		slices.SortStableFunc(teams, func(a, b team) int {
			return cmp.Or(cmp.Compare(b.wins, a.wins), cmp.Compare(b.pts, a.pts))
		})
		for rank, t := range teams {
			res.wins[t.id] += t.wins / simulations
			if rank < playoffTeams {
				res.playoffs[t.id] += 100.0 / simulations
			}
		}
	}
	for id := range res.playoffs {
		res.playoffs[id] = round1(res.playoffs[id])
	}
	for id := range res.wins {
		res.wins[id] = round1(res.wins[id])
	}
	return res
}
