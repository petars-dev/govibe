package categorystore

import (
	"context"
	"errors"
	"testing"
)

// With no database configured the store answers ErrNoDatabase instead of panicking:
// the app boots and serves without a DSN, and only a query says what is missing.
func TestQueriesWithoutADatabaseAreRefused(t *testing.T) {
	s := New(nil)
	if _, err := s.List(context.Background()); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("List = %v, want ErrNoDatabase", err)
	}
	if _, err := s.Get(context.Background(), 1); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("Get = %v, want ErrNoDatabase", err)
	}
	if _, err := s.Create(context.Background()); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("Create = %v, want ErrNoDatabase", err)
	}
}
