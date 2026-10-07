package league

import (
	"cmp"
	"fmt"
	"sync"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// A complete feed has points for at least minProjected players (team
// defenses aside), or perTeam per team playing on a small slate (playoff
// weeks). A real week has hundreds; Sleeper blanks them while it refreshes
// mid-week, leaving only the defenses, which also tell how many teams play.
const (
	minProjected = 150
	perTeam      = 12
)

// projectionCache keeps the last complete projections per week, to serve
// while Sleeper's feed is blank. In memory: lost on scale-to-zero, fine.
type projectionCache struct {
	min  int // players needed for a complete feed; 0 means minProjected (tests lower it)
	mu   sync.Mutex
	last map[string]projSnapshot // by season type/season/week
}

type projSnapshot struct {
	stats   map[string]map[string]float64
	fetched time.Time
}

// complete checks a fetched feed. A complete one is remembered and returned
// as is. An incomplete one is swapped for the last complete set for the same
// week, with a note; failing that it is returned with a note and ok false,
// so callers don't treat missing points as zeros.
func (c *projectionCache) complete(key string, feed map[string]map[string]float64, players map[string]sleeper.Player, now time.Time) (proj map[string]map[string]float64, note string, ok bool) {
	n, teams := 0, 0
	for id, stats := range feed {
		if _, has := stats["pts_ppr"]; has {
			if players[id].Position == "DEF" {
				teams++
			} else {
				n++
			}
		}
	}
	need := cmp.Or(c.min, minProjected)
	if teams > 0 {
		need = min(need, teams*perTeam)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n >= need {
		if c.last == nil {
			c.last = make(map[string]projSnapshot)
		}
		c.last[key] = projSnapshot{stats: feed, fetched: now}
		return feed, "", true
	}
	if snap, found := c.last[key]; found {
		return snap.stats, fmt.Sprintf("Sleeper is refreshing this week's projections; these are the last complete ones, from %s UTC",
			snap.fetched.UTC().Format("Mon 15:04")), true
	}
	return feed, "Sleeper's player projections for this week are missing right now (it refreshes them during the week); try again later", false
}

func weekKey(seasonType, season string, week int) string {
	return fmt.Sprintf("%s/%s/%d", seasonType, season, week)
}
