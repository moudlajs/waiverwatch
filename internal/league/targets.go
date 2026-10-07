package league

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// affordable is how far above my best offer a target may be valued and still
// be worth asking for.
const affordable = 1.10

// TargetReport is trade targets in each of my leagues.
type TargetReport struct {
	Source  string        `json:"source"`
	Leagues []TargetBoard `json:"leagues"`
}

// TargetBoard is one league's needs, spare players and targets.
type TargetBoard struct {
	LeagueID string        `json:"league_id"`
	League   string        `json:"league"`
	Kind     string        `json:"kind"`
	Market   string        `json:"market,omitempty"`
	Needs    []string      `json:"needs" jsonschema:"positions I'm targeting, with why (thin: no backup, short: can't fill the lineup)"`
	Spares   []PlayerValue `json:"spares" jsonschema:"my healthy players beyond what my lineup needs, at positions with depth to spare: what I can offer, most valuable first"`
	Targets  []TradeTarget `json:"targets" jsonschema:"players on other teams at the needed positions I can afford: mutual fits first (my offer fills their thin spot), then most valuable"`
	Note     string        `json:"note,omitempty"`
	Error    string        `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// TradeTarget is a player to ask for and what to offer for him.
type TradeTarget struct {
	PlayerValue
	Offer      []string `json:"offer" jsonschema:"the fewest, least valuable spares that match his value (one or two players)"`
	OfferValue int      `json:"offer_value" jsonschema:"the offer's value after the 2-for-1 adjustment evaluate_trade uses"`
	TheyNeed   []string `json:"they_need,omitempty" jsonschema:"positions where his team is thin or short"`
	Mutual     bool     `json:"mutual,omitempty" jsonschema:"the offer includes a player at a position his team needs: a deal that helps both sides"`
}

// TradeTargets finds, in each league matching leagueQuery (empty = all),
// players on other teams who fill my thin positions (or position, when set),
// priced against the spare players I could trade away.
func (s *Service) TradeTargets(ctx context.Context, leagueQuery, position string, limit int) (TargetReport, error) {
	if s.values == nil {
		return TargetReport{}, errors.New("trade values are not set up on this server")
	}
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return TargetReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return TargetReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return TargetReport{}, err
	}
	out := TargetReport{Source: ValueSource, Leagues: make([]TargetBoard, len(leagues))}
	eachLeague(leagues, func(i int, l sleeper.League) {
		b, err := s.targets(ctx, l, user.UserID, position, limit, players)
		if err != nil {
			b.Error = err.Error()
		}
		out.Leagues[i] = b
	})
	return out, nil
}

func (s *Service) targets(ctx context.Context, l sleeper.League, userID, position string, limit int, players map[string]sleeper.Player) (TargetBoard, error) {
	settings := ValueSettings(l).Normalise()
	out := TargetBoard{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Market: settings.String(),
		Needs: []string{}, Spares: []PlayerValue{}, Targets: []TradeTarget{}}
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
	out.Market = marketLabel(settings, source)
	addNote(&out.Note, source)
	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return out, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	if len(mine.Players) == 0 {
		addNote(&out.Note, "eliminated from this league")
		return out, nil
	}

	depth, _, _ := depthChart(l.RosterPositions, mine, players)
	var needs []string
	for _, pd := range depth {
		streamed := pd.Position == "K" || pd.Position == "DEF" // streamed, not traded for
		switch {
		case position != "" && pd.Position == position:
			needs = append(needs, pd.Position)
			out.Needs = append(out.Needs, fmt.Sprintf("%s (asked for; %s, %d backups)", pd.Position, pd.Status, pd.Backups))
		case position == "" && !streamed && pd.Status != depthOK:
			needs = append(needs, pd.Position)
			out.Needs = append(out.Needs, fmt.Sprintf("%s %s (%d healthy, %d backups)", pd.Position, pd.Status, pd.Healthy, pd.Backups))
		}
	}
	if position != "" && len(needs) == 0 {
		addNote(&out.Note, fmt.Sprintf("this league doesn't start a %s", position))
		return out, nil
	}

	// Spares: at positions with backups, the healthy players beyond the
	// lineup's needs, weakest kept first so the best ones are offered.
	for _, pd := range depth {
		if pd.Backups == 0 || slices.Contains(needs, pd.Position) {
			continue
		}
		var healthy []PlayerValue
		for _, id := range mine.Players {
			p := Lookup(players, id)
			if p.Position == pd.Position && !slices.Contains(unavailable, p.InjuryStatus) && !slices.Contains(mine.Reserve, id) && !slices.Contains(mine.Taxi, id) {
				healthy = append(healthy, playerValue(p, market))
			}
		}
		slices.SortStableFunc(healthy, func(a, b PlayerValue) int { return cmp.Compare(b.Value, a.Value) })
		// The best pd.Starting players start; the rest can go.
		for _, pv := range healthy[min(pd.Starting, len(healthy)):] {
			if pv.Value > 0 {
				out.Spares = append(out.Spares, pv)
			}
		}
	}
	slices.SortStableFunc(out.Spares, func(a, b PlayerValue) int { return cmp.Compare(b.Value, a.Value) })
	switch {
	case len(needs) == 0:
		addNote(&out.Note, "no thin spots: every position has a healthy backup (name a position to look anyway)")
		return out, nil
	case len(out.Spares) == 0:
		addNote(&out.Note, "no spare players with trade value: anything I trade away opens a hole elsewhere")
		return out, nil
	}

	budget := offerValue(out.Spares[:min(2, len(out.Spares))])
	for _, r := range rosters {
		if r.RosterID == mine.RosterID {
			continue
		}
		theyNeed := thinPositions(l.RosterPositions, r, players)
		for _, id := range r.Players {
			p := Lookup(players, id)
			if !slices.Contains(needs, p.Position) || slices.Contains(unavailable, p.InjuryStatus) || slices.Contains(r.Reserve, id) {
				continue
			}
			pv := playerValue(p, market)
			if pv.Value == 0 || float64(pv.Value) > float64(budget)*affordable {
				continue
			}
			pv.Team = TeamName(users, r.OwnerID)
			t := TradeTarget{PlayerValue: pv, TheyNeed: theyNeed}
			t.Offer, t.OfferValue, t.Mutual = offerFor(out.Spares, theyNeed, pv.Value)
			out.Targets = append(out.Targets, t)
		}
	}
	sortTradeTargets(out.Targets)
	out.Targets = out.Targets[:min(len(out.Targets), limit)]
	if len(out.Targets) == 0 {
		addNote(&out.Note, "nobody at the needed positions is within reach of my spares")
	}
	return out, nil
}

// thinPositions is where a roster is thin or short, kickers and defenses
// aside (streamed, not traded for).
func thinPositions(slots []string, r sleeper.Roster, players map[string]sleeper.Player) []string {
	var out []string
	depth, _, _ := depthChart(slots, r, players)
	for _, pd := range depth {
		if pd.Status != depthOK && pd.Position != "K" && pd.Position != "DEF" {
			out = append(out, pd.Position)
		}
	}
	return out
}

// sortTradeTargets puts mutual fits first, then the most valuable.
func sortTradeTargets(ts []TradeTarget) {
	slices.SortStableFunc(ts, func(a, b TradeTarget) int {
		return cmp.Or(cmp.Compare(boolRank(a.Mutual), boolRank(b.Mutual)), cmp.Compare(b.Value, a.Value))
	})
}

// boolRank sorts true before false.
func boolRank(b bool) int {
	if b {
		return 0
	}
	return 1
}

// offerFor builds an offer worth want from spares: from the spares at
// positions the other team needs first (that offer gets accepted), else the
// cheapest from all of them. mutual reports whether it includes a needed
// position and is worth what he is: an underpaying offer isn't a fit.
func offerFor(spares []PlayerValue, theyNeed []string, want int) (names []string, value int, mutual bool) {
	needed := func(sp PlayerValue) bool { return slices.Contains(theyNeed, sp.Position) }
	if fits := slices.DeleteFunc(slices.Clone(spares), func(sp PlayerValue) bool { return !needed(sp) }); len(fits) > 0 {
		if offer, v := cheapestOffer(fits, want); v >= want {
			return playerNames(offer), v, true
		}
	}
	offer, v := cheapestOffer(spares, want)
	return playerNames(offer), v, v >= want && slices.ContainsFunc(offer, needed)
}

func playerNames(pvs []PlayerValue) []string {
	names := make([]string, len(pvs))
	for i, pv := range pvs {
		names[i] = pv.Name
	}
	return names
}

// offerValue is what a package of players is worth after the 2-for-1
// adjustment, measured against itself.
func offerValue(pvs []PlayerValue) int {
	assets := make([]TradeAsset, len(pvs))
	for i, pv := range pvs {
		assets[i] = TradeAsset{Value: pv.Value}
	}
	adjust(assets)
	total := 0
	for _, a := range assets {
		total += a.Adjusted
	}
	return total
}

// cheapestOffer picks the least valuable single spare, else pair, whose
// adjusted value reaches want; failing that, the two best spares.
func cheapestOffer(spares []PlayerValue, want int) ([]PlayerValue, int) {
	// Spares are sorted most valuable first; scan from the cheap end.
	for i := len(spares) - 1; i >= 0; i-- {
		if spares[i].Value >= want {
			return []PlayerValue{spares[i]}, spares[i].Value
		}
	}
	var best []PlayerValue
	bestValue := 0
	for i := range spares {
		for j := i + 1; j < len(spares); j++ {
			v := pairValue(spares[i], spares[j], want)
			if v >= want && (best == nil || v < bestValue) {
				best, bestValue = []PlayerValue{spares[i], spares[j]}, v
			}
		}
	}
	if best != nil {
		return best, bestValue
	}
	top := spares[:min(2, len(spares))]
	return top, offerValue(top)
}

// pairValue is two spares' adjusted value offered for one player worth want:
// the 2-for-1 adjustment is relative to the best piece in the whole trade.
func pairValue(a, b PlayerValue, want int) int {
	give := []TradeAsset{{Value: a.Value}, {Value: b.Value}}
	adjust(give, []TradeAsset{{Value: want}})
	return give[0].Adjusted + give[1].Adjusted
}
