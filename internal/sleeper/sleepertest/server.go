// Package sleepertest runs a fake Sleeper API for tests, so no test ever
// touches the real one.
package sleepertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Routes maps a request path (without query) to the value served as JSON.
// A nil value is served as JSON null, which is how Sleeper answers unknown
// users and leagues. Unlisted paths get a 404.
type Routes map[string]any

// NewServer serves routes and returns its base URL for sleeper.New.
func NewServer(t *testing.T, routes Routes) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Errorf("sleepertest: encoding %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
