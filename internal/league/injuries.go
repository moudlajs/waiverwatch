package league

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// severity orders injury statuses, most serious first; unlisted ones sort last.
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

// Injuries lists injured players on the user's rosters, with replacements for starters.
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
	illegal := make(map[string][]string) // player ID -> leagues where he's illegally on IR
	for _, p := range s.pools(ctx, leagues, user.UserID) {
		if p.err != nil {
			out.FailedLeagues = append(out.FailedLeagues, Failure{League: p.league.Name, Error: p.err.Error()})
			continue
		}
		for id := range p.mine {
			pl := Lookup(players, id)
			bad := illegalIR(p.league, p.me, players, id)
			if pl.InjuryStatus == "" && !bad {
				continue
			}
			if bad {
				illegal[id] = append(illegal[id], p.league.Name)
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
	// Illegal IR blocks roster moves, so it sorts first; the warning goes in status (output schemas are frozen, #139).
	slices.SortFunc(out.Players, func(a, b InjuredPlayer) int {
		return cmp.Or(
			cmp.Compare(boolRank(len(illegal[a.PlayerID]) > 0), boolRank(len(illegal[b.PlayerID]) > 0)),
			cmp.Compare(severityRank(a.Status), severityRank(b.Status)),
			cmp.Compare(b.StartingIn, a.StartingIn),
			cmp.Compare(a.Name, b.Name),
		)
	})
	for i, p := range out.Players {
		if leagues := illegal[p.PlayerID]; len(leagues) > 0 {
			out.Players[i].Status = fmt.Sprintf("%s; illegal IR in %s: not IR-eligible there, so Sleeper blocks adds, drops and trades until he's moved off IR",
				cmp.Or(p.Status, "healthy"), strings.Join(leagues, ", "))
		}
	}
	return out, nil
}

func severityRank(status string) int {
	if i := slices.Index(severity, status); i >= 0 {
		return i
	}
	return len(severity)
}

// irEligible says whether a player with status may stay on IR in a league with these settings.
func irEligible(s sleeper.LeagueSettings, status string) bool {
	switch status {
	case "IR", "PUP": // no setting: always allowed
		return true
	case "Out":
		return s.ReserveAllowOut == 1
	case "Doubtful":
		return s.ReserveAllowDoubtful == 1
	case "Sus":
		return s.ReserveAllowSus == 1
	case "NA":
		return s.ReserveAllowNA == 1
	case "COV":
		return s.ReserveAllowCOV == 1
	case "DNR":
		return s.ReserveAllowDNR == 1
	}
	return false
}

func illegalIR(l sleeper.League, r sleeper.Roster, players map[string]sleeper.Player, id string) bool {
	p, known := players[id]
	return known && slices.Contains(r.Reserve, id) && !irEligible(l.Settings, p.InjuryStatus)
}
