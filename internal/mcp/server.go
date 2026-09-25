// Package mcp exposes waiverwatch as MCP tools. It knows MCP, not football,
// and never does HTTP itself: every tool delegates to league.Service.
package mcp

import (
	"context"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
)

// NewServer returns an MCP server with every waiverwatch tool registered.
func NewServer(svc *league.Service, version string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "waiverwatch", Version: version}, &sdk.ServerOptions{
		Instructions: "Sleeper fantasy football data for one user across all their leagues, fetched live on every call. " +
			"Start with list_leagues to see the leagues, records and standings.",
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_leagues",
		Description: "List every Sleeper league the user is in this season, with league type, size, " +
			"the user's team name, record, points for/against and standing. Also returns the current NFL season and week.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, league.Overview, error) {
		ov, err := svc.Overview(ctx)
		return nil, ov, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_matchups",
		Description: "This week's game in every league, with live points: my lineup and my opponent's, starter by starter " +
			"(slot, name, position, NFL team, injury, points). Guillotine leagues have no opponent; they report my rank " +
			"among surviving teams and my margin over the lowest one.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in MatchupsInput) (*sdk.CallToolResult, league.Week, error) {
		w, err := svc.Matchups(ctx, in.Week)
		return nil, w, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "trending_players",
		Description: "The most-added players across all of Sleeper right now (the waiver-wire signal), each with the " +
			"leagues where I can still claim him and the leagues where he is already on my roster.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in TrendingInput) (*sdk.CallToolResult, league.TrendingReport, error) {
		hours, limit := in.LookbackHours, in.Limit
		if hours <= 0 {
			hours = 24
		}
		if limit <= 0 {
			limit = 25
		}
		r, err := svc.Trending(ctx, hours, min(limit, 100), strings.ToUpper(in.Position))
		return nil, r, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "waiver_targets",
		Description: "The best available free agents in each of my leagues (on no roster, on an NFL team, at a position " +
			"the league can start), ranked by this week's trending adds and then Sleeper's overall rank. Includes my " +
			"waiver priority or FAAB budget left in each league. Use it for \"who should I pick up?\"",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in WaiverInput) (*sdk.CallToolResult, league.WaiverReport, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 5
		}
		r, err := svc.WaiverTargets(ctx, normalisePosition(in.Position), in.League, min(limit, 25))
		return nil, r, err
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_roster",
		Description: "A team's full roster: starters with their lineup slot, bench, IR and taxi, each player with position, " +
			"NFL team and injury. Mine by default, or any owner by team or display name. Without a league it covers " +
			"every league (for an owner: every league they are in).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in RosterInput) (*sdk.CallToolResult, league.RosterReport, error) {
		r, err := svc.Rosters(ctx, in.League, in.Owner)
		return nil, r, err
	})

	return s
}

// RosterInput is get_roster's arguments.
type RosterInput struct {
	League string `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
	Owner  string `json:"owner,omitempty" jsonschema:"team name or owner display name; omit for my own team"`
}

// WaiverInput is waiver_targets' arguments.
type WaiverInput struct {
	Position string `json:"position,omitempty" jsonschema:"only this position: QB, RB, WR, TE, K or DEF; omit for all"`
	League   string `json:"league,omitempty" jsonschema:"only leagues whose name contains this (case-insensitive), or a league ID; omit for all"`
	Limit    int    `json:"limit,omitempty" jsonschema:"targets per league, 1-25; default 5"`
}

// normalisePosition accepts common spellings: "rb", "DST", "D/ST", "PK".
func normalisePosition(p string) string {
	p = strings.ToUpper(strings.TrimSpace(p))
	switch p {
	case "DST", "D/ST", "D":
		return "DEF"
	case "PK":
		return "K"
	}
	return p
}

// TrendingInput is trending_players' arguments.
type TrendingInput struct {
	LookbackHours int    `json:"lookback_hours,omitempty" jsonschema:"how far back to count adds; default 24"`
	Limit         int    `json:"limit,omitempty" jsonschema:"players to return, 1-100; default 25"`
	Position      string `json:"position,omitempty" jsonschema:"only this position: QB, RB, WR, TE, K or DEF"`
}

// MatchupsInput is get_matchups' arguments.
type MatchupsInput struct {
	Week int `json:"week,omitempty" jsonschema:"NFL week (1-18); omit for the current week"`
}

func ptr[T any](v T) *T { return &v }
