package sleeper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer answers every path with body and counts requests per path.
func countingServer(t *testing.T, status int, body string) (*Client, *sync.Map) {
	t.Helper()
	var hits sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := hits.LoadOrStore(r.URL.Path, new(atomic.Int32))
		n.(*atomic.Int32).Add(1)
		time.Sleep(5 * time.Millisecond) // let concurrent callers overlap
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &hits
}

func hitsFor(m *sync.Map, path string) int32 {
	n, ok := m.Load(path)
	if !ok {
		return 0
	}
	return n.(*atomic.Int32).Load()
}

func TestCacheReusesResponses(t *testing.T) {
	c, hits := countingServer(t, http.StatusOK, `[{"roster_id":1}]`)
	now := time.Now()
	c.cache.now = func() time.Time { return now }
	ctx := context.Background()

	for range 3 {
		rs, err := c.Rosters(ctx, "L1")
		if err != nil || len(rs) != 1 {
			t.Fatalf("rosters: %v %v", rs, err)
		}
	}
	if got := hitsFor(hits, "/league/L1/rosters"); got != 1 {
		t.Errorf("3 calls within a minute made %d requests, want 1", got)
	}

	// Another league is another entry.
	_, _ = c.Rosters(ctx, "L2")
	if got := hitsFor(hits, "/league/L2/rosters"); got != 1 {
		t.Errorf("L2 requests = %d", got)
	}

	// After the TTL it's fetched again.
	now = now.Add(ttlLive + time.Second)
	_, _ = c.Rosters(ctx, "L1")
	if got := hitsFor(hits, "/league/L1/rosters"); got != 2 {
		t.Errorf("after expiry: %d requests, want 2", got)
	}
}

func TestCacheSharesOneFetchBetweenConcurrentCallers(t *testing.T) {
	c, hits := countingServer(t, http.StatusOK, `[{"roster_id":1}]`)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.Rosters(context.Background(), "L1"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := hitsFor(hits, "/league/L1/rosters"); got != 1 {
		t.Errorf("20 simultaneous callers made %d requests, want 1", got)
	}
}

func TestCacheNeverKeepsErrors(t *testing.T) {
	c, hits := countingServer(t, http.StatusServiceUnavailable, `oops`)
	for range 2 {
		if _, err := c.Rosters(context.Background(), "L1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	}
	if got := hitsFor(hits, "/league/L1/rosters"); got != 2 {
		t.Errorf("failed responses were cached: %d requests, want 2", got)
	}
}

func TestPlayersAreNotCachedHere(t *testing.T) {
	c, hits := countingServer(t, http.StatusOK, `{"1":{"player_id":"1"}}`)
	for range 2 {
		if _, err := c.Players(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := hitsFor(hits, "/players/nfl"); got != 2 {
		t.Errorf("player dictionary requests = %d, want 2 (league.Directory caches it)", got)
	}
}

func TestFriendlyErrors(t *testing.T) {
	tests := []struct {
		status int
		want   error
		text   string
	}{
		{http.StatusTooManyRequests, ErrRateLimited, "limiting requests"},
		{http.StatusBadGateway, ErrUnavailable, "isn't responding"},
		{http.StatusNotFound, ErrNotFound, "not found"},
	}
	for _, tt := range tests {
		c, _ := countingServer(t, tt.status, ``)
		_, err := c.State(context.Background())
		if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.text) {
			t.Errorf("HTTP %d: %v, want %v", tt.status, err, tt.want)
		}
	}

	// Unreachable host: ErrUnavailable, keeping the cause.
	c := New("http://127.0.0.1:1")
	if _, err := c.State(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unreachable: %v, want ErrUnavailable", err)
	}
}

func TestCacheSizeLimit(t *testing.T) {
	c := newCache(10)
	c.put("a", []byte("12345"), time.Hour)
	c.put("b", []byte("12345"), time.Hour)
	c.put("c", []byte("12345"), time.Hour) // over the cap: something goes
	if c.size > 10 {
		t.Errorf("size %d over the cap", c.size)
	}
	if _, ok := c.get("c"); !ok {
		t.Error("the newest entry should be kept")
	}
	c.put("huge", []byte("12345678901"), time.Hour) // bigger than the cache: not stored
	if _, ok := c.get("huge"); ok || c.size > 10 {
		t.Error("an entry bigger than the cache must not be stored")
	}
}
