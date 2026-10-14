// Package orders holds the shop's rules.
package orders

import "example.com/shop/internal/store/orderstore"

// Service lists orders.
type Service struct{ store *orderstore.Store }

// New returns a service over a store.
func New(s *orderstore.Store) *Service { return &Service{store: s} }

// All lists every order.
func (s *Service) All() []string { return s.store.List() }

// Helper is exported and referenced by nothing.
func Helper() {}
