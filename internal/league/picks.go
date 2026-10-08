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

// pickQuery is a pick as people name it: "2027 1st", "2027 round 2 early", "2027 1st chgo".
type pickQuery struct {
	season string
	round  int
	slot   string // early, mid or late when named; else projected
	team   string // folded team name fragment, or ""
}

var pickPattern = regexp.MustCompile(`^(20\d\d) (?:round )?(\d)(?:st|nd|rd|th)?(?: round)?(?: (.*))?$`)

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

type draftPick struct {
	season         string
	round          int
	origin, holder int
}

// leaguePicks: Sleeper lists each traded pick once, with its current holder, however often it moved.
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

// projectSlot guesses early/mid/late from the original team's standing; empty before any games.
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
		return ""
	case rank*3 <= n:
		return "late"
	case rank*3 > 2*n:
		return "early"
	default:
		return "mid"
	}
}

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

func describePick(p draftPick, team func(rosterID int) string) string {
	s := fmt.Sprintf("%s %s (%s's", p.season, ordinal(p.round), team(p.origin))
	if p.holder != p.origin {
		s += ", held by " + team(p.holder)
	}
	return s + ")"
}
