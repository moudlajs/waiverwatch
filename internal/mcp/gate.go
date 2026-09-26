package mcp

import (
	"context"
	"fmt"
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

	mu    sync.Mutex
	users map[string]*userLimit
}

type userLimit struct {
	lim  *rate.Limiter
	seen time.Time
}

func newGate(perMinute, burst int) *gate {
	return &gate{perMinute: perMinute, burst: burst, users: make(map[string]*userLimit)}
}

// limited wraps a tool so that the gate runs first.
func limited[In, Out any](g *gate, h func(ctx context.Context, in In) (Out, error)) sdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		ctx, err := g.enter(ctx, req)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		out, err := h(ctx, in)
		return nil, out, err
	}
}

func (g *gate) enter(ctx context.Context, req *sdk.CallToolRequest) (context.Context, error) {
	if req == nil || req.Extra == nil {
		return ctx, nil
	}
	id, ok := auth.UserFrom(req.Extra.TokenInfo)
	if !ok {
		return ctx, nil
	}
	if !g.allow(id.UserID, time.Now()) {
		return ctx, fmt.Errorf("slow down: waiverwatch allows %d requests a minute per user; try again in a minute", g.perMinute)
	}
	return league.WithUser(ctx, id.Username), nil
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
