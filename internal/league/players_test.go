package league

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/store"
)

var mahomes = sleeper.Player{PlayerID: "4046", FullName: "Patrick Mahomes", Position: "QB", Team: "KC"}

// fakeFetch counts downloads and returns a one-player dictionary, or err.
type fakeFetch struct {
	calls atomic.Int32
	err   error
}

func (f *fakeFetch) fetch(context.Context) (map[string]sleeper.Player, error) {
	f.calls.Add(1)
	time.Sleep(10 * time.Millisecond) // let concurrent callers pile up
	if f.err != nil {
		return nil, f.err
	}
	return map[string]sleeper.Player{"4046": mahomes}, nil
}

func newDirectory(t *testing.T, f *fakeFetch, now time.Time) (*Directory, *store.Memory, *time.Time) {
	t.Helper()
	s := store.NewMemory()
	d := NewDirectory(s, f.fetch)
	clock := now
	d.now = func() time.Time { return clock }
	return d, s, &clock
}

func TestDirectoryRefresh(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	tests := []struct {
		name      string
		age       time.Duration // how old the stored copy is; 0 = none stored
		fetchErr  error
		wantCalls int32
		wantErr   bool
	}{
		{name: "first use fetches", wantCalls: 1},
		{name: "fresh copy is reused", age: time.Hour, wantCalls: 0},
		{name: "just under a day is reused", age: MaxPlayersAge - time.Minute, wantCalls: 0},
		{name: "a day old is refreshed", age: MaxPlayersAge, wantCalls: 1},
		{name: "failed refresh falls back to stale copy", age: 48 * time.Hour, fetchErr: errors.New("sleeper down"), wantCalls: 1},
		{name: "failed first fetch is an error", fetchErr: errors.New("sleeper down"), wantCalls: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeFetch{err: tt.fetchErr}
			d, s, _ := newDirectory(t, f, start)
			if tt.age > 0 {
				_ = s.SavePlayers(ctx, store.Players{
					ByID:      map[string]sleeper.Player{"4046": mahomes},
					FetchedAt: start.Add(-tt.age),
				})
			}

			ps, err := d.Players(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := f.calls.Load(); got != tt.wantCalls {
				t.Errorf("fetches = %d, want %d", got, tt.wantCalls)
			}
			if !tt.wantErr && ps["4046"].FullName != "Patrick Mahomes" {
				t.Errorf("got %+v", ps["4046"])
			}
		})
	}
}

func TestDirectoryRefreshUpdatesTimestamp(t *testing.T) {
	f := &fakeFetch{}
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	d, _, clock := newDirectory(t, f, start)
	ctx := context.Background()

	for _, step := range []time.Duration{0, 23 * time.Hour, 2 * time.Hour, time.Hour} {
		*clock = clock.Add(step)
		if _, err := d.Players(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Fetched at 0h and again at 25h; 23h and 26h reuse.
	if got := f.calls.Load(); got != 2 {
		t.Errorf("fetches = %d, want 2", got)
	}
}

func TestDirectoryConcurrentFirstUse(t *testing.T) {
	f := &fakeFetch{}
	d, _, _ := newDirectory(t, f, time.Now())

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := d.Players(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetches = %d, want 1", got)
	}
}

func TestLookup(t *testing.T) {
	ps := map[string]sleeper.Player{"4046": mahomes}
	tests := []struct {
		id, want string
	}{
		{"4046", "Patrick Mahomes"},
		{"99999", "99999"},
	}
	for _, tt := range tests {
		if got := Lookup(ps, tt.id).Name(); got != tt.want {
			t.Errorf("Lookup(%q).Name() = %q, want %q", tt.id, got, tt.want)
		}
	}
}
