// Command waiverwatch is an MCP server for Sleeper fantasy football. It
// speaks MCP over stdio; add it to Claude Code or Claude Desktop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/mcp"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

func main() {
	user := flag.String("user", os.Getenv("WAIVERWATCH_USER"), "Sleeper username (default $WAIVERWATCH_USER)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// stdout carries the MCP protocol; diagnostics go to stderr.
	if err := run(ctx, *user); err != nil {
		fmt.Fprintln(os.Stderr, "waiverwatch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("no Sleeper user: pass -user or set WAIVERWATCH_USER")
	}
	svc := league.NewService(sleeper.New(sleeper.DefaultBaseURL), username)
	return mcp.NewServer(svc, version()).Run(ctx, &sdk.StdioTransport{})
}

// version is the module version for `go install`ed binaries, else "dev".
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
