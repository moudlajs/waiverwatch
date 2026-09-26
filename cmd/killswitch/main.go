// Command killswitch unlinks the project's billing account when its budget
// is spent. It runs as its own Cloud Run service, reachable only by the
// budget's Pub/Sub push subscription. See internal/killswitch.
//
// Environment: KILLSWITCH_PROJECT (required), KILLSWITCH_DRY_RUN=1 to only
// log what it would do, PORT.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/moudlajs/waiverwatch/internal/killswitch"
	"github.com/moudlajs/waiverwatch/internal/mcp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "killswitch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	project := os.Getenv("KILLSWITCH_PROJECT")
	if project == "" {
		return errors.New("KILLSWITCH_PROJECT is not set")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	s := killswitch.New(project, os.Getenv("KILLSWITCH_DRY_RUN") == "1")
	return mcp.Serve(ctx, ":"+port, s) // the generic HTTP lifecycle, not MCP
}
