package league

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// FetchValues returns a trade value market keyed by Sleeper player ID,
// normally (*fantasycalc.Client).Values.
type FetchValues func(ctx context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error)

// ValueSource credits where values come from; answers carry it.
const ValueSource = "FantasyCalc (fantasycalc.com): values from real fantasy trades"

// maxMatches caps the players one name can match (e.g. "Smith").
const maxMatches = 5

// ValueReport is players' trade values, per league.
type ValueReport struct {
	Source   string       `json:"source"`
	NotFound []string     `json:"not_found,omitempty" jsonschema:"player names that matched no NFL player"`
	Leagues  []ValueBoard `json:"leagues"`
}

// ValueBoard is the values in one league's market: named players, or one
// team's roster.
type ValueBoard struct {
	LeagueID string        `json:"league_id"`
	League   string        `json:"league"`
	Kind     string        `json:"kind"`
	Market   string        `json:"market" jsonschema:"the FantasyCalc market these values come from, e.g. dynasty superflex 10-team PPR"`
	Team     string        `json:"team,omitempty" jsonschema:"roster values: whose roster"`
	Owner    string        `json:"owner,omitempty"`
	Total    int           `json:"total,omitempty" jsonschema:"roster values: the roster's summed value"`
	Rank     int           `json:"rank,omitempty" jsonschema:"roster values: this roster's rank by total value in the league, 1 is the most valuable"`
	Ranking  []TeamValue   `json:"ranking,omitempty" jsonschema:"roster values: every team in the league by total value"`
	Players  []PlayerValue `json:"players"`
	Note     string        `json:"note,omitempty"`
	Error    string        `json:"error,omitempty" jsonschema:"set when this league could not be loaded or the owner was not found; the others are still valid"`
}

// TeamValue is one team's summed roster value.
type TeamValue struct {
	Rank  int    `json:"rank" jsonschema:"1 is the most valuable roster; tied teams share a rank"`
	Team  string `json:"team"`
	Total int    `json:"total"`
	Mine  bool   `json:"mine,omitempty"`
}

// PlayerValue is one player's trade value in a league's market.
type PlayerValue struct {
	PlayerID     string `json:"player_id"`
	Name         string `json:"name"`
	Position     string `json:"position,omitempty"`
	NFLTeam      string `json:"nfl_team,omitempty"`
	Team         string `json:"team,omitempty" jsonschema:"named players: the fantasy team he is on in this league; empty for a free agent"`
	Mine         bool   `json:"mine,omitempty" jsonschema:"named players: he is on my team"`
	Value        int    `json:"value" jsonschema:"trade value, up to ~11000; 0 when unrated"`
	Unrated      bool   `json:"unrated,omitempty" jsonschema:"FantasyCalc doesn't rate him (kickers, defenses, deep bench): no market value, not proven worthless"`
	OverallRank  int    `json:"overall_rank,omitempty"`
	PositionRank int    `json:"position_rank,omitempty"`
	Tier         int    `json:"tier,omitempty" jsonschema:"1 is the top tier"`
	Trend30Day   int    `json:"trend_30_day,omitempty" jsonschema:"value change over the last 30 days"`
}

// ValueSettings picks the FantasyCalc market that fits a league. Keeper and
// guillotine leagues use redraft values; a league that can start two QBs
// (QB and superflex slots) uses superflex values.
func ValueSettings(l sleeper.League) fantasycalc.Settings {
	qbs := 0
	for _, slot := range l.RosterPositions {
		if slot == "QB" || slot == "SUPER_FLEX" {
			qbs++
		}
	}
	return fantasycalc.Settings{
		Dynasty: l.Kind() == "dynasty",
		QBs:     qbs,
		Teams:   l.TotalRosters,
		PPR:     l.Scoring.Rec,
	}
}

// Values answers trade value questions in each league matching leagueQuery
// (empty = all). With names, it values those players in every league and
// says who has them; without, it values a whole roster: owner's (team or
// display name), or mine when owner is empty.
func (s *Service) Values(ctx context.Context, leagueQuery, owner string, names []string) (ValueReport, error) {
	if s.values == nil {
		return ValueReport{}, errors.New("trade values are not set up on this server")
	}
	if owner != "" && len(names) > 0 {
		return ValueReport{}, errors.New("name players or an owner, not both: named players are shown on whichever team has them")
	}
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return ValueReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return ValueReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return ValueReport{}, err
	}

	out := ValueReport{Source: ValueSource, Leagues: []ValueBoard{}}
	var wanted []sleeper.Player
	for _, n := range names {
		found := findPlayers(players, n)
		if len(found) == 0 {
			out.NotFound = append(out.NotFound, n)
		}
		for _, p := range found {
			if !slices.ContainsFunc(wanted, func(w sleeper.Player) bool { return w.PlayerID == p.PlayerID }) {
				wanted = append(wanted, p)
			}
		}
	}
	if len(names) > 0 && len(wanted) == 0 {
		return ValueReport{}, fmt.Errorf("no NFL player matches %s", strings.Join(names, ", "))
	}

	all := make([]ValueBoard, len(leagues))
	skip := make([]bool, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		lv, err := s.leagueValues(ctx, l, user.UserID, owner, wanted, players)
		var nf notFoundError
		switch {
		case err == nil:
		case errors.As(err, &nf) && leagueQuery == "":
			skip[i] = true // searching every league for an owner: absence is expected
		default:
			lv.Error = err.Error()
		}
		all[i] = lv
	})
	for i, lv := range all {
		if !skip[i] {
			out.Leagues = append(out.Leagues, lv)
		}
	}
	if len(out.Leagues) == 0 {
		if owner == "" {
			return ValueReport{}, errors.New("no leagues this season")
		}
		return ValueReport{}, fmt.Errorf("no team matching %q in any of my leagues", owner)
	}
	return out, nil
}

