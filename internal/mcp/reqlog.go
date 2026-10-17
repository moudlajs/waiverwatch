package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// logRequests logs each /mcp request at start and end to find calls held until the timeout (#129).
// It sits inside sign-in, and never logs bodies, parameters, the Authorization header or usernames.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		attrs := []any{
			"http", r.Method,
			"accept", r.Header.Get("Accept"),
			"protocol", r.Header.Get("MCP-Protocol-Version"),
			"session", r.Header.Get("Mcp-Session-Id") != "",
			"last_event", r.Header.Get("Last-Event-ID") != "",
		}
		// Logged before the body is read, so a client that never finishes sending still shows up.
		slog.Info("mcp request start", attrs...)
		methods := "-"
		if r.Method == http.MethodPost && r.Body != nil {
			// Peek at most 1 MiB, then hand on the original stream: what was read, then the rest.
			peek, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			methods = "read_error"
			if err == nil {
				methods = rpcMethods(peek)
			}
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(peek), r.Body), r.Body}
		}
		bodyMS := time.Since(start).Milliseconds()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("mcp request end", append(attrs, "rpc", methods, "body_ms", bodyMS, "status", sw.status, "streamed", sw.flushed,
			"canceled", r.Context().Err() != nil, "ms", time.Since(start).Milliseconds())...)
	})
}

// rpcMethods names the JSON-RPC messages in a body, e.g. "tools/list", or "response" for a client's reply.
func rpcMethods(body []byte) string {
	type msg struct {
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	var batch []msg
	if err := json.Unmarshal(body, &batch); err != nil {
		var one msg
		if err := json.Unmarshal(body, &one); err != nil {
			return "unparsable"
		}
		batch = []msg{one}
	}
	names := make([]string, 0, len(batch))
	for _, m := range batch {
		switch {
		case m.Method != "":
			names = append(names, m.Method)
		case m.Result != nil || m.Error != nil:
			names = append(names, "response")
		default:
			names = append(names, "?")
		}
	}
	return strings.Join(names, ",")
}

// statusWriter records the status and whether the handler flushed (streamed).
type statusWriter struct {
	http.ResponseWriter
	status  int
	flushed bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	w.flushed = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
