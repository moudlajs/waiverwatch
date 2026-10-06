// Package dynastyprocess is a read-only client for DynastyProcess's open
// dynasty values (github.com/dynastyprocess/data, refreshed weekly), joined
// to Sleeper player IDs through their ID table. It knows HTTP and CSV,
// nothing about leagues or MCP.
package dynastyprocess

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultBaseURL serves the data repository's files.
const DefaultBaseURL = "https://raw.githubusercontent.com/dynastyprocess/data/master/files"

// MaxAge is how long fetched values are reused; the data changes weekly.
const MaxAge = 6 * time.Hour

// retryWait is how long a stale copy is served without asking again after a
// failed refresh.
const retryWait = time.Minute

// maxBody caps a file; the ID table is ~3 MB.
const maxBody = 16 << 20

// ErrUnavailable is worded for people.
var ErrUnavailable = errors.New("DynastyProcess (the backup trade value source) isn't responding right now; try again shortly")

// Player is one player's dynasty value.
type Player struct {
	SleeperID string
	Name      string
	Position  string
	Team      string
	Value1QB  int // value in 1QB leagues
	Value2QB  int // value in superflex and 2QB leagues
}

// Client fetches values. The zero value is not usable; use New.
type Client struct {
	http *http.Client
	base string
	now  func() time.Time

	mu         sync.Mutex
	bySleeper  map[string]Player
	fetched    time.Time
	retryAfter time.Time
	flight     singleflight.Group
}

// New returns a client for baseURL, normally DefaultBaseURL.
func New(baseURL string) *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}, base: baseURL, now: time.Now}
}

// Values returns every valued player keyed by Sleeper player ID. If a
// refresh fails but an older copy exists, the older copy is returned.
func (c *Client) Values(ctx context.Context) (map[string]Player, error) {
	c.mu.Lock()
	cur, fetched, retryAfter := c.bySleeper, c.fetched, c.retryAfter
	c.mu.Unlock()
	if cur != nil && (c.now().Sub(fetched) < MaxAge || c.now().Before(retryAfter)) {
		return cur, nil
	}

	shared := context.WithoutCancel(ctx) // shared by every waiting caller
	ch := c.flight.DoChan("values", func() (any, error) {
		byID, err := c.fetch(shared)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.bySleeper, c.fetched, c.retryAfter = byID, c.now(), time.Time{}
		c.mu.Unlock()
		return byID, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err == nil {
			return r.Val.(map[string]Player), nil
		}
		if cur == nil {
			return nil, r.Err
		}
		c.mu.Lock()
		c.retryAfter = c.now().Add(retryWait)
		c.mu.Unlock()
		slog.WarnContext(ctx, "dynastyprocess refresh failed, using stale values", "err", r.Err)
		return cur, nil
	}
}

func (c *Client) fetch(ctx context.Context) (map[string]Player, error) {
	ids, err := c.table(ctx, "db_playerids.csv")
	if err != nil {
		return nil, err
	}
	sleeperOf := make(map[string]string)
	for _, row := range ids {
		fp, sl := row["fantasypros_id"], row["sleeper_id"]
		if known(fp) && known(sl) {
			sleeperOf[fp] = sl
		}
	}
	values, err := c.table(ctx, "values-players.csv")
	if err != nil {
		return nil, err
	}
	out := make(map[string]Player, len(values))
	for _, row := range values {
		id, ok := sleeperOf[row["fp_id"]]
		if !ok {
			continue // can't be joined to Sleeper
		}
		v1, err1 := strconv.Atoi(row["value_1qb"])
		v2, err2 := strconv.Atoi(row["value_2qb"])
		if err1 != nil || err2 != nil {
			continue
		}
		out[id] = Player{SleeperID: id, Name: row["player"], Position: row["pos"], Team: row["team"], Value1QB: v1, Value2QB: v2}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w (no values could be read)", ErrUnavailable)
	}
	return out, nil
}

// known reports whether a CSV cell holds an ID (R writes missing ones as NA).
func known(s string) bool { return s != "" && s != "NA" }

// table downloads a CSV file and returns its rows keyed by header.
func (c *Client) table(ctx context.Context, name string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (HTTP %d for %s)", ErrUnavailable, resp.StatusCode, name)
	}
	r := csv.NewReader(io.LimitReader(resp.Body, maxBody))
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	var rows []map[string]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		row := make(map[string]string, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		rows = append(rows, row)
	}
}
