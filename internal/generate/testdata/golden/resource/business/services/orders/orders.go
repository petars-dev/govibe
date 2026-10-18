// Package orders holds the order queries and rules that controllers and
// commands call. It owns the storage call: nothing above it touches the store.
package orders

import (
	"context"

	"github.com/acme/shop/storage/orderstore"
)

// Reader is what the service needs from the order store.
type Reader interface {
	List(ctx context.Context) ([]orderstore.Order, error)
	Get(ctx context.Context, id int64) (orderstore.Order, error)
}

// Service answers order questions on top of a Reader.
type Service struct{ store Reader }

// New returns a Service over store.
func New(store Reader) *Service { return &Service{store: store} }

// List returns every order, newest first.
func (s *Service) List(ctx context.Context) ([]orderstore.Order, error) {
	return s.store.List(ctx)
}

// Get returns the order with the id; sql.ErrNoRows when there is none.
func (s *Service) Get(ctx context.Context, id int64) (orderstore.Order, error) {
	return s.store.Get(ctx, id)
}
