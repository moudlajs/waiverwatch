package mcp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/moudlajs/waiverwatch/internal/auth"
	"github.com/moudlajs/waiverwatch/internal/league"
)

// gate runs before every tool call: it picks the Sleeper user the call
// answers for (from the verified access token) and enforces that user's
// rate limit. Without a token (stdio) the Service's default user applies
// and nothing is limited.
type gate struct {
	perMinute int
	burst     int
	usageKey  []byte // keys the anonymous usage IDs; nil: none logged

	mu    sync.Mutex
	users map[string]*userLimit
}

type userLimit struct {
	lim  *rate.Limiter
	seen time.Time
}

func newGate(perMinute, burst int, usageKey []byte) *gate {
	return &gate{perMinute: perMinute, burst: burst, usageKey: usageKey, users: make(map[string]*userLimit)}
}

// limited wraps a tool so that the gate runs first, and logs one anonymous
// usage line per call (tool, outcome, duration, daily anonymous user ID).
func limited[In, Out any](g *gate, h func(ctx context.Context, in In) (Out, error)) sdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		start := time.Now()
		ctx, anon, err := g.enter(ctx, req, start)
		var out Out
		if err == nil {
			out, err = h(ctx, in)
		}
		tool := ""
		if req != nil && req.Params != nil {
			tool = req.Params.Name
		}
		slog.InfoContext(ctx, "tool call", "tool", tool, "ok", err == nil,
			"ms", time.Since(start).Milliseconds(), "user", anon)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	}
}

// enter picks the call's user and applies their limit. anon is the user's
// anonymous usage ID for today ("" locally).
func (g *gate) enter(ctx context.Context, req *sdk.CallToolRequest, now time.Time) (_ context.Context, anon string, _ error) {
	if req == nil || req.Extra == nil {
		return ctx, "", nil
	}
	id, ok := auth.UserFrom(req.Extra.TokenInfo)
	if !ok {
		return ctx, "", nil
	}
	anon = g.anonID(id.UserID, now)
	if !g.allow(id.UserID, now) {
		return ctx, anon, fmt.Errorf("slow down: waiverwatch allows %d requests a minute per user; try again in a minute", g.perMinute)
	}
	return league.WithUser(ctx, id.Username), anon, nil
}

// anonID is a user's anonymous usage ID for the UTC day of now: a keyed
// hash, so it can't be turned back into a Sleeper user without the key, and
// it changes every day, so days can't be linked. It exists only to count
// distinct users per day.
func (g *gate) anonID(userID string, now time.Time) string {
	if len(g.usageKey) == 0 || userID == "" {
		return ""
	}
	m := hmac.New(sha256.New, g.usageKey)
	m.Write([]byte("usage|" + now.UTC().Format(time.DateOnly) + "|" + userID))
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// allow takes one call from user's allowance. Users idle for 10 minutes are
// forgotten once the map grows, so it can't grow without bound.
func (g *gate) allow(user string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, ok := g.users[user]
	if !ok {
		if len(g.users) >= 1000 {
			for k, v := range g.users {
				if now.Sub(v.seen) > 10*time.Minute {
					delete(g.users, k)
				}
			}
		}
		u = &userLimit{lim: rate.NewLimiter(rate.Limit(float64(g.perMinute)/60), g.burst)}
		g.users[user] = u
	}
	u.seen = now
	return u.lim.AllowN(now, 1)
}
