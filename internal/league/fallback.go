package league

import (
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/moudlajs/waiverwatch/internal/dynastyprocess"
	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

const fallbackNote = "FantasyCalc isn't responding, so these values come from DynastyProcess (open dynasty values, " +
	"updated weekly): 1QB or superflex only, not fitted to league size, scoring or redraft, and no draft picks"

// WithFallback serves values FantasyCalc can't provide from fallback, with a note.
func (s *Service) WithFallback(fallback FetchValues) *Service {
	s.fallback = fallback
	return s
}

// market returns FantasyCalc's values, else the fallback's; if both fail, FantasyCalc's error.
func (s *Service) market(ctx context.Context, settings fantasycalc.Settings) (map[string]fantasycalc.Value, string, error) {
	m, err := s.values(ctx, settings)
	if err == nil || s.fallback == nil || ctx.Err() != nil {
		return m, "", err
	}
	fm, ferr := s.fallback(ctx, settings)
	if ferr == nil {
		return fm, fallbackNote, nil
	}
	slog.WarnContext(ctx, "both trade value sources failed", "fantasycalc", err, "backup", ferr)
	return nil, "", err
}

func marketLabel(settings fantasycalc.Settings, note string) string {
	if note == "" {
		return settings.String()
	}
	if settings.QBs >= 2 {
		return "DynastyProcess dynasty superflex (backup)"
	}
	return "DynastyProcess dynasty 1QB (backup)"
}

func redraftNote(l sleeper.League, source string) string {
	if k := l.Kind(); source == "" && (k == "keeper" || k == "guillotine") {
		return k + " league: valued with redraft values"
	}
	return ""
}

// DynastyProcessValues adapts DynastyProcess values (1QB or superflex by settings.QBs) to FetchValues.
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

func addNote(field *string, note string) {
	switch {
	case note == "":
	case *field == "":
		*field = note
	default:
		*field += "; " + note
	}
}
