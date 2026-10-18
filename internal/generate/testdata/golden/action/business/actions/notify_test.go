package actions

import (
	"context"
	"testing"
)

func TestNotify(t *testing.T) {
	if err := Notify(context.Background()); err != nil {
		t.Fatal(err)
	}
}
