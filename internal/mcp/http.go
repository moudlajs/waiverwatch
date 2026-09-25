package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/moudlajs/waiverwatch/internal/auth"
)

// HTTPHandler serves s over stateless Streamable HTTP at /mcp, and /health
// (reporting version) for health checks; not /healthz, which Cloud Run
// reserves. Stateless because hosted instances come and go. With a non-nil
// signIn, /mcp requires its OAuth access token and its endpoints are served
// too. /mcp also sits behind a rate limit per instance.
func HTTPHandler(s *sdk.Server, version string, signIn *auth.Server, limit rate.Limit, burst int) http.Handler {
	var mcpHandler http.Handler = sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: slog.Default()},
	)
	mux := http.NewServeMux()
	if signIn != nil {
		signIn.Routes(mux)
		mcpHandler = signIn.Protect(mcpHandler)
	}
	limiter := rate.NewLimiter(limit, burst)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %s\n", version)
	})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	return mux
}

// Serve serves h on addr until ctx is cancelled, then drains in-flight
// requests (Cloud Run allows 10s after SIGTERM).
func Serve(ctx context.Context, addr string, h http.Handler) error {
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
	slog.Info("listening", "addr", addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown) // not ctx: it is already cancelled
}
