// Package orderstore reads and writes the orders table. It imports nothing from the
// framework: it takes the database pool from its constructor.
package orderstore

import (
	"context"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// Order is one row of the orders table.
type Order struct {
	ID        int64     `db:"id" json:"id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// ErrNoDatabase is what every query returns while the app runs with no database
// configured (no DSN: the framework hands routes and commands a nil rt.DB).
var ErrNoDatabase = errors.New("orderstore: no database is configured (set MARIADB_DSN)")

// Store is the orders data-access object.
type Store struct{ db *sqlx.DB }

// New returns a Store over db. A nil db is fine until a query runs.
func New(db *sqlx.DB) *Store { return &Store{db: db} }

// List returns every order, newest first.
func (s *Store) List(ctx context.Context) ([]Order, error) {
	if s.db == nil {
		return nil, ErrNoDatabase
	}
	out := []Order{}
	err := s.db.SelectContext(ctx, &out, "SELECT id, created_at FROM orders ORDER BY id DESC")
	return out, err
}

// Get returns the order with the id; sql.ErrNoRows when there is none.
func (s *Store) Get(ctx context.Context, id int64) (Order, error) {
	var out Order
	if s.db == nil {
		return out, ErrNoDatabase
	}
	err := s.db.GetContext(ctx, &out, "SELECT id, created_at FROM orders WHERE id = ?", id)
	return out, err
}

// Create inserts a order and returns its id.
func (s *Store) Create(ctx context.Context) (int64, error) {
	if s.db == nil {
		return 0, ErrNoDatabase
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO orders () VALUES ()")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