func (s *Service) leagueValues(ctx context.Context, l sleeper.League, userID, owner string, wanted []sleeper.Player, players map[string]sleeper.Player) (ValueBoard, error) {
	settings := ValueSettings(l).Normalise()
	out := ValueBoard{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Market: settings.String(), Players: []PlayerValue{}}
	if k := l.Kind(); k == "keeper" || k == "guillotine" {
		out.Note = k + " league: valued with redraft values"
	}

	var (
		market  map[string]fantasycalc.Value
		rosters []sleeper.Roster
		users   []sleeper.LeagueUser
	)
	g, gctx := errgroup.WithContext(ctx)
	var source string
	g.Go(func() (err error) { market, source, err = s.market(gctx, settings); return err })
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if err := g.Wait(); err != nil {
		return out, err
	}
	addNote(&out.Note, source)
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}

	if len(wanted) > 0 {
		for _, p := range wanted {
			pv := playerValue(p, market)
			for _, r := range rosters {
				if slices.Contains(r.Players, p.PlayerID) {
					pv.Team, pv.Mine = TeamName(users, r.OwnerID), r.RosterID == mine.RosterID
				}
			}
			out.Players = append(out.Players, pv)
		}
		return out, nil
	}

	r := mine
	if owner != "" {
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
	for _, id := range r.Players {
		pv := playerValue(Lookup(players, id), market)
		out.Total += pv.Value
		out.Players = append(out.Players, pv)
	}
	slices.SortStableFunc(out.Players, func(a, b PlayerValue) int { return cmp.Compare(b.Value, a.Value) })
	var ranks map[int]int
	out.Ranking, ranks = valueRanking(rosters, users, market, mine.RosterID)
	out.Rank = ranks[r.RosterID]
	if len(r.Players) == 0 && l.Kind() == "guillotine" {
		addNote(&out.Note, "eliminated: guillotine teams are emptied when they are cut")
	}
	return out, nil
}

func playerValue(p sleeper.Player, market map[string]fantasycalc.Value) PlayerValue {
	v, rated := market[p.PlayerID]
	return PlayerValue{
		PlayerID: p.PlayerID, Name: p.Name(), Position: p.Position, NFLTeam: p.Team, Unrated: !rated,
		Value: v.Value, OverallRank: v.OverallRank, PositionRank: v.PositionRank, Tier: v.Tier, Trend30Day: v.Trend30Day,
	}
}

// findPlayers returns the players at a fantasy position whose name contains
// query, ignoring case and punctuation ("jamarr" finds Ja'Marr Chase): an
// exact name first, else up to maxMatches, active and best ranked first.
func findPlayers(players map[string]sleeper.Player, query string) []sleeper.Player {
	q := foldName(query)
	if q == "" {
		return nil
	}
	var exact, partial []sleeper.Player
	for _, p := range players {
		if !slices.Contains(Positions, p.Position) {
			continue
		}
		switch n := foldName(p.Name()); {
		case n == q:
			exact = append(exact, p)
		case strings.Contains(n, q):
			partial = append(partial, p)
		}
	}
	if len(exact) > 0 {
		partial = exact
	}
	slices.SortFunc(partial, func(a, b sleeper.Player) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.PlayerID, b.PlayerID))
	})
	return partial[:min(len(partial), maxMatches)]
}

// rank orders by search_rank with unranked players last.
func rank(p sleeper.Player) int {
	if p.SearchRank <= 0 {
		return int(^uint(0) >> 1)
	}
	return p.SearchRank
}

// foldName lowercases a name and keeps only letters, digits and single
// spaces.
func foldName(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case unicode.IsSpace(r):
			space = true
		}
	}
	return b.String()
}

// rosterValue sums the values of the given players.
func rosterValue(ids []string, market map[string]fantasycalc.Value) int {
	total := 0
	for _, id := range ids {
		total += market[id].Value
	}
	return total
}

// valueRanking orders a league's teams by summed roster value, most valuable
// first; tied teams share a rank. Empty rosters (teams cut from a guillotine
// league) are left out. ranks maps roster IDs to their rank (team names can
// be empty or shared).
func valueRanking(rosters []sleeper.Roster, users []sleeper.LeagueUser, market map[string]fantasycalc.Value, mineID int) (out []TeamValue, ranks map[int]int) {
	sorted := slices.DeleteFunc(slices.Clone(rosters), func(r sleeper.Roster) bool { return len(r.Players) == 0 })
	slices.SortStableFunc(sorted, func(a, b sleeper.Roster) int {
		return cmp.Compare(rosterValue(b.Players, market), rosterValue(a.Players, market))
	})
	ranks = make(map[int]int, len(sorted))
	for i, r := range sorted {
		tv := TeamValue{Rank: i + 1, Team: TeamName(users, r.OwnerID), Total: rosterValue(r.Players, market), Mine: r.RosterID == mineID}
		if i > 0 && tv.Total == out[i-1].Total {
			tv.Rank = out[i-1].Rank
		}
		ranks[r.RosterID] = tv.Rank
		out = append(out, tv)
	}
	return out, ranks
}
