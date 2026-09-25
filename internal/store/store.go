// Package store holds what waiverwatch keeps between requests: only the
// player dictionary. Live data (rosters, matchups, trending) is never stored.
package store

import (
	"context"
	"sync"
	"time"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// Players is a snapshot of the player dictionary.
type Players struct {
	ByID      map[string]sleeper.Player
	FetchedAt time.Time
}

// Store persists the player dictionary. Memory is the only implementation;
// Cloud Run's disk is ephemeral, so anything durable would be a service.
type Store interface {
	// Players returns the saved snapshot, or the zero value if there is none.
	Players(ctx context.Context) (Players, error)
	SavePlayers(ctx context.Context, p Players) error
}

// Memory keeps the dictionary in process memory. Safe for concurrent use.
type Memory struct {
	mu      sync.RWMutex
	players Players
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{} }

// Players implements Store.
func (m *Memory) Players(context.Context) (Players, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.players, nil
}

// SavePlayers implements Store.
func (m *Memory) SavePlayers(_ context.Context, p Players) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.players = p
	return nil
}
