package league

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Positions waiver_targets understands: the offensive fantasy positions plus
// kickers and team defenses. IDP is not supported.
var Positions = []string{"QB", "RB", "WR", "TE", "K", "DEF"}

// flexSlots maps Sleeper's flex lineup slots to the positions they accept.
var flexSlots = map[string][]string{
	"FLEX":       {"RB", "WR", "TE"},
	"SUPER_FLEX": {"QB", "RB", "WR", "TE"},
	"WRRB_FLEX":  {"WR", "RB"},
	"REC_FLEX":   {"WR", "TE"},
}

// WaiverReport is the best available players in each of the user's leagues.
type WaiverReport struct {
	Position string        `json:"position,omitempty"`
	Leagues  []WaiverBoard `json:"leagues"`
}

// WaiverBoard is one league's waiver situation for the user.
type WaiverBoard struct {
	LeagueID string   `json:"league_id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Waivers  Waivers  `json:"waivers"`
	Targets  []Target `json:"targets"`
	Note     string   `json:"note,omitempty"`
	Error    string   `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// Waivers is how claims work in a league, from the user's side.
type Waivers struct {
	Type          string `json:"type" jsonschema:"faab, rolling (waiver priority) or reverse_standings"`
	Priority      int    `json:"priority,omitempty" jsonschema:"my waiver position, 1 claims first"`
	FAABRemaining *int   `json:"faab_remaining,omitempty" jsonschema:"FAAB leagues: my budget left"`
	FAABBudget    int    `json:"faab_budget,omitempty"`
}

// Target is an available player worth a claim.
type Target struct {
	PlayerID   string `json:"player_id"`
	Name       string `json:"name"`
	Position   string `json:"position"`
	NFLTeam    string `json:"nfl_team"`
	Injury     string `json:"injury,omitempty"`
	Adds       int    `json:"adds,omitempty" jsonschema:"adds across Sleeper in the last 24h (only the top 100 trending are counted)"`
	SearchRank int    `json:"search_rank,omitempty" jsonschema:"Sleeper's overall player rank, lower is better"`
	// waiver_targets only:
	Value     int     `json:"value,omitempty" jsonschema:"FantasyCalc trade value in this league's format: rest-of-season worth"`
	Projected float64 `json:"projected,omitempty" jsonschema:"Sleeper's projected points this week in this league's scoring"`
	Bid       *int    `json:"bid,omitempty" jsonschema:"FAAB leagues: a suggested bid, value/100 percent of my remaining budget (at most half), plus 5 points when he's trending hard; a starting point, not a rule"`
}

// Waiver ranking and bids.
const (
	candidatePool = 400  // available players considered before re-ranking by value and projection
	hotAdds       = 1000 // 24h adds that count as trending hard
)

// WaiverTargets returns, per league, the best available players: those on no
// roster, on an NFL team, active, and at a position the league can start.
// position and leagueQuery (a league name fragment or ID) narrow it down;
// empty means all. Ranked by trade value (rest-of-season worth), then this
// week's projection, then 24h trending adds; FAAB leagues get a suggested bid.
// Without values or projections, it falls back to trending and Sleeper's rank.
func (s *Service) WaiverTargets(ctx context.Context, position, leagueQuery string, limit int) (WaiverReport, error) {
	if position != "" && !slices.Contains(Positions, position) {
		return WaiverReport{}, fmt.Errorf("unknown position %q: use one of %s", position, strings.Join(Positions, ", "))
	}
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return WaiverReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return WaiverReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return WaiverReport{}, err
	}
	trending, err := s.api.TrendingAdds(ctx, 24, maxTrending)
	if err != nil {
		return WaiverReport{}, err
	}
	adds := make(map[string]int, len(trending))
	for _, t := range trending {
		adds[t.PlayerID] = t.Count
	}
	var notes []string
	seasonType := state.SeasonType
	if seasonType == "" || seasonType == "off" {
		seasonType = "regular"
	}
	proj, err := s.api.Projections(ctx, seasonType, state.Season, state.Week)
	if err != nil {
		notes = append(notes, "no projections: "+err.Error())
	} else {
		var (
			note string
			ok   bool
		)
		proj, note, ok = s.projections.complete(weekKey(seasonType, state.Season, state.Week), proj, players, time.Now())
		if !ok {
			proj = nil // ranked by value and trending alone
		}
		if note != "" {
			notes = append(notes, note)
		}
	}

	pools := s.pools(ctx, leagues, user.UserID)
	out := WaiverReport{Position: position, Leagues: make([]WaiverBoard, len(pools))}
	for i, p := range pools {
		l := p.league
		b := WaiverBoard{LeagueID: l.LeagueID, Name: l.Name, Kind: l.Kind(), Targets: []Target{}}
		if p.err != nil {
			b.Error = p.err.Error()
			out.Leagues[i] = b
			continue
		}
		b.Waivers = waivers(l, p.me)
		if len(p.me.Players) == 0 && l.Kind() == "guillotine" {
			b.Note = "eliminated from this league"
			out.Leagues[i] = b
			continue
		}
		eligible := EligiblePositions(l.RosterPositions)
		if position != "" {
			if !eligible[position] {
				b.Note = fmt.Sprintf("this league has no lineup slot for %s", position)
				out.Leagues[i] = b
				continue
			}
			eligible = map[string]bool{position: true}
		}
		b.Targets = targets(players, p.rostered, eligible, adds, candidatePool)
		for _, n := range notes {
			addNote(&b.Note, n)
		}
		var market map[string]fantasycalc.Value
		if s.values != nil {
			m, source, err := s.market(ctx, ValueSettings(l).Normalise())
			if err != nil {
				addNote(&b.Note, "no trade values: "+err.Error())
			}
			market = m
			addNote(&b.Note, source)
		}
		b.Targets = rankTargets(b.Targets, market, proj, projectionKey(l.Scoring.Rec), b.Waivers, l.Kind(), limit)
		out.Leagues[i] = b
	}
	return out, nil
}

