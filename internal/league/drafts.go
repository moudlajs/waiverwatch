package league

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// maxSeasonsBack bounds how far a dynasty league's history is followed.
const maxSeasonsBack = 5

// DraftReport is the user's draft picks across leagues.
type DraftReport struct {
	Player          string         `json:"player,omitempty"`
	LeaguesSearched int            `json:"leagues_searched"`
	MyPicks         int            `json:"my_picks" jsonschema:"picks listed; with a player filter, how many times I drafted him"`
	Leagues         []DraftHistory `json:"leagues"`
}

// DraftHistory is one league's drafts. Dynasty leagues include earlier
// seasons (the startup draft and past rookie drafts).
type DraftHistory struct {
	LeagueID string         `json:"league_id"`
	League   string         `json:"league"`
	Kind     string         `json:"kind"`
	Drafts   []DraftSummary `json:"drafts"`
	Error    string         `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// DraftSummary is one draft and the user's picks in it.
type DraftSummary struct {
	Season string   `json:"season"`
	Type   string   `json:"type" jsonschema:"snake, linear or auction"`
	Status string   `json:"status"`
	Rounds int      `json:"rounds"`
	Picks  []MyPick `json:"picks"`
}

// MyPick is a player the user drafted.
type MyPick struct {
	Round     int    `json:"round"`
	PickNo    int    `json:"pick_no" jsonschema:"overall pick number"`
	PlayerID  string `json:"player_id"`
	Name      string `json:"name"`
	Position  string `json:"position,omitempty"`
	NFLTeam   string `json:"nfl_team,omitempty"`
	Cost      int    `json:"cost,omitempty" jsonschema:"auction drafts: the price paid"`
	Keeper    bool   `json:"keeper,omitempty"`
	StillMine bool   `json:"still_mine" jsonschema:"on my roster in this league today"`
}

// Drafts lists the user's draft picks in leagues matching leagueQuery (empty
// = all), optionally only picks whose player name contains player. Dynasty
// leagues include drafts from earlier seasons of the same league.
func (s *Service) Drafts(ctx context.Context, leagueQuery, player string) (DraftReport, error) {
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return DraftReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return DraftReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return DraftReport{}, err
	}

	all := make([]DraftHistory, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		ld, err := s.leagueDrafts(ctx, l, user.UserID, players)
		if err != nil {
			ld.Error = err.Error()
		}
		all[i] = ld
	})

	out := DraftReport{Player: player, LeaguesSearched: len(leagues), Leagues: []DraftHistory{}}
	for _, ld := range all {
		ld = filterPicks(ld, player)
		if player != "" && len(ld.Drafts) == 0 && ld.Error == "" {
			continue // searching for a player: leave out leagues where I never drafted him
		}
		for _, d := range ld.Drafts {
			out.MyPicks += len(d.Picks)
		}
		out.Leagues = append(out.Leagues, ld)
	}
	return out, nil
}

func (s *Service) leagueDrafts(ctx context.Context, l sleeper.League, userID string, players map[string]sleeper.Player) (DraftHistory, error) {
	out := DraftHistory{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Drafts: []DraftSummary{}}

	rosters, err := s.api.Rosters(ctx, l.LeagueID)
	if err != nil {
		return out, err
	}
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	onRoster := Rostered([]sleeper.Roster{mine})

	// This season's league, then, for dynasty, the same league's earlier
	// seasons: that is where the startup draft lives.
	leagueID, prev := l.LeagueID, l.PreviousID
	for depth := 0; ; depth++ {
		drafts, err := s.api.Drafts(ctx, leagueID)
		if err != nil {
			return out, err
		}
		for _, d := range drafts {
			picks, err := s.api.DraftPicks(ctx, d.DraftID)
			if err != nil {
				return out, err
			}
			// Roster IDs only identify me in the current season's league.
			myRosterID := 0
			if depth == 0 {
				myRosterID = mine.RosterID
			}
			out.Drafts = append(out.Drafts, DraftSummary{
				Season: d.Season, Type: d.Type, Status: d.Status, Rounds: d.Settings.Rounds,
				Picks: myPicks(picks, userID, myRosterID, onRoster, players),
			})
		}
		if l.Kind() != "dynasty" || prev == "" || depth+1 >= maxSeasonsBack {
			break
		}
		earlier, err := s.api.League(ctx, prev)
		if err != nil {
			return out, err
		}
		leagueID, prev = earlier.LeagueID, earlier.PreviousID
	}
	return out, nil
}

// myPicks keeps the picks the user made: picked_by is the user, or, when
// Sleeper left it empty (auto-picks), the pick belonged to myRosterID
// (0 = don't guess).
func myPicks(picks []sleeper.Pick, userID string, myRosterID int, onRoster map[string]bool, players map[string]sleeper.Player) []MyPick {
	out := []MyPick{}
	for _, p := range picks {
		if p.PickedBy != userID && (p.PickedBy != "" || myRosterID == 0 || p.RosterID != myRosterID) {
			continue
		}
		pl, known := players[p.PlayerID]
		name, pos, team := pl.Name(), pl.Position, pl.Team
		if !known { // retired or unknown to the dictionary: the draft remembers
			name = strings.TrimSpace(p.Metadata.FirstName + " " + p.Metadata.LastName)
			pos, team = p.Metadata.Position, p.Metadata.Team
			if name == "" {
				name = p.PlayerID
			}
		}
		cost, _ := strconv.Atoi(p.Metadata.Amount)
		out = append(out, MyPick{
			Round: p.Round, PickNo: p.PickNo, PlayerID: p.PlayerID, Name: name, Position: pos,
			NFLTeam: team, Cost: cost, Keeper: p.IsKeeper, StillMine: onRoster[p.PlayerID],
		})
	}
	return out
}

// filterPicks keeps only picks whose player name contains player (ignoring
// case), and only drafts with such picks. An empty player keeps everything.
func filterPicks(ld DraftHistory, player string) DraftHistory {
	if player == "" {
		return ld
	}
	q := strings.ToLower(strings.TrimSpace(player))
	var drafts []DraftSummary
	for _, d := range ld.Drafts {
		var picks []MyPick
		for _, p := range d.Picks {
			if strings.Contains(strings.ToLower(p.Name), q) {
				picks = append(picks, p)
			}
		}
		if len(picks) > 0 {
			d.Picks = picks
			drafts = append(drafts, d)
		}
	}
	if drafts == nil {
		drafts = []DraftSummary{}
	}
	ld.Drafts = drafts
	return ld
}
