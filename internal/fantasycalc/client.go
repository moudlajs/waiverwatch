// Package fantasycalc is a read-only client for FantasyCalc's public trade
// values: what players are worth in trades, built from real fantasy trades.
// No key, no auth. It knows HTTP and JSON, nothing about leagues or MCP.
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

// MaxAge is how long fetched values are reused. They move with trades over
// days, not minutes; a few hours old is fine.
const MaxAge = 3 * time.Hour

// retryWait is how long a stale copy is served without asking again after a
// failed refresh, so an outage doesn't cost every call a timeout.
const retryWait = time.Minute

// maxBody caps a response; a full dynasty list is ~330 KB.
const maxBody = 8 << 20

// ErrUnavailable is worded for people: tools pass it to Claude, which passes
// it on.
var ErrUnavailable = errors.New("FantasyCalc (the trade value source) isn't responding right now; try again shortly")

// Settings picks a FantasyCalc market. Values differ by format, QB count,
// league size and scoring.
type Settings struct {
	Dynasty bool
	QBs     int     // 1, or 2 for superflex and 2QB leagues
	Teams   int     // league size
	PPR     float64 // points per reception
}

// Normalise maps s onto the markets FantasyCalc distinguishes: 1 or 2 QBs,
// 8 to 14 teams in steps of two (it answers larger sizes with its 12-team
// values, so bigger leagues get the nearest real market, 14), and 0, 0.5 or
// 1 PPR.
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
// Call it on normalised settings to name the market actually used.
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
	Position     string // QB, RB, WR, TE, or PICK
	Team         string // NFL team; empty for free agents and picks
	Value        int
	OverallRank  int
	PositionRank int
	Tier         int // 0 when FantasyCalc gives none
	Trend30Day   int // value change over the last 30 days
}

// Client fetches values. The zero value is not usable; use New.
type Client struct {
	http *http.Client
	base string
	now  func() time.Time

	mu     sync.Mutex
	cache  map[Settings]snapshot
	flight singleflight.Group // concurrent misses for one market share a fetch
}

type snapshot struct {
	byID       map[string]Value
	fetched    time.Time
	retryAfter time.Time // after a failed refresh: don't ask again before this
}

// New returns a client for baseURL, normally DefaultBaseURL. Tests pass an
// httptest server URL instead.
func New(baseURL string) *Client {
	return &Client{
		http:  &http.Client{Timeout: 10 * time.Second},
		base:  baseURL,
		now:   time.Now,
		cache: make(map[Settings]snapshot),
	}
}

// Values returns the market for s keyed by Sleeper player ID. If a refresh
// fails but an older copy exists, the older copy is returned: values a few
// hours stale beat no answer.
func (c *Client) Values(ctx context.Context, s Settings) (map[string]Value, error) {
	s = s.Normalise()
	cur, ok := c.cached(s)
	if ok && (c.now().Sub(cur.fetched) < MaxAge || c.now().Before(cur.retryAfter)) {
		return cur.byID, nil
	}

	// Shared by every caller waiting on this market, so it must not die with
	// whichever caller started it (the HTTP client has its own timeout).
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

// entry is one element of FantasyCalc's response; only the fields
// waiverwatch uses are mapped.
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
			continue // can't be joined to Sleeper
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
