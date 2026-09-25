package sleeper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fixtures maps request paths to files in testdata/. The files are real
// Sleeper responses with names and IDs replaced.
var fixtures = map[string]string{
	"/user/testuser":             "user.json",
	"/user/100/leagues/nfl/2026": "leagues.json",
	"/league/L1/rosters":         "rosters.json",
	"/league/L1/users":           "users.json",
	"/league/L1/matchups/3":      "matchups.json",
	"/state/nfl":                 "state.json",
	"/players/nfl/trending/add":  "trending.json",
	"/user/ghost":                "", // Sleeper's answer for unknown users: 200 + null
	"/league/missing/rosters":    "", // same for unknown leagues
	"/league/broken/rosters":     "broken",
	"/league/down/rosters":       "500",
	"/league/slow/rosters":       "slow",
	"/user/100/leagues/nfl/1999": "[]",
	"/league/gone/matchups/1":    "404",
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/players/nfl/trending/add" {
			if got := r.URL.Query(); got.Get("lookback_hours") != "24" || got.Get("limit") != "3" {
				http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
				return
			}
		}
		name, ok := fixtures[r.URL.Path]
		switch {
		case !ok, name == "404":
			http.NotFound(w, r)
		case name == "":
			_, _ = w.Write([]byte("null"))
		case name == "[]":
			_, _ = w.Write([]byte("[]"))
		case name == "broken":
			_, _ = w.Write([]byte(`[{"roster_id": `))
		case name == "500":
			http.Error(w, "boom", http.StatusInternalServerError)
		case name == "slow":
			<-r.Context().Done()
		default:
			http.ServeFile(w, r, filepath.Join("testdata", name))
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestClientDecodes(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	t.Run("user", func(t *testing.T) {
		u, err := c.User(ctx, "testuser")
		if err != nil {
			t.Fatal(err)
		}
		if want := (User{UserID: "100", DisplayName: "TestUser"}); u != want {
			t.Errorf("got %+v, want %+v", u, want)
		}
	})

	t.Run("leagues", func(t *testing.T) {
		ls, err := c.Leagues(ctx, "100", "2026")
		if err != nil {
			t.Fatal(err)
		}
		if len(ls) != 2 {
			t.Fatalf("got %d leagues, want 2", len(ls))
		}
		l := ls[0]
		if l.LeagueID != "L1" || l.Name != "Dynasty League" || l.TotalRosters != 12 || l.Status != "in_season" {
			t.Errorf("unexpected league %+v", l)
		}
		if l.Kind() != "dynasty" || ls[1].Kind() != "guillotine" {
			t.Errorf("kinds = %q, %q; want dynasty, guillotine", l.Kind(), ls[1].Kind())
		}
		if len(l.RosterPositions) != 22 || l.RosterPositions[0] != "QB" {
			t.Errorf("roster positions = %v", l.RosterPositions)
		}
	})

	t.Run("rosters", func(t *testing.T) {
		rs, err := c.Rosters(ctx, "L1")
		if err != nil {
			t.Fatal(err)
		}
		r := rs[0]
		if r.RosterID != 1 || r.OwnerID != "100" || !reflect.DeepEqual(r.CoOwners, []string{"101"}) {
			t.Errorf("unexpected roster %+v", r)
		}
		if len(r.Players) != 22 || len(r.Starters) != 10 || len(r.Taxi) != 1 || len(r.Reserve) != 1 {
			t.Errorf("players/starters/taxi/reserve = %d/%d/%d/%d", len(r.Players), len(r.Starters), len(r.Taxi), len(r.Reserve))
		}
		s := r.Settings
		if s.Wins != 1 || s.Losses != 1 || s.PointsFor() != 310.04 || s.PointsAgainst() != 242.96 {
			t.Errorf("record %d-%d, %.2f/%.2f", s.Wins, s.Losses, s.PointsFor(), s.PointsAgainst())
		}
		if rs[1].CoOwners != nil {
			t.Errorf("null co_owners should decode to nil, got %v", rs[1].CoOwners)
		}
	})

	t.Run("league users", func(t *testing.T) {
		us, err := c.LeagueUsers(ctx, "L1")
		if err != nil {
			t.Fatal(err)
		}
		if us[0].DisplayName != "TestUser" || us[0].Metadata.TeamName != "Waiver Wizards" || us[1].Metadata.TeamName != "" {
			t.Errorf("unexpected users %+v", us)
		}
	})

	t.Run("matchups", func(t *testing.T) {
		ms, err := c.Matchups(ctx, "L1", 3)
		if err != nil {
			t.Fatal(err)
		}
		m := ms[0]
		if m.RosterID != 1 || m.MatchupID != 2 || m.Points != 29.2 || len(m.StartersPoints) != len(m.Starters) {
			t.Errorf("unexpected matchup %+v", m)
		}
		if len(m.PlayersPoints) == 0 {
			t.Error("players_points not decoded")
		}
	})

	t.Run("state", func(t *testing.T) {
		s, err := c.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := (State{Season: "2026", SeasonType: "regular", Week: 3}); s != want {
			t.Errorf("got %+v, want %+v", s, want)
		}
	})

	t.Run("trending adds", func(t *testing.T) {
		ts, err := c.TrendingAdds(ctx, 24, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(ts) != 3 || ts[0].PlayerID != "11435" || ts[0].Count != 398115 {
			t.Errorf("unexpected trending %+v", ts)
		}
	})

	t.Run("empty list is not an error", func(t *testing.T) {
		ls, err := c.Leagues(ctx, "100", "1999")
		if err != nil || len(ls) != 0 {
			t.Errorf("got %v, %v; want empty, nil", ls, err)
		}
	})
}

func TestClientErrors(t *testing.T) {
	c := newTestClient(t)

	tests := []struct {
		name     string
		call     func(ctx context.Context) error
		notFound bool
	}{
		{"unknown user is null", func(ctx context.Context) error { _, err := c.User(ctx, "ghost"); return err }, true},
		{"unknown league is null", func(ctx context.Context) error { _, err := c.Rosters(ctx, "missing"); return err }, true},
		{"404", func(ctx context.Context) error { _, err := c.Matchups(ctx, "gone", 1); return err }, true},
		{"500", func(ctx context.Context) error { _, err := c.Rosters(ctx, "down"); return err }, false},
		{"bad JSON", func(ctx context.Context) error { _, err := c.Rosters(ctx, "broken"); return err }, false},
		{"timeout", func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, err := c.Rosters(ctx, "slow")
			return err
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(context.Background())
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if got := errors.Is(err, ErrNotFound); got != tt.notFound {
				t.Errorf("errors.Is(ErrNotFound) = %v, want %v (err: %v)", got, tt.notFound, err)
			}
		})
	}

	t.Run("timeout wraps the context error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := c.Rosters(ctx, "slow")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestLeagueKind(t *testing.T) {
	for typ, want := range map[int]string{0: "redraft", 1: "keeper", 2: "dynasty", 3: "guillotine", 9: "unknown"} {
		if got := (League{Settings: LeagueSettings{Type: typ}}).Kind(); got != want {
			t.Errorf("type %d: got %q, want %q", typ, got, want)
		}
	}
}

func TestFixturesExist(t *testing.T) {
	for path, name := range fixtures {
		if filepath.Ext(name) != ".json" {
			continue
		}
		if _, err := os.Stat(filepath.Join("testdata", name)); err != nil {
			t.Errorf("fixture for %s: %v", path, err)
		}
	}
}
