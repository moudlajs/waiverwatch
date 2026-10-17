package league

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

const exposureLimit = 25

// Exposure is how much my lineups depend on single players and NFL teams across leagues.
type Exposure struct {
	Week     int               `json:"week"`
	Leagues  int               `json:"leagues" jsonschema:"leagues counted (eliminated guillotine teams are skipped)"`
	Players  []ExposedPlayer   `json:"players" jsonschema:"players I have in two or more leagues, or start in any, most started first"`
	NFLTeams []NFLTeamExposure `json:"nfl_teams" jsonschema:"NFL teams by how many of my starters they supply this week, most first"`
	Note     string            `json:"note,omitempty"`
}

// ExposedPlayer is one player across my leagues.
type ExposedPlayer struct {
	Name       string   `json:"name"`
	Position   string   `json:"position,omitempty"`
	NFLTeam    string   `json:"nfl_team,omitempty"`
	Injury     string   `json:"injury,omitempty"`
	OnBye      bool     `json:"on_bye,omitempty" jsonschema:"the player's NFL team has no game projected this week"`
	Rostered   int      `json:"rostered" jsonschema:"leagues where the player is on my roster"`
	Starting   int      `json:"starting" jsonschema:"leagues where the player is in my lineup this week"`
	StartingIn []string `json:"starting_in,omitempty"`
}

// NFLTeamExposure counts my starters from one NFL team.
type NFLTeamExposure struct {
	NFLTeam  string `json:"nfl_team"`
	Starters int    `json:"starters"`
	OnBye    bool   `json:"on_bye,omitempty"`
}

// PlayerExposure finds the players and NFL teams my lineups lean on across all leagues.
func (s *Service) PlayerExposure(ctx context.Context) (Exposure, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return Exposure{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return Exposure{}, err
	}
	out := Exposure{Week: state.Week, Players: []ExposedPlayer{}, NFLTeams: []NFLTeamExposure{}}

	seasonType := projectionSeason(state)
	var playing map[string]bool
	if proj, err := s.api.Projections(ctx, seasonType, state.Season, state.Week); err != nil {
		out.Note = "byes unknown: no projections (" + err.Error() + ")"
	} else if playing = playingTeams(proj, players); len(playing) == 0 {
		playing = nil
		out.Note = "byes unknown: no games are projected this week"
	}

	mine := make([]sleeper.Roster, len(leagues))
	errs := make([]error, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		rosters, err := s.api.Rosters(ctx, l.LeagueID)
		if err != nil {
			errs[i] = err
			return
		}
		mine[i], _ = MyRoster(rosters, user.UserID)
	})

	byID := make(map[string]*ExposedPlayer)
	teams := make(map[string]int)
	for i, r := range mine {
		if errs[i] != nil {
			addNote(&out.Note, leagues[i].Name+" skipped: "+errs[i].Error())
			continue
		}
		if len(r.Players) == 0 {
			continue
		}
		out.Leagues++
		for _, id := range r.Players {
			e, ok := byID[id]
			if !ok {
				p := Lookup(players, id)
				e = &ExposedPlayer{Name: p.Name(), Position: p.Position, NFLTeam: p.Team, Injury: p.InjuryStatus}
				e.OnBye = playing != nil && p.Team != "" && !playing[p.Team]
				byID[id] = e
			}
			e.Rostered++
			if slices.Contains(r.Starters, id) {
				e.Starting++
				e.StartingIn = append(e.StartingIn, leagues[i].Name)
				if e.NFLTeam != "" {
					teams[e.NFLTeam]++
				}
			}
		}
	}

	for _, e := range byID {
		if e.Rostered >= 2 || e.Starting > 0 {
			out.Players = append(out.Players, *e)
		}
	}
	slices.SortFunc(out.Players, func(a, b ExposedPlayer) int {
		return cmp.Or(cmp.Compare(b.Starting, a.Starting), cmp.Compare(b.Rostered, a.Rostered), cmp.Compare(a.Name, b.Name))
	})
	if len(out.Players) > exposureLimit {
		addNote(&out.Note, fmt.Sprintf("showing the %d most-used of %d players", exposureLimit, len(out.Players)))
		out.Players = out.Players[:exposureLimit]
	}
	for team, n := range teams {
		out.NFLTeams = append(out.NFLTeams, NFLTeamExposure{NFLTeam: team, Starters: n, OnBye: playing != nil && !playing[team]})
	}
	slices.SortFunc(out.NFLTeams, func(a, b NFLTeamExposure) int {
		return cmp.Or(cmp.Compare(b.Starters, a.Starters), cmp.Compare(a.NFLTeam, b.NFLTeam))
	})
	out.NFLTeams = out.NFLTeams[:min(len(out.NFLTeams), 8)]
	return out, nil
}
