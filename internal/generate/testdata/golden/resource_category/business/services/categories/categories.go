// Package categories holds the category queries and rules that controllers and
// commands call. It owns the storage call: nothing above it touches the store.
package categories

import (
	"context"

	"github.com/acme/shop/storage/categorystore"
)

// Reader is what the service needs from the category store.
type Reader interface {
	List(ctx context.Context) ([]categorystore.Category, error)
	Get(ctx context.Context, id int64) (categorystore.Category, error)
}

// Service answers category questions on top of a Reader.
type Service struct{ store Reader }

// New returns a Service over store.
func New(store Reader) *Service { return &Service{store: store} }

// List returns every category, newest first.
func (s *Service) List(ctx context.Context) ([]categorystore.Category, error) {
	return s.store.List(ctx)
}

// Get returns the category with the id; sql.ErrNoRows when there is none.
func (s *Service) Get(ctx context.Context, id int64) (categorystore.Category, error) {
	return s.store.Get(ctx, id)
}
