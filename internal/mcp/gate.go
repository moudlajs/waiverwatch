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

// gate picks each tool call's Sleeper user from the verified token and rate-limits per user (stdio: unlimited).
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

// limited runs the gate before a tool and logs one anonymous usage line per call.
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

// anonID is a keyed, daily-rotating hash: not reversible to a Sleeper user, and days can't be linked.
func (g *gate) anonID(userID string, now time.Time) string {
	if len(g.usageKey) == 0 || userID == "" {
		return ""
	}
	m := hmac.New(sha256.New, g.usageKey)
	m.Write([]byte("usage|" + now.UTC().Format(time.DateOnly) + "|" + userID))
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// allow evicts users idle 10+ minutes once the map is large, so it stays bounded.
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
