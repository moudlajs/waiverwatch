package league

import (
	"cmp"
	"fmt"
	"sync"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Sleeper blanks projections mid-week (only defenses remain); a feed below these counts is incomplete.
const (
	minProjected = 150
	perTeam      = 12
)

// projectionCache keeps the last complete projections per week, served while Sleeper's feed is blank.
type projectionCache struct {
	min  int // 0 means minProjected
	mu   sync.Mutex
	last map[string]projSnapshot
}

type projSnapshot struct {
	stats   map[string]map[string]float64
	fetched time.Time
}

// complete returns a complete feed as is, else the week's last complete set; ok is false if neither.
func (c *projectionCache) complete(key string, feed map[string]map[string]float64, players map[string]sleeper.Player, now time.Time) (proj map[string]map[string]float64, note string, ok bool) {
	n, teams := 0, 0
	for id, stats := range feed {
		if hasPoints(stats) {
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

// hasPoints reports whether stats include fantasy points (bye weeks and blank feeds lack them).
func hasPoints(stats map[string]float64) bool {
	for _, k := range []string{"pts_ppr", "pts_half_ppr", "pts_std"} {
		if _, ok := stats[k]; ok {
			return true
		}
	}
	return false
}

func weekKey(seasonType, season string, week int) string {
	return fmt.Sprintf("%s/%s/%d", seasonType, season, week)
}
