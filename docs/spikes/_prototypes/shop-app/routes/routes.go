package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/shop/business/services/orders"
	"example.com/shop/commands"
	"example.com/shop/controllers"
	"example.com/shop/storage/orderstore"
	"petars-dev/govibe"
	"petars-dev/govibe/web"
)

// Register is the app's HTTP surface: every handler shape the spike needs.
func Register(rt *govibe.Runtime, r chi.Router) error {
	svc := orders.New(orderstore.New(rt.DB)) // nil DB is fine until a query runs
	ctl := &controllers.Orders{Svc: svc}
	ver := &web.Version{V: "app"}

	r.Get("/", controllers.Home) // app named func
	r.Route("/api", func(api chi.Router) {
		api.Get("/hello", controllers.HelloAPI)                                   // app named func using web.JSON
		api.With(controllers.AuditMW).Get("/orders", controllers.ListOrders(svc)) // app closure from factory + app mw
		api.Get("/orders/{id}", ctl.Show)                                         // app method value (-fm)
	})
	r.Get("/hello", func(w http.ResponseWriter, r *http.Request) { // app anonymous func
		_, _ = w.Write([]byte(rt.Config.String("shop.greeting")))
	})
	r.Handle("/plain", controllers.Plain{})                               // app http.Handler type
	r.Get("/fw/version", ver.Handle)                                      // FRAMEWORK method value mounted by the app
	r.Get("/fw/health", web.Health("from-app"))                           // FRAMEWORK closure mounted by the app
	r.Get("/fw/ping", web.Ping)                                           // FRAMEWORK named func mounted by the app
	r.Handle("/fw/static", http.StripPrefix("/fw", web.Static{Msg: "s"})) // stdlib wrapper around a framework handler
	r.Handle("/files/*", http.FileServer(http.Dir("assets")))             // stdlib handler
	rt.Log.Info("routes registered " + rt.Env())                          // call through a framework interface (logrus.FieldLogger)
	devRoutes(rt, r)
	return nil
}

// Console declares the app's commands (one, to make inspect --json non-empty).
func Console(rt *govibe.Runtime) []govibe.Command {
	svc := orders.New(orderstore.New(rt.DB))
	return []govibe.Command{{Use: "orders:export", Short: "Export orders", Run: commands.ExportOrders(svc)}}
}
