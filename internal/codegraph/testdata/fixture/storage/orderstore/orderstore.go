// Package orderstore reads and writes the orders table.
package orderstore

import (
	"context"
	"database/sql"
)

// Order is one row of the orders table.
type Order struct {
	ID     int64 `db:"id" json:"id"`
	UserID int64 `db:"user_id" json:"user_id"`
	Total  int64 `db:"total" json:"total"`
}

const (
	listSQL   = "SELECT id, user_id, total FROM orders ORDER BY id DESC"
	getSQL    = "SELECT id, user_id, total FROM orders WHERE id = ?"
	insertSQL = "INSERT INTO orders (user_id, total) VALUES (?, ?)"
)

// Store is the orders data-access object.
type Store struct {
	db *sql.DB
}

// New returns a Store over db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// List returns every order, newest first.
func (s *Store) List(ctx context.Context) ([]Order, error) {
	rows, err := s.db.QueryContext(ctx, listSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.UserID, &o.Total); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Get returns the order with the given id.
func (s *Store) Get(ctx context.Context, id string) (Order, error) {
	var o Order
	err := s.db.QueryRowContext(ctx, getSQL, id).Scan(&o.ID, &o.UserID, &o.Total)
	return o, err
}

// Create inserts a new order.
func (s *Store) Create(ctx context.Context, o Order) error {
	_, err := s.db.ExecContext(ctx, insertSQL, o.UserID, o.Total)
	return err
}
