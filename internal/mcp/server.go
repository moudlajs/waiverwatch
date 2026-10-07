// Package mcp exposes waiverwatch as MCP tools and serves them as a
// transport. It knows MCP, not football, and never calls Sleeper itself:
// every tool delegates to league.Service.
package mcp

import (
	"context"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
)

// Per signed-in user: plenty for a person talking to Claude, little for a
// script. Local (stdio) use is not limited.
const (
	toolCallsPerMinute = 30
	toolCallBurst      = 10
)

// NewServer returns an MCP server with every waiverwatch tool registered.
// usageKey keys the anonymous daily user IDs in the usage log (the hosted
// signing key); nil logs none.
func NewServer(svc *league.Service, version string, usageKey []byte) *sdk.Server {
	return newServer(svc, version, newGate(toolCallsPerMinute, toolCallBurst, usageKey))
}

func newServer(svc *league.Service, version string, g *gate) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "waiverwatch", Version: version}, &sdk.ServerOptions{
		Instructions: "Sleeper fantasy football data for the signed-in Sleeper user across all their leagues, fetched " +
			"live on every call. Start with list_leagues to see the leagues, records and standings.",
	})

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_leagues",
		Description: "List every Sleeper league the user is in this season, with league type, size, " +
			"the user's team name, record, points for/against and standing. Also returns the current NFL season and " +
			"week, and this server's version and tools.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, _ struct{}) (LeaguesOutput, error) {
		ov, err := svc.Overview(ctx)
		return LeaguesOutput{Overview: ov, Server: ServerInfo{Version: version, Tools: toolNames, Note: reconnectNote}}, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_matchups",
		Description: "This week's game in every league, with live points and Sleeper's projected points: my lineup and my " +
			"opponent's, starter by starter (slot, name, position, NFL team, injury, points, projected) and each side's " +
			"projected total. Projections are for the whole game, not what's left of it. Guillotine leagues have no opponent; they report my rank " +
			"among surviving teams and my margin over the lowest one.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in MatchupsInput) (league.Week, error) {
		w, err := svc.Matchups(ctx, in.Week)
		return w, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "trending_players",
		Description: "The most-added players across all of Sleeper right now (the waiver-wire signal), each with the " +
			"leagues where I can still claim him and the leagues where he is already on my roster.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in TrendingInput) (league.TrendingReport, error) {
		hours, limit := in.LookbackHours, in.Limit
		if hours <= 0 {
			hours = 24
		}
		if limit <= 0 {
			limit = 25
		}
		r, err := svc.Trending(ctx, hours, min(limit, 100), strings.ToUpper(in.Position))
		return r, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "waiver_targets",
		Description: "The best available free agents in each of my leagues (on no roster, on an NFL team, at a position " +
			"the league can start), ranked by trade value (rest-of-season worth in that league's format), then this " +
			"week's projected points, then trending adds. Includes my waiver priority or FAAB budget left, and in FAAB " +
			"leagues a suggested bid per player. Use it for \"who should I pick up?\" and \"how much should I bid?\"",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in WaiverInput) (league.WaiverReport, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 5
		}
		r, err := svc.WaiverTargets(ctx, normalisePosition(in.Position), in.League, min(limit, 25))
		return r, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_roster",
		Description: "A team's full roster: starters with their lineup slot, bench, IR and taxi, each player with position, " +
			"NFL team and injury. Mine by default, or any owner by team or display name. Without a league it covers " +
			"every league (for an owner: every league they are in).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in RosterInput) (league.RosterReport, error) {
		r, err := svc.Rosters(ctx, in.League, in.Owner)
		return r, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "injury_report",
		Description: "Every injured player on my rosters across all leagues (Out, IR, Doubtful, Questionable...), most " +
			"serious and most-started first, with the leagues where he is in my lineup. Where he is starting, it " +
			"suggests the best available replacement at his position in that league.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, _ struct{}) (league.InjuryReport, error) {
		r, err := svc.Injuries(ctx)
		return r, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "compare_rosters",
		Description: "My roster next to another team's, position by position (starters with lineup slot first, then " +
			"bench, then IR), with both records. By default the other team is this week's opponent in every league; " +
			"name an owner (team or display name) to compare against anyone, e.g. before a trade.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in RosterInput) (league.Comparisons, error) {
		c, err := svc.Compare(ctx, in.League, in.Owner)
		return c, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "draft_results",
		Description: "My draft picks in every league (round, overall pick, player, auction price, and whether he is still " +
			"on my roster). Dynasty leagues include earlier seasons, i.e. the startup draft and past rookie drafts. " +
			"Pass a player name to answer \"where did I draft X?\", including players I have since dropped or traded.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in DraftInput) (league.DraftReport, error) {
		r, err := svc.Drafts(ctx, in.League, in.Player)
		return r, err
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "position_depth",
		Description: "Am I thin anywhere? For each league: starting slots per position (flex slots filled from spare " +
			"players), healthy players, backups, questionable and unavailable players, and a status: ok, thin (no " +
			"backup) or short (can't fill the lineup). thin_spots lists every problem across leagues.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in DepthInput) (league.DepthReport, error) {
		return svc.Depth(ctx, in.League)
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "player_values",
		Description: "Trade values: what players are worth on the trade market right now (FantasyCalc, from real fantasy " +
			"trades; up to ~11000), fitted to each league's format: dynasty or redraft, superflex or 1QB, size, PPR. " +
			"Name players to see their value in each league and whose team they are on; omit players for a whole " +
			"roster's values and total (mine, or an owner's). Use it for \"is this trade fair?\": compare the sides' sums.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in ValuesInput) (league.ValueReport, error) {
		return svc.Values(ctx, in.League, in.Owner, in.Players)
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "evaluate_trade",
		Description: "Is this trade fair? Values what I give and what I get (players, and dynasty draft picks like " +
			"\"2027 1st\") with FantasyCalc values for that league's format: who it leans to, by how much, and my depth " +
			"before and after. Two good players are not worth one great one: each side's best piece counts in full and " +
			"lesser pieces are discounted. Picks are checked against who holds them (traded picks included) and next " +
			"year's are valued early/mid/late from their original team's standing; add a team to pick one (\"2027 1st " +
			"CHGO\") or a slot to override (\"2027 1st late\"). Ambiguous names come back with every candidate and " +
			"their teams. Without a league, it uses the one league where I have every player I give.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in TradeInput) (league.TradeReport, error) {
		return svc.EvaluateTrade(ctx, in.League, in.Give, in.Get)
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "trade_targets",
		Description: "Who should I trade for? Per league: my thin positions (no healthy backup, or can't fill the lineup), " +
			"my spare players (healthy depth beyond what my lineup needs, where I can afford to lose it), and players on " +
			"other teams at the thin positions that my spares can buy, each with the cheapest offer (one or two spares). " +
			"Thin means a position's own slots; flex depth isn't counted (see position_depth). Other teams' taxi players " +
			"count as targets. Name a position to look there even if it isn't thin. Check a deal with evaluate_trade " +
			"before proposing it.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in TargetsInput) (league.TargetReport, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 5
		}
		return svc.TradeTargets(ctx, in.League, normalisePosition(in.Position), min(limit, 20))
	}))

	sdk.AddTool(s, &sdk.Tool{
		Name: "lineup_check",
		Description: "Is my lineup right? For this week in every league: empty slots, starters who are out (Out, Doubtful, " +
			"IR...) or on bye, questionable starters, and the best lineup by Sleeper's projections (flex-aware) as " +
			"start/bench changes with the projected points gained. Projections are whole-game: players whose game " +
			"has started are locked in Sleeper, so check kickoff times before suggesting a swap.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, limited(g, func(ctx context.Context, in DepthInput) (league.LineupReport, error) {
		return svc.LineupCheck(ctx, in.League)
	}))

	return s
}

