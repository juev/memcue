package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// useConfig points the handle at cfg for its next connection. The open already connected through the original
// configuration, so the connection it keeps is dropped: the next operation dials through cfg.
func useConfig(s *Store, cfg *pgx.ConnConfig) {
	s.remote.mu.Lock()
	defer s.remote.mu.Unlock()
	s.remote.config = cfg
	s.remote.drop(context.Background())
}
