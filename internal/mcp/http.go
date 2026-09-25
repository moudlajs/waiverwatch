package mcp

import (
	"log/slog"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// HTTPHandler serves s over stateless Streamable HTTP at /mcp, and /healthz
// for health checks. Stateless because hosted instances come and go; there
// are no sessions to keep. /mcp sits behind one global rate limit: the
// server has no login yet (see #34), so the limit caps what a stranger who
// finds the URL can cost.
func HTTPHandler(s *sdk.Server, limit rate.Limit, burst int) http.Handler {
	mcpHandler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: slog.Default()},
	)
	limiter := rate.NewLimiter(limit, burst)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
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
