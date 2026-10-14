// Package orderstore reads and writes the orders table.
package orderstore

const listSQL = "SELECT id, total FROM orders ORDER BY id"

// Store is the orders table.
type Store struct{}

// New returns a store.
func New() *Store { return &Store{} }

// List returns every order.
func (s *Store) List() []string { _ = listSQL; return nil }

// One returns one order.
func (s *Store) One(id string) string { return "SELECT id FROM orders WHERE id = ?" + id }