// TargetsInput is trade_targets' arguments.
type TargetsInput struct {
	League   string `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
	Position string `json:"position,omitempty" jsonschema:"look for this position (QB, RB, WR, TE) even if it isn't thin; omit for my thin positions"`
	Limit    int    `json:"limit,omitempty" jsonschema:"targets per league, 1-20; default 5"`
}

// toolNames are every tool NewServer registers; a test keeps it in step.
var toolNames = []string{
	"list_leagues", "get_matchups", "trending_players", "waiver_targets", "get_roster", "injury_report",
	"compare_rosters", "draft_results", "position_depth", "player_values", "evaluate_trade", "trade_targets",
	"lineup_check",
}

// Claude keeps a stored copy of a connector's tool list until the connector
// is reconnected, so a chat may lack tools a release added. Tool results are
// always live: this lets Claude notice.
const reconnectNote = "If any of these tools are missing from your tool list, waiverwatch was updated after this " +
	"connector's tools were loaded: tell the user to reconnect the waiverwatch connector (claude.ai Settings > " +
	"Connectors) and start a new chat. Don't improvise what a missing tool would return."

// LeaguesOutput is list_leagues' answer: the overview plus what this server
// offers.
type LeaguesOutput struct {
	league.Overview
	Server ServerInfo `json:"server"`
}

// ServerInfo describes the running waiverwatch.
type ServerInfo struct {
	Version string   `json:"version"`
	Tools   []string `json:"tools" jsonschema:"every tool this server has"`
	Note    string   `json:"note"`
}

// TradeInput is evaluate_trade's arguments.
type TradeInput struct {
	Give   []string `json:"give" jsonschema:"players I give (name or part of it, must be on my roster) and picks (2027 1st, 2028 2nd CHGO)"`
	Get    []string `json:"get" jsonschema:"players (or picks) I get"`
	League string   `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit to use the one league where I have everything I give"`
}

// ValuesInput is player_values' arguments.
type ValuesInput struct {
	Players []string `json:"players,omitempty" jsonschema:"player names (or parts, e.g. Gibbs); omit to value a whole roster"`
	League  string   `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
	Owner   string   `json:"owner,omitempty" jsonschema:"only without players: team name or owner display name whose roster to value; omit for mine"`
}

// DepthInput is position_depth's and lineup_check's arguments.
type DepthInput struct {
	League string `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
}

// DraftInput is draft_results' arguments.
type DraftInput struct {
	League string `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
	Player string `json:"player,omitempty" jsonschema:"only picks whose player name contains this, e.g. Walker; omit for all my picks"`
}

// RosterInput is get_roster's and compare_rosters' arguments.
type RosterInput struct {
	League string `json:"league,omitempty" jsonschema:"league name fragment (case-insensitive) or ID; omit for all leagues"`
	Owner  string `json:"owner,omitempty" jsonschema:"team name or owner display name; get_roster: omit for my own team, compare_rosters: omit for this week's opponent"`
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
