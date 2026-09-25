// Command waiverwatch lists a Sleeper user's NFL leagues for the current season.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

func main() {
	user := flag.String("user", os.Getenv("WAIVERWATCH_USER"), "Sleeper username (default $WAIVERWATCH_USER)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, sleeper.New(sleeper.DefaultBaseURL), *user); err != nil {
		fmt.Fprintln(os.Stderr, "waiverwatch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c *sleeper.Client, username string) error {
	if username == "" {
		return errors.New("no Sleeper user: pass -user or set WAIVERWATCH_USER")
	}
	state, err := c.State(ctx)
	if err != nil {
		return err
	}
	u, err := c.User(ctx, username)
	if err != nil {
		return err
	}
	leagues, err := c.Leagues(ctx, u.UserID, state.Season)
	if err != nil {
		return err
	}

	fmt.Printf("%s - %s season, week %d\n\n", u.DisplayName, state.Season, state.Week)
	for _, l := range leagues {
		fmt.Printf("  %-40s %-10s %2d teams  %s\n", l.Name, l.Kind(), l.TotalRosters, l.Status)
	}
	return nil
}
