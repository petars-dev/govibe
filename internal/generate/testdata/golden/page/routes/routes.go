package routes

import (
	"github.com/go-chi/chi/v5"

	"github.com/acme/shop/controllers"
	"github.com/petars-dev/govibe"
)

// Register is the app's HTTP surface, the Hello GoVibe demo. The router it receives
// already carries the standard middleware and the asset mounts; the dev tools are
// mounted by the framework, not here. Construct dependencies here as the app grows:
// registration must not open connections or read secrets, because tooling calls it
// with no database to list what exists (rt.DB is nil then).
func Register(rt *govibe.Runtime, r chi.Router) error {
	// UI routes
	r.Get("/", controllers.Home)

	// API routes
	r.Route("/api", func(api chi.Router) {
		api.Get("/hello", controllers.HelloAPI)
	})

	// htmx-friendly plain text endpoint (also used by UI button if desired)
	r.Get("/hello", controllers.Hello)

	r.Get("/about", controllers.About)
	// govibe:routes
	return nil
}
