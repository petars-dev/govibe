// Package orders holds the order queries that handlers are allowed to call.
package orders

import (
	"context"

	"example.com/fixture/storage/orderstore"
)

// Lister is the read side of the order store.
type Lister interface {
	List(ctx context.Context) ([]orderstore.Order, error)
}

// Service answers order questions on top of a Lister.
type Service struct {
	store Lister
}

// New returns a Service backed by store.
func New(store Lister) *Service {
	return &Service{store: store}
}

// List returns all orders, newest first.
func (s *Service) List(ctx context.Context) ([]orderstore.Order, error) {
	return s.store.List(ctx)
}

func (s Service) hasStore() bool {
	return s.store != nil
}