// matchLeagues keeps the leagues whose ID equals query or whose name contains
// it, ignoring case. An empty query keeps all.
func matchLeagues(leagues []sleeper.League, query string) ([]sleeper.League, error) {
	if query == "" {
		return leagues, nil
	}
	q := strings.ToLower(query)
	var names []string
	var kept []sleeper.League
	for _, l := range leagues {
		names = append(names, l.Name)
		if l.LeagueID == query || strings.Contains(strings.ToLower(l.Name), q) {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("no league matches %q; my leagues are: %s", query, strings.Join(names, "; "))
	}
	return kept, nil
}

// EligiblePositions is the set of Positions a league can start, expanding
// flex slots.
func EligiblePositions(slots []string) map[string]bool {
	set := make(map[string]bool)
	for _, s := range slots {
		if slices.Contains(Positions, s) {
			set[s] = true
		}
		for _, p := range flexSlots[s] {
			set[p] = true
		}
	}
	return set
}

func waivers(l sleeper.League, me sleeper.Roster) Waivers {
	w := Waivers{Priority: me.Settings.WaiverPosition}
	switch l.Settings.WaiverType {
	case 2:
		left := l.Settings.WaiverBudget - me.Settings.WaiverBudgetUsed
		w.Type, w.FAABRemaining, w.FAABBudget, w.Priority = "faab", &left, l.Settings.WaiverBudget, 0
	case 1:
		w.Type = "reverse_standings"
	default:
		w.Type = "rolling"
	}
	return w
}

// targets ranks the available players at eligible positions: trending adds
// first, then search rank (unranked last), then name for a stable order.
func targets(players map[string]sleeper.Player, rostered, eligible map[string]bool, adds map[string]int, limit int) []Target {
	var out []Target
	for id, p := range players {
		if rostered[id] || !p.Active || p.Team == "" || !eligible[p.Position] {
			continue
		}
		out = append(out, Target{
			PlayerID: id, Name: p.Name(), Position: p.Position, NFLTeam: p.Team,
			Injury: p.InjuryStatus, Adds: adds[id], SearchRank: p.SearchRank,
		})
	}
	slices.SortFunc(out, func(a, b Target) int {
		return cmp.Or(
			cmp.Compare(b.Adds, a.Adds),
			cmp.Compare(rankKey(a.SearchRank), rankKey(b.SearchRank)),
			cmp.Compare(a.Name, b.Name),
		)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Target{}
	}
	return out
}

// rankTargets values the candidates, keeps the best limit by trade value,
// then projection, then trending adds (candidates arrive sorted by adds and
// Sleeper's rank, which breaks the remaining ties), and suggests FAAB bids.
// Guillotine leagues (survive this week or be cut) put the projection
// before the value. Outside dynasty, where injured players are stashes,
// players who can't play (Out, IR...) go after everyone who can.
func rankTargets(cands []Target, market map[string]fantasycalc.Value, proj map[string]map[string]float64, key string, w Waivers, kind string, limit int) []Target {
	for i := range cands {
		cands[i].Value = market[cands[i].PlayerID].Value
		cands[i].Projected = proj[cands[i].PlayerID][key]
	}
	out := func(t Target) int {
		if kind != "dynasty" && slices.Contains(unavailable, t.Injury) {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(cands, func(a, b Target) int {
		byValue, byProj := cmp.Compare(b.Value, a.Value), cmp.Compare(b.Projected, a.Projected)
		if kind == "guillotine" {
			return cmp.Or(cmp.Compare(out(a), out(b)), byProj, byValue)
		}
		return cmp.Or(cmp.Compare(out(a), out(b)), byValue, byProj)
	})
	cands = cands[:min(len(cands), limit)]
	if w.FAABRemaining != nil {
		for i := range cands {
			bid := faabBid(cands[i], *w.FAABRemaining)
			cands[i].Bid = &bid
		}
	}
	return cands
}

// faabBid suggests a bid: value/100 percent of what's left (a 1,000-value
// player gets 10%), at most half, plus 5 points for a player trending hard.
func faabBid(t Target, remaining int) int {
	pct := min(50, t.Value/100)
	if t.Adds >= hotAdds {
		pct += 5
	}
	return max(0, remaining*pct/100)
}

// rankKey sorts unranked (0) players after every ranked one.
func rankKey(r int) int {
	if r <= 0 {
		return math.MaxInt
	}
	return r
}
