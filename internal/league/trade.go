package league

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Verdict thresholds: the adjusted margin as a share of the bigger side.
const (
	fairShare   = 0.05
	slightShare = 0.15
)

// TradeReport values a proposed trade in one league.
type TradeReport struct {
	Source       string       `json:"source"`
	LeagueID     string       `json:"league_id"`
	League       string       `json:"league"`
	Kind         string       `json:"kind"`
	Market       string       `json:"market" jsonschema:"the FantasyCalc market these values come from"`
	Partner      string       `json:"partner,omitempty" jsonschema:"the team the players I get are on; several when they come from more than one"`
	Give         []TradeAsset `json:"give"`
	Get          []TradeAsset `json:"get"`
	GiveValue    int          `json:"give_value" jsonschema:"summed value of what I give"`
	GetValue     int          `json:"get_value" jsonschema:"summed value of what I get"`
	GiveAdjusted int          `json:"give_adjusted" jsonschema:"give side after the consolidation adjustment"`
	GetAdjusted  int          `json:"get_adjusted" jsonschema:"get side after the consolidation adjustment"`
	Margin       int          `json:"margin" jsonschema:"get_adjusted minus give_adjusted: positive means I win the trade"`
	Verdict      string       `json:"verdict"`
	Notes        []string     `json:"notes,omitempty"`
}

// TradeAsset is a player or dynasty draft pick in a trade.
type TradeAsset struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	Position string `json:"position,omitempty" jsonschema:"PICK for draft picks"`
	NFLTeam  string `json:"nfl_team,omitempty"`
	Team     string `json:"team,omitempty" jsonschema:"the fantasy team he is on now; empty for a free agent or a pick"`
	Value    int    `json:"value"`
	Adjusted int    `json:"adjusted" jsonschema:"what he counts for after the consolidation adjustment"`
	Unrated  bool   `json:"unrated,omitempty" jsonschema:"FantasyCalc doesn't rate him; counted as 0"`
}

// EvaluateTrade values giving give for get in the league matching
// leagueQuery. Without a league, it is the one league where every player in
// give is on my roster.
func (s *Service) EvaluateTrade(ctx context.Context, leagueQuery string, give, get []string) (TradeReport, error) {
	if s.values == nil {
		return TradeReport{}, errors.New("trade values are not set up on this server")
	}
	if len(give) == 0 || len(get) == 0 {
		return TradeReport{}, errors.New("name at least one player or pick on each side")
	}
	if slices.ContainsFunc(slices.Concat(give, get), func(n string) bool { return foldName(n) == "" }) {
		return TradeReport{}, errors.New("a player or pick name is empty")
	}
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return TradeReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return TradeReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return TradeReport{}, err
	}

	if len(leagues) > 1 {
		if leagues, err = s.leaguesWithMine(ctx, leagues, user.UserID, give, players); err != nil {
			return TradeReport{}, err
		}
	}
	return s.evaluate(ctx, leagues[0], user.UserID, give, get, players)
}

