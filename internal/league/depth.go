package league

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// unavailable are injury statuses that keep a player out of a lineup.
// Questionable players can usually play and count, but are flagged.
var unavailable = []string{"Out", "Doubtful", "IR", "PUP", "Sus", "NA", "COV", "DNR"}

// Depth statuses.
const (
	depthOK    = "ok"    // at least one healthy backup
	depthThin  = "thin"  // exactly enough: one injury leaves a hole
	depthShort = "short" // can't fill the starting slots right now
)

// DepthReport is the user's positional depth in each league.
type DepthReport struct {
	// ThinSpots lists every thin or short position or flex group across
	// leagues, e.g. "Dynasty 2025: RB short (1 healthy for 2 slots)".
	ThinSpots []string     `json:"thin_spots"`
	Leagues   []DepthChart `json:"leagues"`
}

// DepthChart is one league's depth chart for the user.
type DepthChart struct {
	LeagueID  string          `json:"league_id"`
	League    string          `json:"league"`
	Kind      string          `json:"kind"`
	Positions []PositionDepth `json:"positions"`
	Flex      []FlexDepth     `json:"flex,omitempty"`
	Note      string          `json:"note,omitempty"`
	Error     string          `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// PositionDepth is one position on the user's roster.
type PositionDepth struct {
	Position     string   `json:"position"`
	Slots        int      `json:"slots" jsonschema:"dedicated starting slots for this position (flex counted separately)"`
	Healthy      int      `json:"healthy" jsonschema:"players who can start: not IR/taxi, not Out/Doubtful/IR/PUP/suspended"`
	Starting     int      `json:"starting" jsonschema:"healthy players needed to start here: its own slots plus the flex slots it fills"`
	Backups      int      `json:"backups" jsonschema:"healthy players left after filling this position's slots and the flex slots"`
	Questionable []string `json:"questionable,omitempty"`
	Unavailable  []string `json:"unavailable,omitempty" jsonschema:"hurt or suspended players at this position, with status"`
	Status       string   `json:"status" jsonschema:"ok (a healthy backup), thin (no backup) or short (slots can't all be filled)"`
}

// FlexDepth is one kind of flex slot and whether spare players fill it.
type FlexDepth struct {
	Slot   string `json:"slot" jsonschema:"FLEX, SUPER_FLEX, WRRB_FLEX or REC_FLEX"`
	Slots  int    `json:"slots"`
	Filled int    `json:"filled" jsonschema:"slots covered by healthy players beyond the dedicated slots"`
	Status string `json:"status" jsonschema:"ok (an eligible spare is left after filling it), thin (none left) or short (can't be filled)"`
}

// Depth reports, for leagues matching leagueQuery (empty = all), whether the
// user has healthy starters and backups at each position the league starts.
func (s *Service) Depth(ctx context.Context, leagueQuery string) (DepthReport, error) {
	_, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return DepthReport{}, err
	}
	if leagues, err = matchLeagues(leagues, leagueQuery); err != nil {
		return DepthReport{}, err
	}
	players, err := s.players.Players(ctx)
	if err != nil {
		return DepthReport{}, err
	}

	out := DepthReport{ThinSpots: []string{}, Leagues: make([]DepthChart, len(leagues))}
	for i, p := range s.pools(ctx, leagues, user.UserID) {
		l := p.league
		ld := DepthChart{LeagueID: l.LeagueID, League: l.Name, Kind: l.Kind(), Positions: []PositionDepth{}}
		switch {
		case p.err != nil:
			ld.Error = p.err.Error()
		case len(p.me.Players) == 0 && l.Kind() == "guillotine":
			ld.Note = "eliminated from this league"
		default:
			ld.Positions, ld.Flex = depthChart(l.RosterPositions, p.me, players)
			for _, pd := range ld.Positions {
				// Kickers and defenses are streamed, not backed up: only an
				// empty slot is worth reporting.
				streamed := pd.Position == "K" || pd.Position == "DEF"
				if pd.Status == depthShort || (pd.Status == depthThin && !streamed) {
					out.ThinSpots = append(out.ThinSpots, fmt.Sprintf("%s: %s %s (%d healthy, %d starting, %d backups)",
						l.Name, pd.Position, pd.Status, pd.Healthy, pd.Starting, pd.Backups))
				}
			}
			for _, fd := range ld.Flex {
				if fd.Status != depthOK {
					out.ThinSpots = append(out.ThinSpots, fmt.Sprintf("%s: %s %s (%d of %d filled, no eligible spare left)",
						l.Name, fd.Slot, fd.Status, fd.Filled, fd.Slots))
				}
			}
		}
		out.Leagues[i] = ld
	}
	return out, nil
}

// depthChart works out, for one roster, how the starting slots can be filled
// by healthy players and what is left over.
func depthChart(slots []string, me sleeper.Roster, players map[string]sleeper.Player) ([]PositionDepth, []FlexDepth) {
	// Starting slots, dedicated and flex.
	dedicated := map[string]int{}
	flexCount := map[string]int{}
	for _, s := range slots {
		if slices.Contains(Positions, s) {
			dedicated[s]++
		} else if _, ok := flexSlots[s]; ok {
			flexCount[s]++
		}
	}

	// Who can start: on the roster, not on IR or the taxi squad.
	benched := map[string]bool{}
	for _, id := range append(slices.Clone(me.Reserve), me.Taxi...) {
		benched[id] = true
	}
	byPos := map[string]*PositionDepth{}
	for _, pos := range Positions {
		if dedicated[pos] > 0 || flexEligible(pos, flexCount) {
			byPos[pos] = &PositionDepth{Position: pos, Slots: dedicated[pos]}
		}
	}
	for _, id := range me.Players {
		p := Lookup(players, id)
		pd, ok := byPos[p.Position]
		if !ok {
			continue // a position this league doesn't start (IDP, K without a K slot)
		}
		switch {
		case benched[id] || slices.Contains(unavailable, p.InjuryStatus):
			status := p.InjuryStatus
			if benched[id] && status == "" {
				status = "IR/taxi"
			}
			pd.Unavailable = append(pd.Unavailable, p.Name()+" ("+status+")")
		default:
			pd.Healthy++
			if p.InjuryStatus == "Questionable" {
				pd.Questionable = append(pd.Questionable, p.Name())
			}
		}
	}

	// Spares after dedicated slots fill the flex slots, most restrictive flex
	// first, each time taking from the position with the most spares.
	spare := map[string]int{}
	for pos, pd := range byPos {
		spare[pos] = max(0, pd.Healthy-pd.Slots)
	}
	var flex []FlexDepth
	kinds := make([]string, 0, len(flexCount))
	for k := range flexCount {
		kinds = append(kinds, k)
	}
	slices.SortFunc(kinds, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(flexSlots[a]), len(flexSlots[b])), cmp.Compare(a, b))
	})
	flexUsed := map[string]int{}
	for _, kind := range kinds {
		fd := FlexDepth{Slot: kind, Slots: flexCount[kind]}
		for range fd.Slots {
			best := ""
			for _, pos := range flexSlots[kind] {
				if spare[pos] > 0 && (best == "" || spare[pos] > spare[best]) {
					best = pos
				}
			}
			if best == "" {
				break
			}
			spare[best]--
			flexUsed[best]++
			fd.Filled++
		}
		flex = append(flex, fd)
	}
	// Grade each flex group once every group is filled: it has a backup if
	// any eligible position still has a spare.
	for i := range flex {
		left := 0
		for _, pos := range flexSlots[flex[i].Slot] {
			left += spare[pos]
		}
		flex[i].Status = status(flex[i].Filled, flex[i].Slots, left)
	}

	var out []PositionDepth
	for _, pos := range Positions {
		pd, ok := byPos[pos]
		if !ok {
			continue
		}
		pd.Backups = spare[pos]
		pd.Starting = min(pd.Healthy, pd.Slots) + flexUsed[pos]
		pd.Status = status(pd.Healthy, pd.Slots, pd.Backups)
		// A flex-only position with nobody left over isn't a hole by itself;
		// the flex group reports it.
		if pd.Slots == 0 && pd.Status == depthThin {
			pd.Status = depthOK
		}
		out = append(out, *pd)
	}
	return out, flex
}

// status grades filling need slots from have players with spare left over.
func status(have, need, spare int) string {
	switch {
	case have < need:
		return depthShort
	case spare == 0:
		return depthThin
	default:
		return depthOK
	}
}

// flexEligible reports whether pos can fill any of the league's flex slots.
func flexEligible(pos string, flex map[string]int) bool {
	for kind := range flex {
		if slices.Contains(flexSlots[kind], pos) {
			return true
		}
	}
	return false
}
