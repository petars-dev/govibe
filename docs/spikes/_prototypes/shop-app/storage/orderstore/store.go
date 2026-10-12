package orderstore

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type Store struct{ db *sqlx.DB }

func New(db *sqlx.DB) *Store { return &Store{db: db} }

func (s *Store) List(ctx context.Context) ([]string, error) {
	if s.db == nil {
		return nil, nil
	}
	var out []string
	err := s.db.SelectContext(ctx, &out, "SELECT name FROM orders")
	return out, err
}
