// Package stubfw stands in for the framework in the indexer's tests: a module the
// fixture application depends on, with the same shape (a root package with the App
// declaration, public helper packages, an internal package), so the graph can be
// tested against a project that is built on a framework without building the real one.
// It compiles but is never run.
package stubfw

import (
	"net/http"

	"example.com/stubfw/internal/kernel"
)

// App declares what an application contributes.
type App struct {
	Name    string
	Handler http.Handler
}

// Main runs an application.
func Main(app App) { kernel.Run(app.Name, app.Handler) }
