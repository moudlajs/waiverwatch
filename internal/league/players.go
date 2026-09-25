// Package league holds the fantasy logic: joins between rosters, owners and
// players. It knows football, not HTTP or MCP.
package league

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/store"
)

// MaxPlayersAge is how long a fetched player dictionary is used before it is
// refreshed. Sleeper asks for at most one fetch a day.
const MaxPlayersAge = 24 * time.Hour

// FetchPlayers downloads the player dictionary, normally
// (*sleeper.Client).Players.
type FetchPlayers func(ctx context.Context) (map[string]sleeper.Player, error)

// Directory hands out the player dictionary, fetching it on first use and
// again once it is older than MaxPlayersAge.
type Directory struct {
	store store.Store
	fetch FetchPlayers
	now   func() time.Time

	// refresh serialises fetches, so concurrent first callers share one
	// download instead of each pulling 15 MB.
	refresh sync.Mutex
}

// NewDirectory returns a Directory backed by s.
func NewDirectory(s store.Store, fetch FetchPlayers) *Directory {
	return &Directory{store: s, fetch: fetch, now: time.Now}
}

// Players returns the dictionary keyed by player ID. If a refresh fails but
// an older copy exists, the older copy is returned: names and positions
// rarely change, and a stale name beats no answer.
func (d *Directory) Players(ctx context.Context) (map[string]sleeper.Player, error) {
	cur, err := d.store.Players(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading player dictionary: %w", err)
	}
	if d.fresh(cur) {
		return cur.ByID, nil
	}

	d.refresh.Lock()
	defer d.refresh.Unlock()

	// Another caller may have refreshed while this one waited.
	if cur, err = d.store.Players(ctx); err != nil {
		return nil, fmt.Errorf("reading player dictionary: %w", err)
	}
	if d.fresh(cur) {
		return cur.ByID, nil
	}

	byID, err := d.fetch(ctx)
	if err != nil {
		if cur.ByID != nil {
			slog.WarnContext(ctx, "player dictionary refresh failed, using stale copy",
				"age", d.now().Sub(cur.FetchedAt).Round(time.Minute), "err", err)
			return cur.ByID, nil
		}
		return nil, fmt.Errorf("refreshing player dictionary: %w", err)
	}
	if err := d.store.SavePlayers(ctx, store.Players{ByID: byID, FetchedAt: d.now()}); err != nil {
		return nil, fmt.Errorf("saving player dictionary: %w", err)
	}
	return byID, nil
}

func (d *Directory) fresh(p store.Players) bool {
	return p.ByID != nil && d.now().Sub(p.FetchedAt) < MaxPlayersAge
}

// Lookup returns the player with id. Unknown IDs (new signings the
// dictionary hasn't caught up with) come back with the ID as their name
// rather than failing the whole answer.
func Lookup(players map[string]sleeper.Player, id string) sleeper.Player {
	if p, ok := players[id]; ok {
		return p
	}
	return sleeper.Player{PlayerID: id, FullName: id}
}
