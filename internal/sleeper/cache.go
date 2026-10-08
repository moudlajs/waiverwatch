package sleeper

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// How long responses are reused; shared by every caller of a Client.
const (
	ttlShared  = 5 * time.Minute  // NFL state, trending adds, projections: the same for everyone
	ttlSlow    = 10 * time.Minute // username lookups, a user's leagues, draft lists
	ttlLive    = time.Minute      // rosters, members, matchups: live, but a minute old is fine
	ttlHistory = 24 * time.Hour   // earlier seasons' leagues, completed drafts' picks

	cacheMaxBytes = 64 << 20 // well inside the 256 MiB container
)

// cache keeps raw JSON responses, decoded afresh per caller so no two callers share a value.
type cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	size    int
	max     int
	now     func() time.Time
	flight  singleflight.Group
}

type cacheEntry struct {
	raw     []byte
	expires time.Time
}

func newCache(maxBytes int) *cache {
	return &cache{entries: make(map[string]cacheEntry), max: maxBytes, now: time.Now}
}

func (c *cache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.raw, true
}

func (c *cache) put(key string, raw []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(raw) > c.max {
		return
	}
	if old, ok := c.entries[key]; ok {
		c.size -= len(old.raw)
		delete(c.entries, key)
	}
	if c.size+len(raw) > c.max {
		now := c.now()
		for k, e := range c.entries { // expired first
			if !now.Before(e.expires) {
				c.size -= len(e.raw)
				delete(c.entries, k)
			}
		}
		for k, e := range c.entries { // then whatever is needed
			if c.size+len(raw) <= c.max {
				break
			}
			c.size -= len(e.raw)
			delete(c.entries, k)
		}
	}
	c.entries[key] = cacheEntry{raw: raw, expires: c.now().Add(ttl)}
	c.size += len(raw)
}
