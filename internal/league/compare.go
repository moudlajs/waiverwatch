package league

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Comparisons is my roster against another team's, per league.
type Comparisons struct {
	Week    int          `json:"week"`
	Leagues []Comparison `json:"leagues"`
}

// Comparison is one league's side-by-side.
type Comparison struct {
	LeagueID  string            `json:"league_id"`
	League    string            `json:"league"`
	Kind      string            `json:"kind"`
	Me        *TeamRecord       `json:"me,omitempty"`
	Them      *TeamRecord       `json:"them,omitempty"`
	Positions []PositionCompare `json:"positions,omitempty"`
	Note      string            `json:"note,omitempty"`
	Error     string            `json:"error,omitempty" jsonschema:"set when this league could not be loaded or the owner was not found; the others are still valid"`
}

// TeamRecord names a team and its season so far.
type TeamRecord struct {
	Team      string  `json:"team"`
	Owner     string  `json:"owner,omitempty"`
	Record    string  `json:"record" jsonschema:"wins-losses-ties"`
	PointsFor float64 `json:"points_for"`
}

// PositionCompare is both teams' players at one position: starters first
// (with their lineup slot), then bench, then IR (slot "IR"). Taxi squads are
// left out.
type PositionCompare struct {
	Position string         `json:"position"`
	Mine     []RosterPlayer `json:"mine"`
	Theirs   []RosterPlayer `json:"theirs"`
}

// Compare puts my roster next to another team's in each league matching
// leagueQuery (empty = all). With no owner, the other team is this week's
// opponent; with an owner, leagues they are not in are left out unless a
// league was named.
func (s *Service) Compare(ctx context.Context, leagueQuery, owner string) (Comparisons, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return Comparisons{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return Comparisons{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return Comparisons{}, err
	}

	all := make([]Comparison, len(leagues))
	skip := make([]bool, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		c, err := s.compare(ctx, l, user.UserID, owner, state.Week, players)
		var nf notFoundError
		switch {
		case err == nil:
		case errors.As(err, &nf) && owner != "" && leagueQuery == "":
			skip[i] = true
		default:
			c.Error = err.Error()
		}
		all[i] = c
	})

	out := Comparisons{Week: state.Week, Leagues: []Comparison{}}
	for i, c := range all {
		if !skip[i] {
			out.Leagues = append(out.Leagues, c)
		}
	}
	if len(out.Leagues) == 0 {
		return Comparisons{}, fmt.Errorf("no team matching %q in any of my leagues", owner)
	}
	return out, nil
}

func (s *Service) compare(ctx context.Context, l sleeper.League, userID, owner string, week int, players map[string]sleeper.Player) (Comparison, error) {
	out := Comparison{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind()}

	var (
		rosters  []sleeper.Roster
		users    []sleeper.LeagueUser
		matchups []sleeper.Matchup
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if owner == "" {
		g.Go(func() (err error) { matchups, err = s.api.Matchups(gctx, l.LeagueID, week); return err })
	}
	if err := g.Wait(); err != nil {
		return out, err
	}

	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}

	var them sleeper.Roster
	if owner != "" {
		found, err := FindOwner(rosters, users, owner)
		if err != nil {
			return out, err
		}
		them = found
	} else {
		if l.Kind() == "guillotine" {
			out.Note = "guillotine leagues have no opponent; name an owner to compare"
			return out, nil
		}
		me, _ := find(matchups, mine.RosterID)
		opp, ok := Opponent(matchups, me)
		if !ok {
			out.Note = fmt.Sprintf("no opponent in week %d (bye)", week)
			return out, nil
		}
		for _, r := range rosters {
			if r.RosterID == opp.RosterID {
				them = r
			}
		}
	}
	if them.RosterID == mine.RosterID {
		return out, errors.New("that is my own team")
	}

	out.Me, out.Them = teamRecord(mine, users), teamRecord(them, users)
	out.Positions = sideBySide(mine, them, l.RosterPositions, players)
	return out, nil
}

func teamRecord(r sleeper.Roster, users []sleeper.LeagueUser) *TeamRecord {
	st := r.Settings
	tr := &TeamRecord{
		Team:      TeamName(users, r.OwnerID),
		Record:    fmt.Sprintf("%d-%d-%d", st.Wins, st.Losses, st.Ties),
		PointsFor: st.PointsFor(),
	}
	for _, u := range users {
		if u.UserID == r.OwnerID {
			tr.Owner = u.DisplayName
		}
	}
	return tr
}

// sideBySide groups both rosters by position in Positions order, then any
// other positions alphabetically.
func sideBySide(mine, them sleeper.Roster, slots []string, players map[string]sleeper.Player) []PositionCompare {
	group := func(r sleeper.Roster) map[string][]RosterPlayer {
		starters, bench, ir, _ := split(r, slots, players)
		byPos := make(map[string][]RosterPlayer)
		for _, p := range starters {
			if p.PlayerID != "" { // skip empty slots
				byPos[p.Position] = append(byPos[p.Position], p)
			}
		}
		for _, p := range bench {
			byPos[p.Position] = append(byPos[p.Position], p)
		}
		for _, p := range ir {
			p.Slot = "IR"
			byPos[p.Position] = append(byPos[p.Position], p)
		}
		return byPos
	}
	a, b := group(mine), group(them)

	var order []string
	order = append(order, Positions...)
	var extra []string
	for _, m := range []map[string][]RosterPlayer{a, b} {
		for pos := range m {
			if !slices.Contains(order, pos) && !slices.Contains(extra, pos) {
				extra = append(extra, pos)
			}
		}
	}
	slices.Sort(extra)
	order = append(order, extra...)

	var out []PositionCompare
	for _, pos := range order {
		if len(a[pos]) == 0 && len(b[pos]) == 0 {
			continue
		}
		pc := PositionCompare{Position: pos, Mine: a[pos], Theirs: b[pos]}
		if pc.Mine == nil {
			pc.Mine = []RosterPlayer{}
		}
		if pc.Theirs == nil {
			pc.Theirs = []RosterPlayer{}
		}
		out = append(out, pc)
	}
	return out
}
