package dynastyprocess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Fixtures are made up in DynastyProcess's formats: their data is GPL-3, this repo is MIT.
func fixture(t *testing.T, calls, status *atomic.Int32) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if s := status.Load(); s != 0 {
			http.Error(w, "nope", int(s))
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", filepath.Base(r.URL.Path)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestValues(t *testing.T) {
	var calls, status atomic.Int32
	c := fixture(t, &calls, &status)
	ctx := context.Background()

	got, err := c.Values(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Player{SleeperID: "4984", Name: "Charlie Passer", Position: "QB", Team: "BUF", Value1QB: 6100, Value2QB: 10900}
	if len(got) != 3 || got["4984"] != want || got["7564"].Value1QB != 10208 {
		t.Errorf("got %+v", got) // No Sleeper Id (NA) and Unranked Guy (no value) are left out
	}
	if _, err := c.Values(ctx); err != nil || calls.Load() != 2 {
		t.Errorf("cached: err %v, %d calls, want 2 (one per file)", err, calls.Load())
	}

	// Expired and the refresh fails: the stale copy, then no retry for a minute.
	now := time.Now()
	c.now = func() time.Time { return now.Add(MaxAge + time.Minute) }
	status.Store(http.StatusBadGateway)
	before := calls.Load()
	if got, err := c.Values(ctx); err != nil || len(got) != 3 {
		t.Errorf("stale fallback: err %v, %d values", err, len(got))
	}
	if _, err := c.Values(ctx); err != nil || calls.Load() != before+1 {
		t.Errorf("within retryWait: err %v, %d new calls, want 1", err, calls.Load()-before)
	}
}

func TestValuesDown(t *testing.T) {
	var calls, status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	if _, err := fixture(t, &calls, &status).Values(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
