// Package sleeper is a read-only client for the public Sleeper API.
// It knows HTTP and JSON, nothing about MCP or fantasy strategy.
package sleeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is the public Sleeper API. No key, no auth.
const DefaultBaseURL = "https://api.sleeper.app/v1"

// ErrNotFound is returned for unknown users, leagues and the like. Sleeper
// usually answers those with 200 and a JSON null rather than a 404.
var ErrNotFound = errors.New("not found")

// ErrBusy is returned when the call budget toward Sleeper is used up for
// longer than a caller should wait.
var ErrBusy = errors.New("waiverwatch has used up its Sleeper call budget for now; try again in a minute")

// Sleeper asks apps to stay under 1000 calls a minute per IP and may block
// above it. Every user of a hosted server shares its egress, so the client
// holds all its calls to this budget, waiting at most budgetWait for room.
const (
	callsPerSecond = 10 // 600 a minute
	callBurst      = 50
	budgetWait     = 10 * time.Second
)

// Client calls the Sleeper API. The zero value is not usable; use New.
type Client struct {
	http   *http.Client
	base   string
	budget *rate.Limiter
}

// New returns a client for baseURL, normally DefaultBaseURL. Tests pass an
// httptest server URL instead.
func New(baseURL string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 10 * time.Second},
		base:   baseURL,
		budget: rate.NewLimiter(callsPerSecond, callBurst),
	}
}

// SetBudget replaces the call budget (tests, or a different deployment).
func (c *Client) SetBudget(perSecond rate.Limit, burst int) {
	c.budget = rate.NewLimiter(perSecond, burst)
}

// User looks up a user by username or user ID.
func (c *Client) User(ctx context.Context, username string) (User, error) {
	var u User
	if err := c.get(ctx, "/user/"+url.PathEscape(username), &u); err != nil {
		return User{}, fmt.Errorf("fetching user %q: %w", username, err)
	}
	return u, nil
}

// Leagues lists a user's NFL leagues for a season.
func (c *Client) Leagues(ctx context.Context, userID, season string) ([]League, error) {
	var ls []League
	path := fmt.Sprintf("/user/%s/leagues/nfl/%s", url.PathEscape(userID), url.PathEscape(season))
	if err := c.get(ctx, path, &ls); err != nil {
		return nil, fmt.Errorf("fetching leagues for user %s: %w", userID, err)
	}
	return ls, nil
}

// League returns one league by ID.
func (c *Client) League(ctx context.Context, leagueID string) (League, error) {
	var l League
	if err := c.get(ctx, "/league/"+url.PathEscape(leagueID), &l); err != nil {
		return League{}, fmt.Errorf("fetching league %s: %w", leagueID, err)
	}
	return l, nil
}

// Drafts lists a league's drafts.
func (c *Client) Drafts(ctx context.Context, leagueID string) ([]Draft, error) {
	var ds []Draft
	if err := c.get(ctx, "/league/"+url.PathEscape(leagueID)+"/drafts", &ds); err != nil {
		return nil, fmt.Errorf("fetching drafts for league %s: %w", leagueID, err)
	}
	return ds, nil
}

// DraftPicks returns every pick made in a draft, in order.
func (c *Client) DraftPicks(ctx context.Context, draftID string) ([]Pick, error) {
	var ps []Pick
	if err := c.get(ctx, "/draft/"+url.PathEscape(draftID)+"/picks", &ps); err != nil {
		return nil, fmt.Errorf("fetching picks for draft %s: %w", draftID, err)
	}
	return ps, nil
}

// Rosters returns every team's roster in a league.
func (c *Client) Rosters(ctx context.Context, leagueID string) ([]Roster, error) {
	var rs []Roster
	if err := c.get(ctx, "/league/"+url.PathEscape(leagueID)+"/rosters", &rs); err != nil {
		return nil, fmt.Errorf("fetching rosters for league %s: %w", leagueID, err)
	}
	return rs, nil
}

// LeagueUsers returns the members of a league, with their display names.
func (c *Client) LeagueUsers(ctx context.Context, leagueID string) ([]LeagueUser, error) {
	var us []LeagueUser
	if err := c.get(ctx, "/league/"+url.PathEscape(leagueID)+"/users", &us); err != nil {
		return nil, fmt.Errorf("fetching users for league %s: %w", leagueID, err)
	}
	return us, nil
}

// Matchups returns a league's matchups for one week, with live points.
func (c *Client) Matchups(ctx context.Context, leagueID string, week int) ([]Matchup, error) {
	var ms []Matchup
	path := fmt.Sprintf("/league/%s/matchups/%d", url.PathEscape(leagueID), week)
	if err := c.get(ctx, path, &ms); err != nil {
		return nil, fmt.Errorf("fetching week %d matchups for league %s: %w", week, leagueID, err)
	}
	return ms, nil
}

// State returns the current NFL season and week.
func (c *Client) State(ctx context.Context) (State, error) {
	var s State
	if err := c.get(ctx, "/state/nfl", &s); err != nil {
		return State{}, fmt.Errorf("fetching NFL state: %w", err)
	}
	return s, nil
}

// TrendingAdds returns the most-added players over the last lookbackHours.
func (c *Client) TrendingAdds(ctx context.Context, lookbackHours, limit int) ([]Trending, error) {
	q := url.Values{}
	q.Set("lookback_hours", strconv.Itoa(lookbackHours))
	q.Set("limit", strconv.Itoa(limit))
	var ts []Trending
	if err := c.get(ctx, "/players/nfl/trending/add?"+q.Encode(), &ts); err != nil {
		return nil, fmt.Errorf("fetching trending adds: %w", err)
	}
	return ts, nil
}

// Players returns the full NFL player dictionary, keyed by player ID. It is
// ~15 MB; Sleeper asks callers to fetch it at most once a day.
func (c *Client) Players(ctx context.Context) (map[string]Player, error) {
	var ps map[string]Player
	if err := c.get(ctx, "/players/nfl", &ps); err != nil {
		return nil, fmt.Errorf("fetching player dictionary: %w", err)
	}
	return ps, nil
}

// get fetches base+path and decodes the JSON body into dst.
func (c *Client) get(ctx context.Context, path string, dst any) error {
	wait, cancel := context.WithTimeout(ctx, budgetWait)
	err := c.budget.Wait(wait)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrBusy
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if bytes.Equal(raw, []byte("null")) {
		return ErrNotFound
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
