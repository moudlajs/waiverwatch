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
	g := newGate(60, 3, nil) // a burst of 3, then one a second
	ctx := context.Background()
	call := func(req *sdk.CallToolRequest) error {
		_, _, err := g.enter(ctx, req, time.Now())
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
	g := newGate(60, 1, nil)
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
	g := newGate(60, 1, nil)
	h := limited(g, func(context.Context, struct{}) (string, error) { return "ok", nil })
	if _, out, err := h(context.Background(), signedIn("1", "alice"), struct{}{}); err != nil || out != "ok" {
		t.Fatalf("first call: %q, %v", out, err)
	}
	if _, out, err := h(context.Background(), signedIn("1", "alice"), struct{}{}); err == nil || out != "" {
		t.Errorf("second call: %q, %v; want the limit error and no output", out, err)
	}
}

func TestAnonID(t *testing.T) {
	g := newGate(60, 1, []byte("usage key"))
	day := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

	a := g.anonID("1213", day)
	if len(a) != 16 || a != g.anonID("1213", day.Add(5*time.Hour)) {
		t.Errorf("same user, same UTC day should give one stable 16-char ID: %q", a)
	}
	if a == g.anonID("1213", day.Add(24*time.Hour)) {
		t.Error("the ID must change the next day")
	}
	if a == g.anonID("42", day) {
		t.Error("different users must get different IDs")
	}
	if a == newGate(60, 1, []byte("other key")).anonID("1213", day) {
		t.Error("the ID must depend on the key")
	}
	if strings.Contains(a, "1213") || newGate(60, 1, nil).anonID("1213", day) != "" {
		t.Error("no key: no ID; and the ID must not contain the user id")
	}
}
