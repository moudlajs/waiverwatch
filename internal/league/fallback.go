package league

import (
	"cmp"
	"context"
	"slices"

	"github.com/moudlajs/waiverwatch/internal/dynastyprocess"
	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
)

// fallbackNote goes on every answer valued by the backup source.
const fallbackNote = "FantasyCalc isn't responding, so these values come from DynastyProcess (open dynasty values, " +
	"updated weekly): 1QB or superflex only, not fitted to league size, scoring or redraft, and no draft picks"

// WithFallback makes values that FantasyCalc can't provide come from
// fallback instead, with a note saying so.
func (s *Service) WithFallback(fallback FetchValues) *Service {
	s.fallback = fallback
	return s
}

// market returns the trade values for settings: FantasyCalc's, else the
// fallback's with fallbackNote. When both fail, FantasyCalc's error is
// returned: it is the source people know.
func (s *Service) market(ctx context.Context, settings fantasycalc.Settings) (map[string]fantasycalc.Value, string, error) {
	m, err := s.values(ctx, settings)
	if err == nil || s.fallback == nil {
		return m, "", err
	}
	if fm, ferr := s.fallback(ctx, settings); ferr == nil {
		return fm, fallbackNote, nil
	}
	return nil, "", err
}

// DynastyProcessValues adapts DynastyProcess's values to FetchValues: 1QB or
// superflex values by settings.QBs, ranked overall and by position.
func DynastyProcessValues(fetch func(context.Context) (map[string]dynastyprocess.Player, error)) FetchValues {
	return func(ctx context.Context, s fantasycalc.Settings) (map[string]fantasycalc.Value, error) {
		players, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		vals := make([]fantasycalc.Value, 0, len(players))
		for _, p := range players {
			v := p.Value1QB
			if s.Normalise().QBs == 2 {
				v = p.Value2QB
			}
			vals = append(vals, fantasycalc.Value{SleeperID: p.SleeperID, Name: p.Name, Position: p.Position, Team: p.Team, Value: v})
		}
		slices.SortFunc(vals, func(a, b fantasycalc.Value) int {
			return cmp.Or(cmp.Compare(b.Value, a.Value), cmp.Compare(a.SleeperID, b.SleeperID))
		})
		byPos := map[string]int{}
		out := make(map[string]fantasycalc.Value, len(vals))
		for i, v := range vals {
			byPos[v.Position]++
			v.OverallRank, v.PositionRank = i+1, byPos[v.Position]
			out[v.SleeperID] = v
		}
		return out, nil
	}
}

// addNote appends note to a free-text note field.
func addNote(field *string, note string) {
	switch {
	case note == "":
	case *field == "":
		*field = note
	default:
		*field += "; " + note
	}
}
