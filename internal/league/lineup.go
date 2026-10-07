package league

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// minGain is the projected gain below which a different lineup isn't worth
// suggesting: projections aren't that precise.
const minGain = 1.0

// LineupReport checks my lineup in each league for one week.
type LineupReport struct {
	Week    int           `json:"week"`
	Leagues []LineupCheck `json:"leagues"`
	Note    string        `json:"note,omitempty"`
}

// LineupCheck is one league's lineup check.
type LineupCheck struct {
	LeagueID  string         `json:"league_id"`
	League    string         `json:"league"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status" jsonschema:"ok, or fix when there are problems or a better lineup"`
	Problems  []string       `json:"problems" jsonschema:"empty slots, starters who are out or on bye, questionable starters"`
	Start     []LineupPlayer `json:"start,omitempty" jsonschema:"bench players the best lineup starts, with the slot they'd take"`
	Bench     []LineupPlayer `json:"bench,omitempty" jsonschema:"current starters the best lineup sits, with the slot they hold now"`
	Projected float64        `json:"projected" jsonschema:"my current starters' projected points (out or on bye counts 0)"`
	Best      float64        `json:"best" jsonschema:"the best lineup's projected points"`
	Note      string         `json:"note,omitempty"`
	Error     string         `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// LineupPlayer is a player in a suggested lineup change.
type LineupPlayer struct {
	Slot      string  `json:"slot"`
	Name      string  `json:"name"`
	Position  string  `json:"position,omitempty"`
	NFLTeam   string  `json:"nfl_team,omitempty"`
	Injury    string  `json:"injury,omitempty"`
	Projected float64 `json:"projected"`
}

// week is what a lineup check needs to know about the NFL week.
type week struct {
	proj     map[string]map[string]float64 // projected stats by player
	playing  map[string]bool               // NFL teams with a game (anyone projected)
	complete bool                          // false: player points are missing, so no swaps
}

// LineupCheck checks my lineup in every league matching leagueQuery (empty =
// all) for the current week.
func (s *Service) LineupCheck(ctx context.Context, leagueQuery string) (LineupReport, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return LineupReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return LineupReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return LineupReport{}, err
	}
	seasonType := state.SeasonType
	if seasonType == "" || seasonType == "off" {
		seasonType = "regular"
	}
	feed, err := s.api.Projections(ctx, seasonType, state.Season, state.Week)
	if err != nil {
		// Without projections there are no byes or better lineups to find.
		return LineupReport{}, fmt.Errorf("checking lineups needs this week's projections: %w", err)
	}
	proj, projNote, complete := s.projections.complete(weekKey(seasonType, state.Season, state.Week), feed, players, time.Now())
	// Byes come from anyone projected: team defenses stay even in a blank feed.
	wk := week{proj: proj, playing: make(map[string]bool), complete: complete}
	for id, stats := range proj {
		if _, ok := stats["pts_ppr"]; ok {
			if team := players[id].Team; team != "" {
				wk.playing[team] = true
			}
		}
	}

	if len(wk.playing) == 0 {
		// Off-season or a week without projections yet: everyone would look on bye.
		return LineupReport{Week: state.Week, Leagues: []LineupCheck{}, Note: "no NFL games are projected this week, so there are no lineups to check"}, nil
	}
	out := LineupReport{Week: state.Week, Leagues: make([]LineupCheck, len(leagues)),
		Note: "projections are for whole games: a player whose game has started is locked, so check kickoff times before swapping"}
	if !complete {
		out.Note = projNote + "; only empty slots, injuries and byes are checked"
	} else {
		addNote(&out.Note, projNote)
	}
	eachLeague(leagues, func(i int, l sleeper.League) {
		c, err := s.lineup(ctx, l, user.UserID, players, wk)
		if err != nil {
			c.Error = err.Error()
		}
		out.Leagues[i] = c
	})
	return out, nil
}

func (s *Service) lineup(ctx context.Context, l sleeper.League, userID string, players map[string]sleeper.Player, wk week) (LineupCheck, error) {
	out := LineupCheck{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Status: "ok", Problems: []string{}}
	rosters, err := s.api.Rosters(ctx, l.LeagueID)
	if err != nil {
		return out, err
	}
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	if len(mine.Players) == 0 {
		out.Note = "eliminated from this league"
		return out, nil
	}
	key := projectionKey(l.Scoring.Rec)
	pts := func(id string) float64 { return wk.proj[id][key] }
	out.Problems, out.Start, out.Bench, out.Projected, out.Best = checkLineup(l.RosterPositions, mine, players, wk, pts)
	if !wk.complete { // missing points aren't zeros: no swaps, no totals
		out.Start, out.Bench, out.Projected, out.Best = nil, nil, 0, 0
	}
	if len(out.Problems) > 0 || len(out.Start) > 0 {
		out.Status = "fix"
	}
	return out, nil
}

// checkLineup finds what's wrong with a roster's lineup and the best lineup
// by projections. Slots it doesn't know (IDP) are left as they are.
func checkLineup(slots []string, r sleeper.Roster, players map[string]sleeper.Player, wk week, pts func(id string) float64) (problems []string, start, bench []LineupPlayer, current, best float64) {
	problems = []string{}
	out := func(id string) (bool, string) { // can't score this week, and why
		p := Lookup(players, id)
		switch {
		case slices.Contains(unavailable, p.InjuryStatus):
			return true, p.InjuryStatus
		case p.Team == "":
			return true, "no NFL team"
		case !wk.playing[p.Team]:
			return true, "on bye"
		}
		return false, ""
	}
	lp := func(id, slot string) LineupPlayer {
		p := Lookup(players, id)
		return LineupPlayer{Slot: slot, Name: p.Name(), Position: p.Position, NFLTeam: p.Team, Injury: p.InjuryStatus, Projected: pts(id)}
	}

	// The current lineup, slot by slot.
	type seat struct{ slot, id string }
	var seats []seat
	holes := 0 // empty slots and starters who can't score
	for i, slot := range slots {
		if slot == "BN" || slot == "IR" || slot == "TAXI" {
			continue // not a starting slot
		}
		id := "0" // Sleeper may list fewer starters than slots: the rest are empty
		if i < len(r.Starters) {
			id = r.Starters[i]
		}
		if !known(slot) {
			continue
		}
		seats = append(seats, seat{slot, id})
		switch out, why := out(id); {
		case id == "0" || id == "":
			holes++
			problems = append(problems, slot+" slot is empty")
		case out:
			holes++
			problems = append(problems, fmt.Sprintf("%s (%s) is %s", Lookup(players, id).Name(), slot, why))
		default:
			current += pts(id)
			if Lookup(players, id).InjuryStatus == "Questionable" {
				problems = append(problems, fmt.Sprintf("%s (%s) is questionable", Lookup(players, id).Name(), slot))
			}
		}
	}

	// The best lineup: every healthy, playing player on the active roster
	// (not IR or taxi), dedicated slots first, then flex from the most
	// restrictive to the least. Greedy: right for the usual slot sets, but a
	// heuristic, not a guaranteed optimum for every combination.
	var pool []string
	for _, id := range r.Players {
		if gone, _ := out(id); !gone && !slices.Contains(r.Reserve, id) && !slices.Contains(r.Taxi, id) {
			pool = append(pool, id)
		}
	}
	slices.SortStableFunc(pool, func(a, b string) int { return cmp.Compare(pts(b), pts(a)) })
	order := make([]int, len(seats))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(len(eligible(seats[a].slot)), len(eligible(seats[b].slot))) })
	chosen := make(map[int]string) // seat index -> player
	used := make(map[string]bool)
	for _, i := range order {
		for _, id := range pool {
			if !used[id] && slices.Contains(eligible(seats[i].slot), Lookup(players, id).Position) {
				chosen[i], used[id] = id, true
				best += pts(id)
				break
			}
		}
	}
	current, best = math.Round(current*100)/100, math.Round(best*100)/100

	// Only suggest changes worth making: any change that fills a hole,
	// otherwise only a real gain.
	starting := make(map[string]bool)
	for _, st := range seats {
		starting[st.id] = true
	}
	if best-current < minGain && holes == 0 {
		return problems, nil, nil, current, best
	}
	for i, st := range seats {
		if id, ok := chosen[i]; ok && !starting[id] {
			start = append(start, lp(id, st.slot))
		}
		if st.id != "0" && st.id != "" && !used[st.id] {
			bench = append(bench, lp(st.id, st.slot))
		}
	}
	return problems, start, bench, current, best
}

// eligible is the positions a lineup slot accepts.
func eligible(slot string) []string {
	if ps, ok := flexSlots[slot]; ok {
		return ps
	}
	return []string{slot}
}

// known reports whether waiverwatch understands a lineup slot.
func known(slot string) bool {
	_, flex := flexSlots[slot]
	return flex || slices.Contains(Positions, slot)
}
