package league

import (
	"cmp"
	"context"
	"slices"
)

// severity orders injury statuses, most serious first. Unlisted statuses
// (e.g. COV, DNR) sort after these.
var severity = []string{"Out", "IR", "PUP", "Sus", "Doubtful", "Questionable", "NA"}

// InjuryReport is every injured player on the user's rosters.
type InjuryReport struct {
	Players       []InjuredPlayer `json:"players"`
	FailedLeagues []Failure       `json:"failed_leagues,omitempty"`
}

// InjuredPlayer is one injured player and every league where he is mine.
type InjuredPlayer struct {
	PlayerID   string         `json:"player_id"`
	Name       string         `json:"name"`
	Position   string         `json:"position"`
	NFLTeam    string         `json:"nfl_team,omitempty"`
	Status     string         `json:"status" jsonschema:"Out, IR, PUP, Sus(pended), Doubtful, Questionable, NA..."`
	BodyPart   string         `json:"body_part,omitempty"`
	Leagues    []InjuryLeague `json:"leagues"`
	StartingIn int            `json:"starting_in" jsonschema:"how many of my lineups he is currently in"`
}

// InjuryLeague is where an injured player sits in one league.
type InjuryLeague struct {
	League      string  `json:"league"`
	Starting    bool    `json:"starting"`
	OnIR        bool    `json:"on_ir"`
	Replacement *Target `json:"replacement,omitempty" jsonschema:"when starting: the best available player at his position in that league"`
}

// Injuries lists injured players on the user's rosters across all leagues,
// most serious and most-started first. Where one is starting, it suggests
// the best available replacement at his position in that league.
func (s *Service) Injuries(ctx context.Context) (InjuryReport, error) {
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return InjuryReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return InjuryReport{}, err
	}
	trending, err := s.api.TrendingAdds(ctx, 24, maxTrending)
	if err != nil {
		return InjuryReport{}, err
	}
	adds := make(map[string]int, len(trending))
	for _, t := range trending {
		adds[t.PlayerID] = t.Count
	}

	out := InjuryReport{Players: []InjuredPlayer{}}
	byID := make(map[string]*InjuredPlayer)
	for _, p := range s.pools(ctx, leagues, user.UserID) {
		if p.err != nil {
			out.FailedLeagues = append(out.FailedLeagues, Failure{League: p.league.Name, Error: p.err.Error()})
			continue
		}
		for id := range p.mine {
			pl := Lookup(players, id)
			if pl.InjuryStatus == "" {
				continue
			}
			ip, ok := byID[id]
			if !ok {
				ip = &InjuredPlayer{
					PlayerID: id, Name: pl.Name(), Position: pl.Position, NFLTeam: pl.Team,
					Status: pl.InjuryStatus, BodyPart: pl.InjuryBodyPart,
				}
				byID[id] = ip
			}
			il := InjuryLeague{
				League:   p.league.Name,
				Starting: slices.Contains(p.me.Starters, id),
				OnIR:     slices.Contains(p.me.Reserve, id),
			}
			if il.Starting {
				ip.StartingIn++
				if best := targets(players, p.rostered, map[string]bool{pl.Position: true}, adds, 1); len(best) > 0 {
					il.Replacement = &best[0]
				}
			}
			ip.Leagues = append(ip.Leagues, il)
		}
	}

	for _, ip := range byID {
		slices.SortFunc(ip.Leagues, func(a, b InjuryLeague) int { return cmp.Compare(a.League, b.League) })
		out.Players = append(out.Players, *ip)
	}
	slices.SortFunc(out.Players, func(a, b InjuredPlayer) int {
		return cmp.Or(
			cmp.Compare(severityRank(a.Status), severityRank(b.Status)),
			cmp.Compare(b.StartingIn, a.StartingIn),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return out, nil
}

func severityRank(status string) int {
	if i := slices.Index(severity, status); i >= 0 {
		return i
	}
	return len(severity)
}
