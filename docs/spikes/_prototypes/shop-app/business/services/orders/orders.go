package orders

import "context"

type Store interface {
	List(ctx context.Context) ([]string, error)
}

type Service struct{ store Store }

func New(s Store) *Service { return &Service{store: s} }

func (s *Service) List(ctx context.Context) ([]string, error) { return s.store.List(ctx) }