// leaguesWithMine narrows leagues to the single one where every player in
// give is on my roster. Names that match no NFL player are taken for draft
// picks and don't narrow anything; a give of picks alone needs a league.
func (s *Service) leaguesWithMine(ctx context.Context, leagues []sleeper.League, userID string, give []string, players map[string]sleeper.Player) ([]sleeper.League, error) {
	var named []string
	for _, n := range give {
		if len(findPlayers(players, n)) > 0 {
			named = append(named, n)
		}
	}
	if len(named) == 0 {
		return nil, errors.New("name the league: draft picks alone don't tell which one")
	}

	has := make([]bool, len(leagues))
	errs := make([]error, len(leagues))
	eachLeague(leagues, func(i int, l sleeper.League) {
		rosters, err := s.api.Rosters(ctx, l.LeagueID)
		if err != nil {
			errs[i] = err
			return
		}
		mine, ok := MyRoster(rosters, userID)
		if !ok {
			return // not in this league's rosters: nothing of mine to trade
		}
		for _, n := range named {
			if _, err := onRoster(mine.Players, n, players); err != nil {
				return
			}
		}
		has[i] = true
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var found, names []string
	var kept []sleeper.League
	for i, l := range leagues {
		names = append(names, l.Name)
		if has[i] {
			kept = append(kept, l)
			found = append(found, l.Name)
		}
	}
	// A league that failed to load might be the one: don't guess past it.
	if err := errors.Join(errs...); err != nil && len(kept) < 2 {
		return nil, fmt.Errorf("couldn't check every league, so name the league: %w", err)
	}
	switch len(kept) {
	case 1:
		return kept, nil
	case 0:
		return nil, fmt.Errorf("no league where I have all of %s; name the league (mine: %s)", strings.Join(give, ", "), strings.Join(names, "; "))
	default:
		return nil, fmt.Errorf("%s: on my roster in more than one league; name one of: %s", strings.Join(give, ", "), strings.Join(found, "; "))
	}
}

func (s *Service) evaluate(ctx context.Context, l sleeper.League, userID string, give, get []string, players map[string]sleeper.Player) (TradeReport, error) {
	settings := ValueSettings(l).Normalise()
	out := TradeReport{Source: ValueSource, LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Market: settings.String()}

	var (
		market  map[string]fantasycalc.Value
		rosters []sleeper.Roster
		users   []sleeper.LeagueUser
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { market, err = s.values(gctx, settings); return err })
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if err := g.Wait(); err != nil {
		return out, err
	}
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	var others []string
	for _, r := range rosters {
		if r.RosterID != mine.RosterID {
			others = append(others, r.Players...)
		}
	}
	teamOf := func(id string) string {
		for _, r := range rosters {
			if slices.Contains(r.Players, id) {
				return TeamName(users, r.OwnerID)
			}
		}
		return ""
	}

	seen := make(map[string]bool)
	var picks bool
	resolve := func(names, pool []string, side string) ([]TradeAsset, error) {
		var assets []TradeAsset
		for _, n := range names {
			a, err := asset(n, pool, players, market)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", side, err)
			}
			if seen[a.PlayerID] {
				return nil, fmt.Errorf("%s is named twice", a.Name)
			}
			seen[a.PlayerID] = true
			if a.Position == "PICK" {
				picks = true
			} else {
				a.Team = teamOf(a.PlayerID)
			}
			assets = append(assets, a)
		}
		return assets, nil
	}
	var err error
	if out.Give, err = resolve(give, mine.Players, "give"); err != nil {
		return out, err
	}
	if out.Get, err = resolve(get, others, "get"); err != nil {
		return out, err
	}
	for _, a := range out.Get {
		if slices.Contains(mine.Players, a.PlayerID) {
			return out, fmt.Errorf("%s is already on my roster", a.Name)
		}
	}

	adjust(out.Give, out.Get)
	var partners []string
	for _, a := range out.Give {
		out.GiveValue += a.Value
		out.GiveAdjusted += a.Adjusted
		if a.Position != "PICK" && !slices.Contains(mine.Players, a.PlayerID) {
			out.Notes = append(out.Notes, fmt.Sprintf("%s is not on my roster", a.Name))
		}
	}
	for _, a := range out.Get {
		out.GetValue += a.Value
		out.GetAdjusted += a.Adjusted
		switch {
		case a.Position == "PICK":
		case a.Team == "":
			out.Notes = append(out.Notes, fmt.Sprintf("%s is a free agent here: no trade needed", a.Name))
		case !slices.Contains(partners, a.Team):
			partners = append(partners, a.Team)
		}
	}
	out.Partner = strings.Join(partners, ", ")
	out.Margin = out.GetAdjusted - out.GiveAdjusted
	out.Verdict = verdict(out.GiveAdjusted, out.GetAdjusted)

	if extra := countPlayers(out.Get) - countPlayers(out.Give); extra > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("I get %d more player(s) than I give: I need %d open roster spot(s) or must drop someone", extra, extra))
	}
	for _, a := range slices.Concat(out.Give, out.Get) {
		if a.Unrated {
			out.Notes = append(out.Notes, fmt.Sprintf("FantasyCalc doesn't rate %s; counted as 0", a.Name))
		}
	}
	if picks {
		out.Notes = append(out.Notes, "who owns each draft pick is not checked")
	}
	if k := l.Kind(); k == "keeper" || k == "guillotine" {
		out.Notes = append(out.Notes, k+" league: valued with redraft values")
	}
	return out, nil
}

