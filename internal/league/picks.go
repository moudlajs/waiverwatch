package league

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/moudlajs/waiverwatch/internal/fantasycalc"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// pickQuery is a draft pick as people name it: "2027 1st", "2027 round 2",
// "2027 1st early", "2027 1st chgo" (a team: the pick's original team or
// whoever holds it).
type pickQuery struct {
	season string
	round  int
	slot   string // early, mid or late when named; else projected
	team   string // folded team name fragment, or ""
}

var pickPattern = regexp.MustCompile(`^(20\d\d) (?:round )?(\d)(?:st|nd|rd|th)?(?: round)?(?: (.*))?$`)

// parsePick reads a pick name; ok is false for anything else (a player).
func parsePick(name string) (pickQuery, bool) {
	m := pickPattern.FindStringSubmatch(foldName(name))
	if m == nil {
		return pickQuery{}, false
	}
	round, _ := strconv.Atoi(m[2])
	q := pickQuery{season: m[1], round: round}
	var team []string
	for _, w := range strings.Fields(m[3]) {
		switch w {
		case "early", "mid", "late":
			q.slot = w
		case "from", "of", "via", "own", "owned", "by", "pick":
		default:
			team = append(team, w)
		}
	}
	q.team = strings.Join(team, " ")
	return q, round > 0
}

// draftPick is one draft pick in a league: whose it originally was and who holds
// it now (roster IDs).
type draftPick struct {
	season         string
	round          int
	origin, holder int
}

// leaguePicks lists every pick in the given future drafts: each team's own
// picks for rounds 1..rounds, moved to their current holder by trades.
// Sleeper lists each traded pick once, with its current holder, however many
// times it changed hands (checked 2026-10-06).
func leaguePicks(rosters []sleeper.Roster, traded []sleeper.TradedPick, seasons []string, rounds int) []draftPick {
	var out []draftPick
	for _, season := range seasons {
		for round := 1; round <= rounds; round++ {
			for _, r := range rosters {
				p := draftPick{season: season, round: round, origin: r.RosterID, holder: r.RosterID}
				for _, t := range traded {
					if t.Season == season && t.Round == round && t.RosterID == r.RosterID {
						p.holder = t.OwnerID
					}
				}
				out = append(out, p)
			}
		}
	}
	return out
}

// pickSeasons are the drafts FantasyCalc values picks for, earliest first.
func pickSeasons(market map[string]fantasycalc.Value) []string {
	var out []string
	for _, v := range market {
		if v.Position != "PICK" {
			continue
		}
		if season, _, ok := strings.Cut(v.Name, " "); ok && !slices.Contains(out, season) {
			out = append(out, season)
		}
	}
	slices.Sort(out)
	return out
}

// ordinal is FantasyCalc's round name: 1st, 2nd, 3rd, 4th...
func ordinal(n int) string {
	switch n {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	}
	return strconv.Itoa(n) + "th"
}

// projectSlot guesses where a pick in the next draft lands from its original
// team's standing now: the bottom third picks early, the top third late.
// Empty when the season hasn't started (no games played).
func projectSlot(rosters []sleeper.Roster, origin int) string {
	played := false
	for _, r := range rosters {
		played = played || r.Settings.Wins+r.Settings.Losses+r.Settings.Ties > 0
	}
	if !played || len(rosters) == 0 {
		return ""
	}
	rank, n := Standing(rosters, origin, false), len(rosters)
	switch {
	case rank == 0:
		return "" // not in this league
	case rank*3 <= n:
		return "late"
	case rank*3 > 2*n:
		return "early"
	default:
		return "mid"
	}
}

// pickValue is a pick's FantasyCalc value: by slot ("2027 1st (Early)") when
// slot is set and FantasyCalc values that slot, else the generic round.
func pickValue(market map[string]fantasycalc.Value, season string, round int, slot string) (fantasycalc.Value, bool) {
	base := season + " " + ordinal(round)
	names := []string{base}
	if slot != "" {
		names = []string{base + " (" + strings.ToUpper(slot[:1]) + slot[1:] + ")", base}
	}
	for _, name := range names {
		for _, v := range market {
			if v.Position == "PICK" && v.Name == name {
				return v, true
			}
		}
	}
	return fantasycalc.Value{}, false
}

// describePick names a pick for people: "2027 1st (CHGO's)".
func describePick(p draftPick, team func(rosterID int) string) string {
	s := fmt.Sprintf("%s %s (%s's", p.season, ordinal(p.round), team(p.origin))
	if p.holder != p.origin {
		s += ", held by " + team(p.holder)
	}
	return s + ")"
}
