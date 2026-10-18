//go:build govibe_dev

package routes

import (
	"context"
	"encoding/json"
	"net/http"

	"example.com/fixture/business/services/orders"
	"example.com/fixture/storage/orderstore"
	"example.com/stubfw"
)

func init() { DevTools = registerDevTools }

type ordersCountIn struct {
	Status string `json:"status,omitempty" jsonschema:"only orders in this status"`
}

type ordersCountOut struct {
	Count int `json:"count"`
}

// registerDevTools adds the orders tool and page to the dev panel of a dev build.
func registerDevTools(rt *stubfw.Runtime, dev *stubfw.Dev) error {
	svc := orders.New(orderstore.New(nil))
	dev.Tool(stubfw.DevToolFunc("fixture_orders_count", "Count the orders, in one status when given.",
		func(ctx context.Context, in ordersCountIn) (ordersCountOut, error) {
			list, err := svc.List(ctx)
			if err != nil {
				return ordersCountOut{}, err
			}
			return ordersCountOut{Count: len(list)}, nil
		}))
	dev.Tool(stubfw.DevTool{Name: "fixture_cache_flush", Description: "Flush the cache.", Run: func(context.Context, json.RawMessage) (any, error) { return "flushed", nil }})
	dev.Page("/orders", "Orders", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("orders"))
	}))
	return nil
}
