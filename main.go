// Command waiverwatch is an MCP server for Sleeper fantasy football. It
// speaks MCP over stdio for local clients (Claude Code, Claude Desktop), or
// over HTTP at /mcp when PORT is set (Cloud Run). Over stdio it answers for
// WAIVERWATCH_USER. Over HTTP anyone signs in with their Sleeper username
// (OAuth; WAIVERWATCH_BASE_URL, WAIVERWATCH_SIGNING_KEY, optional
// WAIVERWATCH_ALLOWED_USERS) and each request answers for its signed-in
// user; WAIVERWATCH_NO_AUTH=1 turns sign-in off for local testing, with
// WAIVERWATCH_USER as the only user.
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
	"strings"
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
	api := sleeper.New(sleeper.DefaultBaseURL)
	players := league.NewDirectory(store.NewMemory(), api.Players)
	// Hosted: the signing key also keys the anonymous usage IDs. Local: none.
	usageKey := []byte(os.Getenv("WAIVERWATCH_SIGNING_KEY"))
	server := mcp.NewServer(league.NewService(api, players, username), version(), usageKey)

	if port == "" {
		if username == "" {
			return errors.New("no Sleeper user: pass -user or set WAIVERWATCH_USER")
		}
		return server.Run(ctx, &sdk.StdioTransport{})
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{ReplaceAttr: cloudLogging})))
	signIn, err := signInServer(api, username)
	if err != nil {
		return fmt.Errorf("sign-in: %w", err)
	}
	return mcp.Serve(ctx, ":"+port, mcp.HTTPHandler(server, version(), signIn, requestsPerSecond, requestBurst))
}

// signInServer builds the OAuth server from the environment. It returns nil
// only when WAIVERWATCH_NO_AUTH=1, so a missing secret stops the server
// instead of silently leaving it open.
func signInServer(api *sleeper.Client, username string) (*auth.Server, error) {
	if os.Getenv("WAIVERWATCH_NO_AUTH") == "1" {
		if username == "" {
			return nil, errors.New("WAIVERWATCH_NO_AUTH=1 needs WAIVERWATCH_USER")
		}
		slog.Warn("WAIVERWATCH_NO_AUTH=1: anyone who can reach this server can use it")
		return nil, nil
	}
	var allowed []string
	if list := os.Getenv("WAIVERWATCH_ALLOWED_USERS"); list != "" {
		allowed = strings.Split(list, ",")
	}
	return auth.New(auth.Config{
		BaseURL:    os.Getenv("WAIVERWATCH_BASE_URL"),
		SigningKey: []byte(os.Getenv("WAIVERWATCH_SIGNING_KEY")),
		Lookup:     sleeperLookup(api),
		Allowed:    allowed,
	})
}

// cloudLogging names the level and message fields the way Cloud Logging
// reads them from JSON logs, so the console shows severities.
func cloudLogging(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if a.Value.String() == slog.LevelWarn.String() {
			a.Value = slog.StringValue("WARNING") // Cloud Logging's name for it
		}
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

// sleeperLookup resolves sign-in usernames against Sleeper.
func sleeperLookup(api *sleeper.Client) auth.Lookup {
	return func(ctx context.Context, name string) (auth.Identity, error) {
		u, err := api.User(ctx, name)
		if errors.Is(err, sleeper.ErrNotFound) {
			return auth.Identity{}, auth.ErrNoSuchUser
		}
		if err != nil {
			return auth.Identity{}, err
		}
		if u.Username == "" {
			u.Username = strings.ToLower(name)
		}
		return auth.Identity{UserID: u.UserID, Username: u.Username}, nil
	}
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
