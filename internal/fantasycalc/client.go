// Package fantasycalc is a read-only client for FantasyCalc's public trade values.
// It knows HTTP and JSON, nothing about leagues or MCP.
package fantasycalc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultBaseURL is the public FantasyCalc API.
const DefaultBaseURL = "https://api.fantasycalc.com"

// MaxAge is how long fetched values are reused.
const MaxAge = 3 * time.Hour

// retryWait: after a failed refresh, serve the stale copy this long before retrying.
const retryWait = time.Minute

const maxBody = 8 << 20

// ErrUnavailable is worded for people: tools pass it on as is.
var ErrUnavailable = errors.New("FantasyCalc (the trade value source) isn't responding right now; try again shortly")

// Settings picks a FantasyCalc market.
type Settings struct {
	Dynasty bool
	QBs     int // 1, or 2 for superflex and 2QB leagues
	Teams   int
	PPR     float64
}

// Normalise maps s onto FantasyCalc's markets; 16+ teams silently get 12-team values, so cap at 14.
func (s Settings) Normalise() Settings {
	if s.QBs >= 2 {
		s.QBs = 2
	} else {
		s.QBs = 1
	}
	s.Teams = min(max(s.Teams+s.Teams%2, 8), 14)
	s.PPR = min(max(math.Round(s.PPR*2)/2, 0), 1)
	return s
}

// String names the market for people, e.g. "dynasty superflex 12-team PPR".
func (s Settings) String() string {
	format, qbs, scoring := "redraft", "1QB", "PPR"
	if s.Dynasty {
		format = "dynasty"
	}
	if s.QBs >= 2 {
		qbs = "superflex"
	}
	switch s.PPR {
	case 0:
		scoring = "standard"
	case 0.5:
		scoring = "half PPR"
	}
	return fmt.Sprintf("%s %s %d-team %s", format, qbs, s.Teams, scoring)
}

// Value is one player's (or, in dynasty, one draft pick's) trade value.
type Value struct {
	SleeperID    string // draft picks carry FantasyCalc's own IDs, e.g. FP_2027_early_0
	Name         string
	Position     string
	Team         string
	Value        int
	OverallRank  int
	PositionRank int
	Tier         int
	Trend30Day   int
}

// Client fetches values. The zero value is not usable; use New.
type Client struct {
	http *http.Client
	base string
	now  func() time.Time

	mu     sync.Mutex
	cache  map[Settings]snapshot
	flight singleflight.Group
}

type snapshot struct {
	byID       map[string]Value
	fetched    time.Time
	retryAfter time.Time
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(baseURL string) *Client {
	return &Client{
		http:  &http.Client{Timeout: 10 * time.Second},
		base:  baseURL,
		now:   time.Now,
		cache: make(map[Settings]snapshot),
	}
}

// Values returns the market for s keyed by Sleeper ID, serving a stale copy if a refresh fails.
func (c *Client) Values(ctx context.Context, s Settings) (map[string]Value, error) {
	s = s.Normalise()
	cur, ok := c.cached(s)
	if ok && (c.now().Sub(cur.fetched) < MaxAge || c.now().Before(cur.retryAfter)) {
		return cur.byID, nil
	}

	// Detached: the shared fetch must not die with whichever caller started it.
	shared := context.WithoutCancel(ctx)
	ch := c.flight.DoChan(s.query(), func() (any, error) {
		byID, err := c.fetch(shared, s)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.cache[s] = snapshot{byID: byID, fetched: c.now()}
		c.mu.Unlock()
		return byID, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			if ok {
				c.mu.Lock()
				if snap := c.cache[s]; snap.fetched.Equal(cur.fetched) { // not refreshed meanwhile
					snap.retryAfter = c.now().Add(retryWait)
					c.cache[s] = snap
				}
				c.mu.Unlock()
				slog.WarnContext(ctx, "fantasycalc refresh failed, using stale values",
					"age", c.now().Sub(cur.fetched).Round(time.Minute), "err", r.Err)
				return cur.byID, nil
			}
			return nil, r.Err
		}
		return r.Val.(map[string]Value), nil
	}
}

func (c *Client) cached(s Settings) (snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.cache[s]
	return snap, ok
}

func (s Settings) query() string {
	q := url.Values{}
	q.Set("isDynasty", strconv.FormatBool(s.Dynasty))
	q.Set("numQbs", strconv.Itoa(s.QBs))
	q.Set("numTeams", strconv.Itoa(s.Teams))
	q.Set("ppr", strconv.FormatFloat(s.PPR, 'f', -1, 64))
	return q.Encode()
}

type entry struct {
	Player struct {
		Name      string `json:"name"`
		SleeperID string `json:"sleeperId"`
		Position  string `json:"position"`
		Team      string `json:"maybeTeam"`
	} `json:"player"`
	Value        int `json:"value"`
	OverallRank  int `json:"overallRank"`
	PositionRank int `json:"positionRank"`
	Tier         int `json:"maybeTier"`
	Trend30Day   int `json:"trend30Day"`
}

func (c *Client) fetch(ctx context.Context, s Settings) (map[string]Value, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/values/current?"+s.query(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("fetching FantasyCalc values: unexpected status %s", resp.Status)
	}

	var entries []entry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decoding FantasyCalc values: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w (no values returned)", ErrUnavailable)
	}
	byID := make(map[string]Value, len(entries))
	for _, e := range entries {
		if e.Player.SleeperID == "" {
			continue
		}
		byID[e.Player.SleeperID] = Value{
			SleeperID:    e.Player.SleeperID,
			Name:         e.Player.Name,
			Position:     e.Player.Position,
			Team:         e.Player.Team,
			Value:        e.Value,
			OverallRank:  e.OverallRank,
			PositionRank: e.PositionRank,
			Tier:         e.Tier,
			Trend30Day:   e.Trend30Day,
		}
	}
	return byID, nil
}
