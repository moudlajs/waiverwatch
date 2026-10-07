package league

import (
	"context"
	"fmt"
	"math"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Week is the user's matchups across all leagues for one week.
type Week struct {
	Season  string `json:"season"`
	Week    int    `json:"week"`
	Leagues []Game `json:"leagues"`
	Note    string `json:"note,omitempty"`
}

// Game is the user's game in one league. Head-to-head leagues set
// Opponent; guillotine leagues set Survival instead.
type Game struct {
	LeagueID string    `json:"league_id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Me       *Side     `json:"me,omitempty"`
	Opponent *Side     `json:"opponent,omitempty" jsonschema:"this week's opponent; absent on a bye and in guillotine leagues"`
	Survival *Survival `json:"survival,omitempty" jsonschema:"guillotine leagues only: the lowest score among surviving teams is eliminated"`
	Error    string    `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// Side is one team's lineup and live score.
type Side struct {
	Team      string    `json:"team"`
	Points    float64   `json:"points"`
	Projected float64   `json:"projected,omitempty" jsonschema:"the starters' projected points for the whole week (Sleeper's projections)"`
	Starters  []Starter `json:"starters"`
}

// Starter is one filled (or empty) lineup slot.
type Starter struct {
	Slot     string  `json:"slot" jsonschema:"lineup slot, e.g. QB, RB, FLEX, SUPER_FLEX"`
	Name     string  `json:"name"`
	Position string  `json:"position,omitempty"`
	NFLTeam  string  `json:"nfl_team,omitempty"`
	Injury   string  `json:"injury,omitempty"`
	Points   float64 `json:"points"`
	// Projected is for the whole game, not what's left of it: points
	// already scored are part of it.
	Projected float64 `json:"projected,omitempty" jsonschema:"Sleeper's projected points for the whole game in this league's standard, half or full PPR scoring (custom bonuses like TE premium aren't included); 0 or missing on a bye"`
}

// Survival is the user's position in a guillotine league this week.
type Survival struct {
	Eliminated bool    `json:"eliminated"`
	Alive      int     `json:"alive" jsonschema:"teams still in the league"`
	Rank       int     `json:"rank,omitempty" jsonschema:"this week's rank by points among surviving teams, 1 is best"`
	Margin     float64 `json:"margin" jsonschema:"my points minus the lowest other surviving team; negative means I am currently last"`
}

// Matchups returns the user's matchup in every league for week, or the
// current week when week is 0.
func (s *Service) Matchups(ctx context.Context, week int) (Week, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return Week{}, err
	}
	if week == 0 {
		week = state.Week
	}
	out := Week{Season: state.Season, Week: week, Leagues: make([]Game, len(leagues))}
	// Off-season (or unknown): ask for the regular season's week; failing
	// that, the answer just carries a note.
	seasonType := state.SeasonType
	if seasonType == "" || seasonType == "off" {
		seasonType = "regular"
	}
	var (
		proj    map[string]map[string]float64
		projErr error
		fetched = make(chan struct{})
	)
	go func() { // alongside the player dictionary: both can be cold
		defer close(fetched)
		proj, projErr = s.api.Projections(ctx, seasonType, state.Season, week)
	}()
	players, err := s.players.Players(ctx)
	<-fetched
	if err != nil {
		return Week{}, err
	}
	if projErr != nil {
		out.Note = "no projections: " + projErr.Error() // live points still stand
	} else {
		var (
			note string
			ok   bool
		)
		proj, note, ok = s.projections.complete(weekKey(seasonType, state.Season, week), proj, players, time.Now())
		if !ok {
			proj = nil // defenses alone would make partial totals
		}
		addNote(&out.Note, note)
	}

	eachLeague(leagues, func(i int, l sleeper.League) {
		m, err := s.matchup(ctx, l, user.UserID, week, players, proj)
		if err != nil {
			m.Error = err.Error()
		}
		out.Leagues[i] = m
	})
	return out, nil
}

func (s *Service) matchup(ctx context.Context, l sleeper.League, userID string, week int, players map[string]sleeper.Player, proj map[string]map[string]float64) (Game, error) {
	out := Game{LeagueID: l.LeagueID, Name: l.Name, Kind: l.Kind()}

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
	if !ok {
		return out, fmt.Errorf("no week %d matchup for my roster in league %s", week, l.LeagueID)
	}
	key := projectionKey(l.Scoring.Rec)
	out.Me = side(me, TeamName(users, mine.OwnerID), l.RosterPositions, players, proj, key)

	if l.Kind() == "guillotine" {
		out.Survival = survival(matchups, rosters, mine)
		return out, nil
	}
	if opp, ok := Opponent(matchups, me); ok {
		owner := ""
		for _, r := range rosters {
			if r.RosterID == opp.RosterID {
				owner = r.OwnerID
			}
		}
		out.Opponent = side(opp, TeamName(users, owner), l.RosterPositions, players, proj, key)
	}
	return out, nil
}

func find(ms []sleeper.Matchup, rosterID int) (sleeper.Matchup, bool) {
	for _, m := range ms {
		if m.RosterID == rosterID {
			return m, true
		}
	}
	return sleeper.Matchup{}, false
}

// Opponent is the other team sharing me's matchup ID. There is none on a
// bye (matchup ID 0).
func Opponent(ms []sleeper.Matchup, me sleeper.Matchup) (sleeper.Matchup, bool) {
	if me.MatchupID == 0 {
		return sleeper.Matchup{}, false
	}
	for _, m := range ms {
		if m.MatchupID == me.MatchupID && m.RosterID != me.RosterID {
			return m, true
		}
	}
	return sleeper.Matchup{}, false
}

// side resolves a matchup's starters to names. slots is the league's
// roster_positions; starters fill its first len(starters) entries in order.
func side(m sleeper.Matchup, team string, slots []string, players map[string]sleeper.Player, proj map[string]map[string]float64, key string) *Side {
	out := &Side{Team: team, Points: m.Points, Starters: make([]Starter, 0, len(m.Starters))}
	for i, id := range m.Starters {
		st := Starter{Slot: "?"}
		if i < len(slots) {
			st.Slot = slots[i]
		}
		if i < len(m.StartersPoints) {
			st.Points = m.StartersPoints[i]
		}
		if id == "0" || id == "" { // Sleeper's marker for an empty slot
			st.Name = "(empty)"
		} else {
			p := Lookup(players, id)
			st.Name, st.Position, st.NFLTeam, st.Injury = p.Name(), p.Position, p.Team, p.InjuryStatus
			st.Projected = proj[id][key]
			out.Projected += st.Projected
		}
		out.Starters = append(out.Starters, st)
	}
	out.Projected = math.Round(out.Projected*100) / 100
	return out
}

// projectionKey picks Sleeper's projected points for a league's points per
// reception: standard, half or full PPR.
func projectionKey(rec float64) string {
	switch {
	case rec >= 0.75:
		return "pts_ppr"
	case rec >= 0.25:
		return "pts_half_ppr"
	default:
		return "pts_std"
	}
}

// survival places mine among the guillotine league's surviving teams, i.e.
// rosters that still have players; eliminated teams are emptied.
func survival(ms []sleeper.Matchup, rosters []sleeper.Roster, mine sleeper.Roster) *Survival {
	if len(mine.Players) == 0 {
		return &Survival{Eliminated: true, Alive: alive(rosters)}
	}
	var myPts float64
	var others []float64
	for _, r := range rosters {
		if len(r.Players) == 0 {
			continue
		}
		m, ok := find(ms, r.RosterID)
		if !ok {
			continue // no entry this week: counting it as 0 would fake a lower last place
		}
		if r.RosterID == mine.RosterID {
			myPts = m.Points
		} else {
			others = append(others, m.Points)
		}
	}
	out := &Survival{Alive: alive(rosters), Rank: 1}
	if len(others) == 0 {
		return out // last team standing
	}
	lowest := others[0]
	for _, p := range others {
		if p > myPts {
			out.Rank++
		}
		lowest = min(lowest, p)
	}
	out.Margin = math.Round((myPts-lowest)*100) / 100
	return out
}

func alive(rosters []sleeper.Roster) int {
	n := 0
	for _, r := range rosters {
		if len(r.Players) > 0 {
			n++
		}
	}
	return n
}
