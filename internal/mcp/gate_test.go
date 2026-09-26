package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func signedIn(id, name string) *sdk.CallToolRequest {
	return &sdk.CallToolRequest{Extra: &sdk.RequestExtra{TokenInfo: &sdkauth.TokenInfo{
		UserID: id, Extra: map[string]any{"sleeper_username": name},
	}}}
}

func TestGateLimitsEachUserSeparately(t *testing.T) {
	g := newGate(60, 3) // a burst of 3, then one a second
	ctx := context.Background()
	call := func(req *sdk.CallToolRequest) error {
		_, err := g.enter(ctx, req)
		return err
	}

	for i := range 3 {
		if err := call(signedIn("1", "alice")); err != nil {
			t.Fatalf("alice call %d: %v", i+1, err)
		}
	}
	err := call(signedIn("1", "alice"))
	if err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("alice's 4th call: %v, want a slow-down error", err)
	}
	if err := call(signedIn("2", "bob")); err != nil {
		t.Errorf("bob is limited by alice's calls: %v", err)
	}
	for range 10 { // local (stdio) calls carry no token and are never limited
		if err := call(&sdk.CallToolRequest{}); err != nil {
			t.Fatalf("stdio call limited: %v", err)
		}
	}
}

func TestGateForgetsIdleUsers(t *testing.T) {
	g := newGate(60, 1)
	start := time.Now()
	for i := range 1000 {
		g.allow(string(rune('a'+i%26))+strings.Repeat("x", i/26), start)
	}
	g.allow("newcomer", start.Add(11*time.Minute))
	if len(g.users) > 2 {
		t.Errorf("%d users kept, want idle ones forgotten", len(g.users))
	}
}

func TestLimitedToolReturnsTheError(t *testing.T) {
	g := newGate(60, 1)
	h := limited(g, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if _, out, err := h(context.Background(), signedIn("1", "alice"), struct{}{}); err != nil || out != "ok" {
		t.Fatalf("first call: %q, %v", out, err)
	}
	if _, out, err := h(context.Background(), signedIn("1", "alice"), struct{}{}); err == nil || out != "" {
		t.Errorf("second call: %q, %v; want the limit error and no output", out, err)
	}
}
