// Package commands holds the thin adapters behind the application's CLI commands: like a
// controller for HTTP, a command calls a service and returns, and touches no storage.
package commands

import (
	"context"

	"example.com/fixture/business/services/orders"
	"example.com/stubfw"
)

// ExportOrders writes every order out. It follows the layering rule: it only calls the
// orders service, which owns the storage call.
func ExportOrders(svc *orders.Service) stubfw.RunFunc {
	return func(ctx context.Context, rt *stubfw.Runtime, args []string) error {
		_, err := svc.List(ctx)
		return err
	}
}

// Digest sends the orders digest.
func Digest(svc *orders.Service) stubfw.RunFunc {
	return func(ctx context.Context, rt *stubfw.Runtime, args []string) error {
		_, err := svc.List(ctx)
		return err
	}
}

// WarmCache warms the cache. Nothing documents it and nothing schedules it, on purpose, so
// the indexer has an undocumented command to report.
func WarmCache(ctx context.Context, rt *stubfw.Runtime, args []string) error {
	return nil
}
