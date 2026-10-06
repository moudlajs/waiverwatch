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
	Targets  []TradeTarget `json:"targets" jsonschema:"players on other teams at the needed positions I can afford, most valuable first"`
	Note     string        `json:"note,omitempty"`
	Error    string        `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// TradeTarget is a player to ask for and what to offer for him.
type TradeTarget struct {
	PlayerValue
	Offer      []string `json:"offer" jsonschema:"the fewest, least valuable spares that match his value (one or two players)"`
	OfferValue int      `json:"offer_value" jsonschema:"the offer's value after the 2-for-1 adjustment evaluate_trade uses"`
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
	if len(mine.Players) == 0 {
		out.Note = "eliminated from this league"
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
		out.Note = fmt.Sprintf("this league doesn't start a %s", position)
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
		out.Note = "no thin spots: every position has a healthy backup (name a position to look anyway)"
		return out, nil
	case len(out.Spares) == 0:
		out.Note = "no spare players with trade value: anything I trade away opens a hole elsewhere"
		return out, nil
	}

	budget := offerValue(out.Spares[:min(2, len(out.Spares))])
	for _, r := range rosters {
		if r.RosterID == mine.RosterID {
			continue
		}
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
			offer, value := cheapestOffer(out.Spares, pv.Value)
			out.Targets = append(out.Targets, TradeTarget{PlayerValue: pv, Offer: offer, OfferValue: value})
		}
	}
	slices.SortStableFunc(out.Targets, func(a, b TradeTarget) int { return cmp.Compare(b.Value, a.Value) })
	out.Targets = out.Targets[:min(len(out.Targets), limit)]
	if len(out.Targets) == 0 {
		out.Note = "nobody at the needed positions is within reach of my spares"
	}
	return out, nil
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
func cheapestOffer(spares []PlayerValue, want int) ([]string, int) {
	// Spares are sorted most valuable first; scan from the cheap end.
	for i := len(spares) - 1; i >= 0; i-- {
		if spares[i].Value >= want {
			return []string{spares[i].Name}, spares[i].Value
		}
	}
	best, bestValue := []string(nil), 0
	for i := range spares {
		for j := i + 1; j < len(spares); j++ {
			v := pairValue(spares[i], spares[j], want)
			if v >= want && (best == nil || v < bestValue) {
				best, bestValue = []string{spares[i].Name, spares[j].Name}, v
			}
		}
	}
	if best != nil {
		return best, bestValue
	}
	top := spares[:min(2, len(spares))]
	var names []string
	for _, pv := range top {
		names = append(names, pv.Name)
	}
	return names, offerValue(top)
}

// pairValue is two spares' adjusted value offered for one player worth want:
// the 2-for-1 adjustment is relative to the best piece in the whole trade.
func pairValue(a, b PlayerValue, want int) int {
	give := []TradeAsset{{Value: a.Value}, {Value: b.Value}}
	adjust(give, []TradeAsset{{Value: want}})
	return give[0].Adjusted + give[1].Adjusted
}
