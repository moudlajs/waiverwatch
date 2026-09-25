// Command waiverwatch is an MCP server for Sleeper fantasy football. It
// speaks MCP over stdio for local clients (Claude Code, Claude Desktop), or
// over HTTP at /mcp when PORT is set (Cloud Run).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/mcp"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
	"github.com/moudlajs/waiverwatch/internal/store"
)

// Hosted rate limit: plenty for one person's Claude, little for anyone else.
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
	return serveHTTP(ctx, ":"+port, mcp.HTTPHandler(server, requestsPerSecond, requestBurst))
}

// serveHTTP serves h on addr until ctx is cancelled, then drains in-flight
// requests (Cloud Run allows 10s after SIGTERM).
func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr, "version", version())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown) // not ctx: it is already cancelled
}

// version is the module version for `go install`ed binaries, else "dev".
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
