package categories

import (
	"context"
	"database/sql"
	"testing"

	"github.com/acme/shop/storage/categorystore"
)

// fakeReader stands in for the store, so the service tests without a database.
type fakeReader struct{ rows []categorystore.Category }

func (f fakeReader) List(ctx context.Context) ([]categorystore.Category, error) { return f.rows, nil }

func (f fakeReader) Get(ctx context.Context, id int64) (categorystore.Category, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return categorystore.Category{}, sql.ErrNoRows
}

func TestListAndGetReadTheStore(t *testing.T) {
	svc := New(fakeReader{rows: []categorystore.Category{{ID: 1}, {ID: 2}}})
	list, err := svc.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if got, err := svc.Get(context.Background(), 2); err != nil || got.ID != 2 {
		t.Fatalf("Get(2) = %v, %v", got, err)
	}
	if _, err := svc.Get(context.Background(), 3); err != sql.ErrNoRows {
		t.Fatalf("Get(3) = %v, want sql.ErrNoRows", err)
	}
}
