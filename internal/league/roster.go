package league

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// RosterReport is one or more teams' rosters.
type RosterReport struct {
	Rosters []TeamRoster `json:"rosters"`
}

// TeamRoster is one team's roster in one league, by lineup status.
type TeamRoster struct {
	LeagueID string         `json:"league_id"`
	League   string         `json:"league"`
	Kind     string         `json:"kind"`
	Team     string         `json:"team,omitempty"`
	Owner    string         `json:"owner,omitempty"`
	Starters []RosterPlayer `json:"starters"`
	Bench    []RosterPlayer `json:"bench"`
	IR       []RosterPlayer `json:"ir,omitempty"`
	Taxi     []RosterPlayer `json:"taxi,omitempty"`
	Error    string         `json:"error,omitempty" jsonschema:"set when this league could not be loaded or the owner was not found; the others are still valid"`
}

// RosterPlayer is a player on a roster.
type RosterPlayer struct {
	Slot       string `json:"slot,omitempty" jsonschema:"starters only: the lineup slot, e.g. QB, FLEX"`
	PlayerID   string `json:"player_id"`
	Name       string `json:"name"`
	Position   string `json:"position,omitempty"`
	NFLTeam    string `json:"nfl_team,omitempty"`
	Injury     string `json:"injury,omitempty"`
	InjuryPart string `json:"injury_part,omitempty"`
}

// Rosters returns the rosters matching leagueQuery (name fragment or ID;
// empty = all leagues) and owner (team or display name; empty = mine). With
// an owner but no league, leagues where nobody matches are left out.
func (s *Service) Rosters(ctx context.Context, leagueQuery, owner string) (RosterReport, error) {
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return RosterReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return RosterReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return RosterReport{}, err
	}

	all := make([]TeamRoster, len(leagues))
	skip := make([]bool, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		tr, err := s.roster(ctx, l, user.UserID, owner, players)
		var nf notFoundError
		switch {
		case err == nil:
		case errors.As(err, &nf) && leagueQuery == "":
			skip[i] = true // searching every league for an owner: absence is expected
		default:
			tr.Error = err.Error()
		}
		all[i] = tr
	})

	out := RosterReport{Rosters: []TeamRoster{}}
	for i, tr := range all {
		if !skip[i] {
			out.Rosters = append(out.Rosters, tr)
		}
	}
	if len(out.Rosters) == 0 {
		return RosterReport{}, fmt.Errorf("no team matching %q in any of my leagues", owner)
	}
	return out, nil
}

func (s *Service) roster(ctx context.Context, l sleeper.League, userID, owner string, players map[string]sleeper.Player) (TeamRoster, error) {
	out := TeamRoster{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Starters: []RosterPlayer{}, Bench: []RosterPlayer{}}

	var (
		rosters []sleeper.Roster
		users   []sleeper.LeagueUser
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if err := g.Wait(); err != nil {
		return out, err
	}

	var r sleeper.Roster
	if owner == "" {
		mine, ok := MyRoster(rosters, userID)
		if !ok {
			return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
		}
		r = mine
	} else {
		found, err := FindOwner(rosters, users, owner)
		if err != nil {
			return out, err
		}
		r = found
	}

	out.Team = TeamName(users, r.OwnerID)
	for _, u := range users {
		if u.UserID == r.OwnerID {
			out.Owner = u.DisplayName
		}
	}
	out.Starters, out.Bench, out.IR, out.Taxi = split(r, l.RosterPositions, players)
	return out, nil
}

// notFoundError means no team in a league matched the owner query.
type notFoundError struct{ msg string }

func (e notFoundError) Error() string { return e.msg }

// FindOwner finds the roster whose owner or co-owner matches query by team
// name or display name: an exact match (ignoring case) first, else a single
// partial match. The error lists the league's teams so the caller can retry.
func FindOwner(rosters []sleeper.Roster, users []sleeper.LeagueUser, query string) (sleeper.Roster, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	names := func(u sleeper.LeagueUser) []string {
		return []string{strings.ToLower(u.DisplayName), strings.ToLower(u.Metadata.TeamName)}
	}
	var exact, partial []sleeper.Roster
	var teams []string
	for _, r := range rosters {
		owners := append([]string{r.OwnerID}, r.CoOwners...)
		teams = append(teams, TeamName(users, r.OwnerID))
		isExact, isPartial := false, false
		for _, u := range users {
			if !slices.Contains(owners, u.UserID) {
				continue
			}
			for _, n := range names(u) {
				isExact = isExact || (n != "" && n == q)
				isPartial = isPartial || (n != "" && strings.Contains(n, q))
			}
		}
		if isExact {
			exact = append(exact, r)
		} else if isPartial {
			partial = append(partial, r)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) == 0 && len(partial) == 1:
		return partial[0], nil
	case len(exact)+len(partial) == 0:
		return sleeper.Roster{}, notFoundError{fmt.Sprintf("no team matches %q; teams: %s", query, strings.Join(teams, "; "))}
	default:
		return sleeper.Roster{}, fmt.Errorf("%q matches more than one team; teams: %s", query, strings.Join(teams, "; "))
	}
}

// split sorts a roster's players into starters (with their lineup slot),
// bench, IR and taxi.
func split(r sleeper.Roster, slots []string, players map[string]sleeper.Player) (starters, bench, ir, taxi []RosterPlayer) {
	player := func(id, slot string) RosterPlayer {
		p := Lookup(players, id)
		return RosterPlayer{
			Slot: slot, PlayerID: id, Name: p.Name(), Position: p.Position,
			NFLTeam: p.Team, Injury: p.InjuryStatus, InjuryPart: p.InjuryBodyPart,
		}
	}
	placed := make(map[string]bool)
	starters = []RosterPlayer{}
	for i, id := range r.Starters {
		slot := "?"
		if i < len(slots) {
			slot = slots[i]
		}
		if id == "0" || id == "" {
			starters = append(starters, RosterPlayer{Slot: slot, Name: "(empty)"})
			continue
		}
		starters = append(starters, player(id, slot))
		placed[id] = true
	}
	for _, id := range r.Reserve {
		ir = append(ir, player(id, ""))
		placed[id] = true
	}
	for _, id := range r.Taxi {
		taxi = append(taxi, player(id, ""))
		placed[id] = true
	}
	bench = []RosterPlayer{}
	for _, id := range r.Players {
		if !placed[id] {
			bench = append(bench, player(id, ""))
		}
	}
	return starters, bench, ir, taxi
}
