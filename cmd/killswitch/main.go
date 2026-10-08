// Command killswitch is a Cloud Run service that unlinks billing when the budget is spent.
// Env: KILLSWITCH_PROJECT (required), KILLSWITCH_DRY_RUN=1, PORT.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moudlajs/waiverwatch/internal/killswitch"
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
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           killswitch.New(project, os.Getenv("KILLSWITCH_DRY_RUN") == "1"),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown) // not ctx: it is already cancelled
}
