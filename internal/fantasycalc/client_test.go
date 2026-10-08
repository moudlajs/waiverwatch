package fantasycalc

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

func TestString(t *testing.T) {
	for in, want := range map[Settings]string{
		{QBs: 1, Teams: 12, PPR: 1}:                  "redraft 1QB 12-team PPR",
		{Dynasty: true, QBs: 2, Teams: 10, PPR: 0.5}: "dynasty superflex 10-team half PPR",
		{QBs: 1, Teams: 14}:                          "redraft 1QB 14-team standard",
	} {
		if got := in.String(); got != want {
			t.Errorf("%+v: got %q, want %q", in, got, want)
		}
	}
}

func TestNormalise(t *testing.T) {
	tests := []struct {
		name string
		in   Settings
		want Settings
	}{
		{"common redraft", Settings{QBs: 1, Teams: 12, PPR: 1}, Settings{QBs: 1, Teams: 12, PPR: 1}},
		{"superflex counts as 2 QBs", Settings{Dynasty: true, QBs: 3, Teams: 10, PPR: 1}, Settings{Dynasty: true, QBs: 2, Teams: 10, PPR: 1}},
		{"no QB slots is still 1QB", Settings{QBs: 0, Teams: 12}, Settings{QBs: 1, Teams: 12}},
		{"odd sizes round up", Settings{QBs: 1, Teams: 11, PPR: 1}, Settings{QBs: 1, Teams: 12, PPR: 1}},
		{"big leagues get the largest market", Settings{QBs: 1, Teams: 32, PPR: 1}, Settings{QBs: 1, Teams: 14, PPR: 1}},
		{"small leagues the smallest", Settings{QBs: 1, Teams: 4}, Settings{QBs: 1, Teams: 8}},
		{"half PPR", Settings{QBs: 1, Teams: 12, PPR: 0.4}, Settings{QBs: 1, Teams: 12, PPR: 0.5}},
		{"PPR capped at 1", Settings{QBs: 1, Teams: 12, PPR: 1.5}, Settings{QBs: 1, Teams: 12, PPR: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Normalise(); got != tt.want {
				t.Errorf("Normalise(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func fixture(t *testing.T, calls *atomic.Int32, status *atomic.Int32) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if s := status.Load(); s != 0 {
			http.Error(w, "nope", int(s))
			return
		}
		name := map[string]string{
			"isDynasty=false&numQbs=1&numTeams=12&ppr=1":  "redraft.json",
			"isDynasty=true&numQbs=2&numTeams=14&ppr=0.5": "dynasty.json",
		}[r.URL.RawQuery]
		if r.URL.Path != "/values/current" || name == "" {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Error(err)
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

	got, err := c.Values(ctx, Settings{QBs: 1, Teams: 12, PPR: 1})
	if err != nil {
		t.Fatal(err)
	}
	gibbs := got["9221"]
	if len(got) != 3 || gibbs.Name != "Jahmyr Gibbs" || gibbs.Position != "RB" || gibbs.Team != "DET" ||
		gibbs.Value != 10735 || gibbs.OverallRank != 1 || gibbs.PositionRank != 1 || gibbs.Tier != 1 || gibbs.Trend30Day != 573 {
		t.Errorf("got %d values, Gibbs = %+v", len(got), gibbs)
	}

	// Dynasty, 2QB, 32 teams and 0.5 PPR asks for the 14-team superflex market.
	dyn, err := c.Values(ctx, Settings{Dynasty: true, QBs: 2, Teams: 32, PPR: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if pick := dyn["FP_2027_early_0"]; pick.Position != "PICK" || pick.Name != "2027 1st (Early)" || pick.Team != "" {
		t.Errorf("pick = %+v", pick)
	}

	// Cached: no new request.
	if _, err := c.Values(ctx, Settings{QBs: 1, Teams: 12, PPR: 1}); err != nil || calls.Load() != 2 {
		t.Errorf("err %v, %d calls, want 2", err, calls.Load())
	}
}

func TestValuesErrors(t *testing.T) {
	var calls, status atomic.Int32
	c := fixture(t, &calls, &status)
	ctx := context.Background()
	redraft := Settings{QBs: 1, Teams: 12, PPR: 1}

	status.Store(http.StatusBadGateway)
	if _, err := c.Values(ctx, redraft); !errors.Is(err, ErrUnavailable) {
		t.Errorf("down with nothing cached: err = %v, want ErrUnavailable", err)
	}

	status.Store(0)
	if _, err := c.Values(ctx, redraft); err != nil {
		t.Fatal(err)
	}

	// Expired and the refresh fails: the stale copy is still served.
	now := time.Now()
	c.now = func() time.Time { return now.Add(MaxAge + time.Minute) }
	status.Store(http.StatusServiceUnavailable)
	before := calls.Load()
	got, err := c.Values(ctx, redraft)
	if err != nil || got["9221"].Value != 10735 || calls.Load() != before+1 {
		t.Errorf("stale fallback: err %v, got %d values, %d new calls", err, len(got), calls.Load()-before)
	}

	// Right after a failed refresh the stale copy is served without asking.
	if _, err := c.Values(ctx, redraft); err != nil || calls.Load() != before+1 {
		t.Errorf("within retryWait: err %v, %d new calls, want 1", err, calls.Load()-before)
	}
	// After it, FantasyCalc is asked again.
	c.now = func() time.Time { return now.Add(MaxAge + time.Minute + retryWait + time.Second) }
	status.Store(0)
	if _, err := c.Values(ctx, redraft); err != nil || calls.Load() != before+2 {
		t.Errorf("after retryWait: err %v, %d new calls, want 2", err, calls.Load()-before)
	}
}
