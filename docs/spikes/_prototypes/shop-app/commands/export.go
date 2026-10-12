package commands

import (
	"context"
	"fmt"

	"example.com/shop/business/services/orders"
	"petars-dev/govibe"
)

// ExportOrders is a thin command adapter over a service.
func ExportOrders(svc *orders.Service) govibe.RunFunc {
	return func(ctx context.Context, rt *govibe.Runtime, args []string) error {
		rows, err := svc.List(ctx)
		fmt.Println("exported", len(rows), rt.Config.String("shop.currency"))
		return err
	}
}
