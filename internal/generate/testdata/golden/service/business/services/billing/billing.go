// Package billing is the billing service: business logic that controllers and
// commands call. It imports nothing from the framework, so it tests without it.
package billing

import "context"

// Service holds the billing logic. Hand it the stores it needs through New.
type Service struct{}

// New returns a Service.
func New() *Service { return &Service{} }

// Ping stands in for the service's methods: replace it.
func (s *Service) Ping(ctx context.Context) error { return nil }
