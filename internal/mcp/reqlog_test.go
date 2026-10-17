package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/time/rate"

	"github.com/moudlajs/waiverwatch/internal/auth"
	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

func captureLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRPCMethods(t *testing.T) {
	for _, tt := range []struct{ body, want string }{
		{`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "tools/list"},
		{`[{"method":"initialize","id":1},{"method":"notifications/initialized"}]`, "initialize,notifications/initialized"},
		{`{"jsonrpc":"2.0","id":7,"result":{}}`, "response"},
		{`{"jsonrpc":"2.0","id":7,"error":{"code":-1}}`, "response"},
		{`{"jsonrpc":"2.0","id":7}`, "?"},
		{`not json`, "unparsable"},
		{``, "unparsable"},
	} {
		if got := rpcMethods([]byte(tt.body)); got != tt.want {
			t.Errorf("rpcMethods(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}

func TestLogRequestsPassesBodyThrough(t *testing.T) {
	captureLogs(t)
	for _, size := range []int{0, 100, 1 << 20, 3<<20 + 7} {
		body := bytes.Repeat([]byte("x"), size)
		var got []byte
		h := logRequests(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, _ = io.ReadAll(r.Body)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))
		if !bytes.Equal(got, body) {
			t.Errorf("%d bytes in, %d out", size, len(got))
		}
	}
}

func TestLogRequestsRecordsTheCall(t *testing.T) {
	logs := captureLogs(t)
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.(http.Flusher).Flush()
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_roster","arguments":{"username":"moudlajs"}}}`))
	req.Header.Set("Authorization", "Bearer secret-token-value")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", "session-xyz")
	h.ServeHTTP(httptest.NewRecorder(), req)

	log := logs.String()
	for _, want := range []string{`"msg":"mcp request start"`, `"msg":"mcp request end"`, `"rpc":"tools/call"`,
		`"status":202`, `"body_ms":`, `"streamed":true`, `"session":true`, `"last_event":false`, `"canceled":false`} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %s: %s", want, log)
		}
	}
	for _, secret := range []string{"moudlajs", "secret-token-value", "session-xyz", "get_roster"} {
		if strings.Contains(log, secret) {
			t.Errorf("log contains %q: %s", secret, log)
		}
	}
}

// stalledBody records whether the start line was already logged when the handler first read it.
type stalledBody struct {
	logs     *strings.Builder
	sawStart bool
}

func (b *stalledBody) Read([]byte) (int, error) {
	b.sawStart = strings.Contains(b.logs.String(), "mcp request start")
	return 0, errors.New("client went away")
}

func TestLogRequestsStartsBeforeTheBodyArrives(t *testing.T) {
	logs := captureLogs(t)
	body := &stalledBody{logs: logs}
	h := logRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", body))

	if !body.sawStart {
		t.Error("the start line must be logged before the body is read")
	}
	if !strings.Contains(logs.String(), `"rpc":"read_error"`) {
		t.Errorf("want rpc read_error: %s", logs.String())
	}
}

func TestStrangersAreNotLogged(t *testing.T) {
	logs := captureLogs(t)
	signIn, err := auth.New(auth.Config{
		BaseURL: "https://waiverwatch.example", SigningKey: []byte("0123456789abcdef0123456789abcdef"),
		Lookup: func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, auth.ErrNoSuchUser },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := league.NewService(sleeper.New("http://unused.invalid"), nil, nil, "me")
	srv := httptest.NewServer(HTTPHandler(NewServer(svc, "test", nil), "test", signIn, rate.Inf, 1))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if strings.Contains(logs.String(), "mcp request") {
		t.Errorf("a stranger's request was logged: %s", logs.String())
	}
}
