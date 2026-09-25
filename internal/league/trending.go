package league

import (
	"context"
	"fmt"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// maxTrending is the most players Sleeper's trending endpoint returns,
// whatever limit is asked for.
const maxTrending = 100

// TrendingReport is the most-added players and where the user can claim them.
type TrendingReport struct {
	LookbackHours int              `json:"lookback_hours"`
	Players       []TrendingPlayer `json:"players"`
	FailedLeagues []Failure        `json:"failed_leagues,omitempty" jsonschema:"leagues whose rosters could not be loaded; availability there is unknown"`
}

// TrendingPlayer is one trending player.
type TrendingPlayer struct {
	PlayerID     string   `json:"player_id"`
	Name         string   `json:"name"`
	Position     string   `json:"position"`
	NFLTeam      string   `json:"nfl_team,omitempty"`
	Injury       string   `json:"injury,omitempty"`
	Adds         int      `json:"adds" jsonschema:"adds across all Sleeper leagues in the lookback window"`
	AvailableIn  []string `json:"available_in" jsonschema:"my leagues where no team rosters this player"`
	OnMyRosterIn []string `json:"on_my_roster_in,omitempty"`
}

// Failure is a league that could not be loaded.
type Failure struct {
	League string `json:"league"`
	Error  string `json:"error"`
}

// Trending returns up to limit of the most-added players over lookbackHours,
// optionally only at position, each with the user's leagues where the player
// is available.
func (s *Service) Trending(ctx context.Context, lookbackHours, limit int, position string) (TrendingReport, error) {
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return TrendingReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return TrendingReport{}, err
	}
	// Filtering by position after the fact needs the whole list.
	fetch := limit
	if position != "" {
		fetch = maxTrending
	}
	trending, err := s.api.TrendingAdds(ctx, lookbackHours, fetch)
	if err != nil {
		return TrendingReport{}, err
	}

	pools := s.pools(ctx, leagues, user.UserID)

	out := TrendingReport{LookbackHours: lookbackHours, Players: []TrendingPlayer{}}
	for _, p := range pools {
		if p.err != nil {
			out.FailedLeagues = append(out.FailedLeagues, Failure{League: p.league.Name, Error: p.err.Error()})
		}
	}
	for _, t := range trending {
		pl := Lookup(players, t.PlayerID)
		if position != "" && pl.Position != position {
			continue
		}
		tp := TrendingPlayer{
			PlayerID: t.PlayerID, Name: pl.Name(), Position: pl.Position, NFLTeam: pl.Team,
			Injury: pl.InjuryStatus, Adds: t.Count, AvailableIn: []string{},
		}
		for _, p := range pools {
			switch {
			case p.err != nil:
			case p.mine[t.PlayerID]:
				tp.OnMyRosterIn = append(tp.OnMyRosterIn, p.league.Name)
			case !p.rostered[t.PlayerID]:
				tp.AvailableIn = append(tp.AvailableIn, p.league.Name)
			}
		}
		out.Players = append(out.Players, tp)
		if len(out.Players) == limit {
			break
		}
	}
	return out, nil
}

// pool is who is taken in one league.
type pool struct {
	league   sleeper.League
	rostered map[string]bool // on any team, including mine
	me       sleeper.Roster
	mine     map[string]bool
	err      error
}

// pools loads every league's rosters concurrently.
func (s *Service) pools(ctx context.Context, leagues []sleeper.League, userID string) []pool {
	out := make([]pool, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		out[i].league = l
		rosters, err := s.api.Rosters(ctx, l.LeagueID)
		if err != nil {
			out[i].err = err
			return
		}
		out[i].rostered = Rostered(rosters)
		if mine, ok := MyRoster(rosters, userID); ok {
			out[i].me = mine
			out[i].mine = Rostered([]sleeper.Roster{mine})
		} else {
			out[i].err = fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
		}
	})
	return out
}

// Rostered is the set of player IDs on any of rosters, including taxi squads
// and IR.
func Rostered(rosters []sleeper.Roster) map[string]bool {
	set := make(map[string]bool)
	for _, r := range rosters {
		for _, list := range [][]string{r.Players, r.Taxi, r.Reserve} {
			for _, id := range list {
				set[id] = true
			}
		}
	}
	return set
}
