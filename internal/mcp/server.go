// Package mcp exposes waiverwatch as MCP tools. It knows MCP, not football,
// and never does HTTP itself: every tool delegates to league.Service.
package mcp

import (
	"context"

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

	return s
}

func ptr[T any](v T) *T { return &v }
