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
	Source       string        `json:"source"`
	LeagueID     string        `json:"league_id"`
	League       string        `json:"league"`
	Kind         string        `json:"kind"`
	Market       string        `json:"market" jsonschema:"the FantasyCalc market these values come from"`
	Partner      string        `json:"partner,omitempty" jsonschema:"the team the players and picks I get come from; several when more than one"`
	Give         []TradeAsset  `json:"give"`
	Get          []TradeAsset  `json:"get"`
	GiveValue    int           `json:"give_value" jsonschema:"summed value of what I give"`
	GetValue     int           `json:"get_value" jsonschema:"summed value of what I get"`
	GiveAdjusted int           `json:"give_adjusted" jsonschema:"give side after the consolidation adjustment"`
	GetAdjusted  int           `json:"get_adjusted" jsonschema:"get side after the consolidation adjustment"`
	Margin       int           `json:"margin" jsonschema:"get_adjusted minus give_adjusted: positive means I win the trade"`
	MarginPct    float64       `json:"margin_pct" jsonschema:"margin as a percentage of the bigger adjusted side"`
	Leans        string        `json:"leans" jsonschema:"who the trade favors: me, partner or even"`
	Verdict      string        `json:"verdict" jsonschema:"fair (within 5%), slight (within 15%) or clear edge"`
	Depth        []DepthChange `json:"depth,omitempty" jsonschema:"my depth before and after at the positions the trade touches"`
	Notes        []string      `json:"notes,omitempty"`
}

// TradeAsset is a player or dynasty draft pick in a trade.
type TradeAsset struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	Position string `json:"position,omitempty" jsonschema:"PICK for draft picks"`
	NFLTeam  string `json:"nfl_team,omitempty"`
	Age      int    `json:"age,omitempty"`
	Injury   string `json:"injury,omitempty" jsonschema:"e.g. Questionable, Out, IR"`
	Team     string `json:"team,omitempty" jsonschema:"the fantasy team he (or the pick) is on now; empty for a free agent"`
	// Picks only.
	OriginalTeam string `json:"original_team,omitempty" jsonschema:"picks: the team whose pick it originally was; its record sets where it lands"`
	Projected    string `json:"projected,omitempty" jsonschema:"picks in the next draft: early, mid or late, projected from the original team's standing now"`
	Value        int    `json:"value"`
	Adjusted     int    `json:"adjusted" jsonschema:"what he counts for after the consolidation adjustment"`
	Unrated      bool   `json:"unrated,omitempty" jsonschema:"FantasyCalc doesn't rate him; counted as 0"`
}

