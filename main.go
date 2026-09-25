// Command waiverwatch is an MCP server for Sleeper fantasy football. It
// speaks MCP over stdio for local clients (Claude Code, Claude Desktop), or
// over HTTP at /mcp when PORT is set (Cloud Run). Over HTTP it requires
// OAuth sign-in, configured by WAIVERWATCH_BASE_URL, WAIVERWATCH_PASSPHRASE
// and WAIVERWATCH_SIGNING_KEY; WAIVERWATCH_NO_AUTH=1 turns it off for
// local testing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/auth"
	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/mcp"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/store"
)

// Hosted rate limit per instance: plenty for one person's Claude, little for
// anyone else.
const (
	requestsPerSecond = 5
	requestBurst      = 20
)

func main() {
	user := flag.String("user", os.Getenv("WAIVERWATCH_USER"), "Sleeper username (default $WAIVERWATCH_USER)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// On stdio, stdout carries the MCP protocol; diagnostics go to stderr.
	if err := run(ctx, *user, os.Getenv("PORT")); err != nil {
		fmt.Fprintln(os.Stderr, "waiverwatch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, username, port string) error {
	if username == "" {
		return errors.New("no Sleeper user: pass -user or set WAIVERWATCH_USER")
	}
	api := sleeper.New(sleeper.DefaultBaseURL)
	players := league.NewDirectory(store.NewMemory(), api.Players)
	server := mcp.NewServer(league.NewService(api, players, username), version())

	if port == "" {
		return server.Run(ctx, &sdk.StdioTransport{})
	}
	signIn, err := signInServer()
	if err != nil {
		return fmt.Errorf("sign-in: %w", err)
	}
	return mcp.Serve(ctx, ":"+port, mcp.HTTPHandler(server, version(), signIn, requestsPerSecond, requestBurst))
}

// signInServer builds the OAuth server from the environment. It returns nil
// only when WAIVERWATCH_NO_AUTH=1, so a missing secret stops the server
// instead of silently leaving it open.
func signInServer() (*auth.Server, error) {
	if os.Getenv("WAIVERWATCH_NO_AUTH") == "1" {
		slog.Warn("WAIVERWATCH_NO_AUTH=1: anyone who can reach this server can use it")
		return nil, nil
	}
	return auth.New(auth.Config{
		BaseURL:    os.Getenv("WAIVERWATCH_BASE_URL"),
		Passphrase: os.Getenv("WAIVERWATCH_PASSPHRASE"),
		SigningKey: []byte(os.Getenv("WAIVERWATCH_SIGNING_KEY")),
	})
}

// buildVersion is set by the container build (-ldflags -X).
var buildVersion string

// version is the release the binary was built from: stamped in by the
// container build, the module version for `go install`, else "dev".
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