// asset resolves a name to a trade asset: a player on pool (a roster) first,
// else any NFL player, else (dynasty) a draft pick from the market.
func asset(name string, pool []string, players map[string]sleeper.Player, market map[string]fantasycalc.Value) (TradeAsset, error) {
	id, err := onRoster(pool, name, players)
	if errors.Is(err, errAmbiguous) {
		return TradeAsset{}, err
	}
	if err != nil {
		if found := findPlayers(players, name); len(found) > 1 {
			var names []string
			for _, p := range found {
				names = append(names, fmt.Sprintf("%s (%s %s)", p.Name(), p.Position, cmp.Or(p.Team, "FA")))
			}
			return TradeAsset{}, fmt.Errorf("%q %w: %s", name, errAmbiguous, strings.Join(names, ", "))
		} else if len(found) == 1 {
			id = found[0].PlayerID
		} else if pick, ok := findPick(market, name); ok {
			return TradeAsset{PlayerID: pick.SleeperID, Name: pick.Name, Position: "PICK", Value: pick.Value}, nil
		} else {
			return TradeAsset{}, fmt.Errorf("no player or pick matches %q", name)
		}
	}
	pv := playerValue(Lookup(players, id), market)
	return TradeAsset{PlayerID: id, Name: pv.Name, Position: pv.Position, NFLTeam: pv.NFLTeam, Value: pv.Value, Unrated: pv.Unrated}, nil
}

// countPlayers counts the assets that take a roster spot (not draft picks).
func countPlayers(assets []TradeAsset) int {
	n := 0
	for _, a := range assets {
		if a.Position != "PICK" {
			n++
		}
	}
	return n
}

var errAmbiguous = errors.New("matches more than one player")

// onRoster finds the one player on roster whose name matches query: an
// exact name first, else a single partial match.
func onRoster(roster []string, query string, players map[string]sleeper.Player) (string, error) {
	q := foldName(query)
	if q == "" {
		return "", errors.New("empty name")
	}
	var exact, partial []string
	for _, id := range roster {
		switch n := foldName(Lookup(players, id).Name()); {
		case n == q:
			exact = append(exact, id)
		case strings.Contains(n, q):
			partial = append(partial, id)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) == 0 && len(partial) == 1:
		return partial[0], nil
	case len(exact)+len(partial) == 0:
		return "", fmt.Errorf("%q is not on the roster", query)
	}
	var names []string
	for _, id := range slices.Concat(exact, partial) {
		names = append(names, Lookup(players, id).Name())
	}
	return "", fmt.Errorf("%q %w: %s", query, errAmbiguous, strings.Join(names, ", "))
}

// findPick finds a dynasty draft pick in the market by name ("2027 1st",
// "2027 1st early"): an exact name first, else the most valuable partial
// match.
func findPick(market map[string]fantasycalc.Value, query string) (fantasycalc.Value, bool) {
	q := foldName(query)
	var best fantasycalc.Value
	found := false
	for _, v := range market {
		if v.Position != "PICK" {
			continue
		}
		n := foldName(v.Name)
		if n == q {
			return v, true
		}
		if strings.Contains(n, q) && (!found || v.Value > best.Value) {
			best, found = v, true
		}
	}
	return best, found
}

// adjust sets each asset's Adjusted value. Two good players are not worth
// one great one: a roster can only start so many, and the stud is the scarce
// asset. So each side's best asset counts in full, and every other asset
// counts value × √(value / top), where top is the best asset in the whole
// trade. A 1-for-1 trade is unchanged; a 2-for-1 is judged as most trade
// calculators and managers do.
func adjust(sides ...[]TradeAsset) {
	top := 0
	for _, side := range sides {
		for _, a := range side {
			top = max(top, a.Value)
		}
	}
	for _, side := range sides {
		best := -1
		for i, a := range side {
			if best < 0 || a.Value > side[best].Value {
				best = i
			}
		}
		for i := range side {
			v := side[i].Value
			if i == best || top == 0 {
				side[i].Adjusted = v
				continue
			}
			side[i].Adjusted = int(math.Round(float64(v) * math.Sqrt(float64(v)/float64(top))))
		}
	}
}

// verdict words the outcome for me: within 5% is fair, within 15% a slight
// edge, beyond that a clear one.
func verdict(give, get int) string {
	bigger := max(give, get)
	if bigger == 0 {
		return "neither side has any value"
	}
	share := math.Abs(float64(get-give)) / float64(bigger)
	who := "I win"
	if give > get {
		who = "I lose"
	}
	switch {
	case share <= fairShare:
		return "fair"
	case share <= slightShare:
		return fmt.Sprintf("%s slightly (%.0f%%)", who, share*100)
	default:
		return fmt.Sprintf("%s clearly (%.0f%%)", who, share*100)
	}
}