// DepthChange is one position of my depth chart before and after a trade.
type DepthChange struct {
	Position string `json:"position"`
	Before   string `json:"before" jsonschema:"ok, thin or short, with healthy players and backups"`
	After    string `json:"after"`
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
// give is on my roster. Picks don't narrow anything; a give of picks alone
// needs a league.
func (s *Service) leaguesWithMine(ctx context.Context, leagues []sleeper.League, userID string, give []string, players map[string]sleeper.Player) ([]sleeper.League, error) {
	var named []string
	for _, n := range give {
		if _, isPick := parsePick(n); !isPick {
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
			// Ambiguous still counts: he is here, and evaluating says which ones match.
			if len(rosterMatches(mine.Players, n, players)) == 0 {
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
		return nil, fmt.Errorf("no league where I have all of %s; name the league (mine: %s)", strings.Join(named, ", "), strings.Join(names, "; "))
	default:
		return nil, fmt.Errorf("%s: on my roster in more than one league; name one of: %s", strings.Join(named, ", "), strings.Join(found, "; "))
	}
}

// trade is the league state one evaluation works from.
type trade struct {
	league  sleeper.League
	market  map[string]fantasycalc.Value
	rosters []sleeper.Roster
	users   []sleeper.LeagueUser
	players map[string]sleeper.Player
	traded  []sleeper.TradedPick
	mine    sleeper.Roster
	seen    map[string]bool
}

func (s *Service) evaluate(ctx context.Context, l sleeper.League, userID string, give, get []string, players map[string]sleeper.Player) (TradeReport, error) {
	settings := ValueSettings(l).Normalise()
	out := TradeReport{Source: ValueSource, LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Market: settings.String()}

	t := trade{league: l, players: players, seen: make(map[string]bool)}
	g, gctx := errgroup.WithContext(ctx)
	var source string
	g.Go(func() (err error) { t.market, source, err = s.market(gctx, settings); return err })
	g.Go(func() (err error) { t.rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { t.users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if l.Kind() == "dynasty" {
		g.Go(func() (err error) { t.traded, err = s.api.TradedPicks(gctx, l.LeagueID); return err })
	}
	if err := g.Wait(); err != nil {
		return out, err
	}
	var ok bool
	if t.mine, ok = MyRoster(t.rosters, userID); !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}

	// Players first: the players I get name the partner, whose roster then
	// settles ambiguous names and whose picks are the ones on offer.
	out.Give, out.Get = make([]TradeAsset, len(give)), make([]TradeAsset, len(get))
	var picks []func() error
	for i, n := range give {
		if q, isPick := parsePick(n); isPick {
			picks = append(picks, func() (err error) { out.Give[i], err = t.pick(q, []int{t.mine.RosterID}, "give"); return err })
			continue
		}
		ids := rosterMatches(t.mine.Players, n, players)
		switch len(ids) {
		case 0:
			return out, fmt.Errorf("give: %q is not on my roster in %s", n, l.Name)
		case 1:
		default:
			return out, t.ambiguous("give", n, ids)
		}
		var err error
		if out.Give[i], err = t.player(ids[0]); err != nil {
			return out, err
		}
	}

	var others []string
	for _, r := range t.rosters {
		if r.RosterID != t.mine.RosterID {
			others = append(others, r.Players...)
		}
	}
	var deferred []int
	for i, n := range get {
		if q, isPick := parsePick(n); isPick {
			picks = append(picks, func() (err error) { out.Get[i], err = t.pick(q, t.partners(out.Get), "get"); return err })
			continue
		}
		ids := rosterMatches(others, n, players)
		if len(ids) == 0 {
			if mine := rosterMatches(t.mine.Players, n, players); len(mine) == 1 {
				return out, fmt.Errorf("get: %s is already on my roster", Lookup(players, mine[0]).Name())
			}
			for _, p := range findPlayers(players, n) { // free agents
				ids = append(ids, p.PlayerID)
			}
		}
		switch len(ids) {
		case 0:
			return out, fmt.Errorf("get: no player or pick matches %q", n)
		case 1:
			var err error
			if out.Get[i], err = t.player(ids[0]); err != nil {
				return out, err
			}
		default:
			deferred = append(deferred, i)
		}
	}
	for _, i := range deferred {
		if partners := t.partners(out.Get); len(partners) == 1 {
			if ids := rosterMatches(t.roster(partners[0]).Players, get[i], players); len(ids) == 1 {
				var err error
				if out.Get[i], err = t.player(ids[0]); err != nil {
					return out, err
				}
				continue
			}
		}
		all := rosterMatches(others, get[i], players)
		if len(all) == 0 {
			for _, p := range findPlayers(players, get[i]) { // free agents
				all = append(all, p.PlayerID)
			}
		}
		return out, t.ambiguous("get", get[i], all)
	}
	if len(picks) > 0 && l.Kind() != "dynasty" {
		return out, errors.New("draft picks are only valued in dynasty leagues")
	}
	for _, resolve := range picks {
		if err := resolve(); err != nil {
			return out, err
		}
	}

	adjust(out.Give, out.Get)
	for _, a := range out.Give {
		out.GiveValue += a.Value
		out.GiveAdjusted += a.Adjusted
	}
	var partners []string
	for _, a := range out.Get {
		out.GetValue += a.Value
		out.GetAdjusted += a.Adjusted
		switch {
		case a.Team == "":
			out.Notes = append(out.Notes, fmt.Sprintf("%s is a free agent here: no trade needed", a.Name))
		case !slices.Contains(partners, a.Team):
			partners = append(partners, a.Team)
		}
	}
	out.Partner = strings.Join(partners, ", ")
	out.Margin = out.GetAdjusted - out.GiveAdjusted
	if bigger := max(out.GiveAdjusted, out.GetAdjusted); bigger > 0 {
		out.MarginPct = math.Round(float64(out.Margin)/float64(bigger)*1000) / 10
	}
	switch {
	case out.Margin > 0:
		out.Leans = "me"
	case out.Margin < 0:
		out.Leans = "partner"
	default:
		out.Leans = "even"
	}
	out.Verdict = verdict(out.GiveAdjusted, out.GetAdjusted)
	out.Depth = t.depthChange(out.Give, out.Get)

	if extra := countPlayers(out.Get) - countPlayers(out.Give); extra > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("I get %d more player(s) than I give: I need %d open roster spot(s) or must drop someone", extra, extra))
	}
	for _, a := range slices.Concat(out.Give, out.Get) {
		if a.Unrated {
			out.Notes = append(out.Notes, fmt.Sprintf("FantasyCalc doesn't rate %s; counted as 0", a.Name))
		}
	}
	if k := l.Kind(); k == "keeper" || k == "guillotine" {
		out.Notes = append(out.Notes, k+" league: valued with redraft values")
	}
	if source != "" {
		out.Notes = append(out.Notes, source)
	}
	return out, nil
}

// player makes a trade asset of a player, once per trade.
func (t *trade) player(id string) (TradeAsset, error) {
	pv := playerValue(Lookup(t.players, id), t.market)
	if t.seen[id] {
		return TradeAsset{}, fmt.Errorf("%s is named twice", pv.Name)
	}
	t.seen[id] = true
	a := TradeAsset{PlayerID: id, Name: pv.Name, Position: pv.Position, NFLTeam: pv.NFLTeam, Age: t.players[id].Age, Injury: t.players[id].InjuryStatus, Value: pv.Value, Unrated: pv.Unrated}
	if r := t.holderOf(id); r != nil {
		a.Team = t.team(r.RosterID)
	}
	return a, nil
}

// pick finds the one pick matching q held by one of holders, and values it:
// by the named slot, else (next draft only) the slot its original team's
// standing projects, else as a generic pick of its round.
func (t *trade) pick(q pickQuery, holders []int, side string) (TradeAsset, error) {
	seasons := pickSeasons(t.market)
	if !slices.Contains(seasons, q.season) {
		return TradeAsset{}, fmt.Errorf("%s: FantasyCalc values picks for %s only", side, strings.Join(seasons, ", "))
	}
	rounds := t.league.Settings.DraftRounds
	if rounds == 0 {
		rounds = 4 // not set: FantasyCalc values rounds 1-4
	}
	var cands []draftPick
	for _, p := range leaguePicks(t.rosters, t.traded, []string{q.season}, rounds) {
		if p.round == q.round && slices.Contains(holders, p.holder) {
			cands = append(cands, p)
		}
	}
	if q.team != "" {
		// A team names whose pick it was; failing that, who holds it.
		byOrigin := slices.DeleteFunc(slices.Clone(cands), func(p draftPick) bool { return !strings.Contains(foldName(t.team(p.origin)), q.team) })
		if len(byOrigin) == 0 {
			byOrigin = slices.DeleteFunc(cands, func(p draftPick) bool { return !strings.Contains(foldName(t.team(p.holder)), q.team) })
		}
		cands = byOrigin
	}
	label := q.season + " " + ordinal(q.round)
	switch len(cands) {
	case 0:
		var who []string
		for _, h := range holders {
			who = append(who, t.team(h))
		}
		if len(who) > 3 {
			who = []string{"any other team"}
		}
		return TradeAsset{}, fmt.Errorf("%s: no %s held by %s%s", side, label, strings.Join(who, " or "), matching(q.team))
	case 1:
	default:
		var names []string
		for _, p := range cands {
			names = append(names, describePick(p, t.team))
		}
		return TradeAsset{}, fmt.Errorf("%s: %q %w: %s; add the original team, e.g. \"%s %s\"", side, label, errAmbiguous, strings.Join(names, "; "), label, t.team(cands[0].origin))
	}
	p := cands[0]
	id := fmt.Sprintf("pick-%s-%d-%d", p.season, p.round, p.origin)
	if t.seen[id] {
		return TradeAsset{}, fmt.Errorf("%s is named twice", describePick(p, t.team))
	}
	t.seen[id] = true

	slot, projected := q.slot, ""
	// The earliest draft FantasyCalc values is the next one: it drops a
	// draft's picks once that draft is done.
	if slot == "" && q.season == seasons[0] {
		slot = projectSlot(t.rosters, p.origin)
		projected = slot
	}
	v, rated := pickValue(t.market, p.season, p.round, slot)
	if q.slot != "" && v.Name == label {
		return TradeAsset{}, fmt.Errorf("%s: FantasyCalc has no %s value for a %s; name it without %q", side, q.slot, label, q.slot)
	}
	name := label
	if v.Name == label {
		projected = "" // no value for that slot: valued as a generic pick of its round
	} else if slot != "" {
		name += " (" + slot + ")"
	}
	return TradeAsset{
		PlayerID: id, Name: name, Position: "PICK", Team: t.team(p.holder), OriginalTeam: t.team(p.origin),
		Projected: projected, Value: v.Value, Unrated: !rated,
	}, nil
}

func matching(team string) string {
	if team == "" {
		return ""
	}
	return fmt.Sprintf(" matching %q", team)
}

// partners are the roster IDs the players resolved on the get side come
// from; with none yet, every other team.
func (t *trade) partners(get []TradeAsset) []int {
	var ids []int
	for _, a := range get {
		if a.PlayerID == "" || a.Position == "PICK" {
			continue
		}
		if r := t.holderOf(a.PlayerID); r != nil && !slices.Contains(ids, r.RosterID) {
			ids = append(ids, r.RosterID)
		}
	}
	if len(ids) > 0 {
		return ids
	}
	for _, r := range t.rosters {
		if r.RosterID != t.mine.RosterID {
			ids = append(ids, r.RosterID)
		}
	}
	return ids
}

func (t *trade) holderOf(playerID string) *sleeper.Roster {
	for i := range t.rosters {
		if slices.Contains(t.rosters[i].Players, playerID) {
			return &t.rosters[i]
		}
	}
	return nil
}

func (t *trade) roster(rosterID int) sleeper.Roster {
	for _, r := range t.rosters {
		if r.RosterID == rosterID {
			return r
		}
	}
	return sleeper.Roster{}
}

func (t *trade) team(rosterID int) string {
	return TeamName(t.users, t.roster(rosterID).OwnerID)
}

// ambiguous lists every player a name matches, with position, NFL team and
// fantasy team, so the caller can pick one without another lookup.
func (t *trade) ambiguous(side, query string, ids []string) error {
	var names []string
	for _, id := range ids {
		p := Lookup(t.players, id)
		team := "free agent"
		if r := t.holderOf(id); r != nil {
			team = t.team(r.RosterID)
		}
		names = append(names, fmt.Sprintf("%s (%s %s, %s)", p.Name(), p.Position, cmp.Or(p.Team, "no NFL team"), team))
	}
	return fmt.Errorf("%s: %q %w: %s", side, query, errAmbiguous, strings.Join(names, "; "))
}

// depthChange is my depth before and after the trade at the positions it
// touches.
func (t *trade) depthChange(give, get []TradeAsset) []DepthChange {
	var out, in []string
	var touched []string
	for _, a := range give {
		if a.Position != "PICK" {
			out = append(out, a.PlayerID)
			touched = append(touched, a.Position)
		}
	}
	for _, a := range get {
		if a.Position != "PICK" {
			in = append(in, a.PlayerID)
			touched = append(touched, a.Position)
		}
	}
	if len(touched) == 0 {
		return nil
	}
	keep := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(out, id) })
	}
	after := t.mine
	after.Players = append(keep(t.mine.Players), in...)
	after.Reserve, after.Taxi = keep(t.mine.Reserve), keep(t.mine.Taxi)

	before, _, _ := depthChart(t.league.RosterPositions, t.mine, t.players)
	now, _, _ := depthChart(t.league.RosterPositions, after, t.players)
	var changes []DepthChange
	for i, pd := range before {
		if !slices.Contains(touched, pd.Position) {
			continue
		}
		changes = append(changes, DepthChange{
			Position: pd.Position,
			Before:   fmt.Sprintf("%s (%d healthy, %d backups)", pd.Status, pd.Healthy, pd.Backups),
			After:    fmt.Sprintf("%s (%d healthy, %d backups)", now[i].Status, now[i].Healthy, now[i].Backups),
		})
	}
	return changes
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

var errAmbiguous = errors.New("matches more than one")

// rosterMatches returns the players on roster whose name matches query: the
// exact names if any, else every partial match.
func rosterMatches(roster []string, query string, players map[string]sleeper.Player) []string {
	q := foldName(query)
	var exact, partial []string
	for _, id := range roster {
		switch n := foldName(Lookup(players, id).Name()); {
		case n == q:
			exact = append(exact, id)
		case strings.Contains(n, q):
			partial = append(partial, id)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
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
