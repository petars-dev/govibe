package web_test

import (
	"context"
	"os"

	"github.com/petars-dev/govibe/web"
)

// A page loads htmx through the framework, which serves the pinned build at
// /_govibe/static/htmx.min.js, instead of a copy of its own.
func ExampleHTMXScript() {
	_ = web.HTMXScript().Render(context.Background(), os.Stdout)
	// Output: <script src="/_govibe/static/htmx.min.js"></script>
}
