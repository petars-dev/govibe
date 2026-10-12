// Package web holds the small helpers an application's controllers and views
// share with the framework: the htmx script the framework serves, and IfDev,
// which keeps links to the dev tools out of a production page.
package web

import (
	"context"
	_ "embed"
	"io"
	"net/http"

	"github.com/a-h/templ"
)

//go:embed htmx.min.js
var htmx []byte

// StaticPrefix is where the framework serves its own embedded files. It is the
// framework's namespace (reserved, like /dev): an application's files live under
// /assets.
const StaticPrefix = "/_govibe/static"

// HTMXPath is the URL of the htmx build the framework ships, pinned per release.
const HTMXPath = StaticPrefix + "/htmx.min.js"

// ServeHTMX serves the embedded htmx build. The kernel mounts it at HTMXPath.
func ServeHTMX(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(htmx)
}

// HTMXScript renders the script tag that loads htmx. A page uses it instead of a
// path of its own, so the framework owns the file and its version.
func HTMXScript() templ.Component {
	return templ.Raw(`<script src="` + HTMXPath + `"></script>`)
}

type devKey struct{}

// MarkDev is the middleware the kernel installs when it mounts the dev tools:
// requests then carry the fact in their context, where IfDev reads it. There is
// no global switch to forget.
func MarkDev(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), devKey{}, true)))
	})
}

// IsDev reports whether the request is served by an app with the dev tools mounted.
func IsDev(ctx context.Context) bool {
	v, _ := ctx.Value(devKey{}).(bool)
	return v
}

// IfDev renders its children only when the dev tools are mounted. In a template:
//
//	@web.IfDev() {
//	    <a href="/dev/graph">Graph</a>
//	}
//
// A production build shows none of it, so a page never links to a /dev path that
// answers 404.
func IfDev() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if !IsDev(ctx) {
			return nil
		}
		return templ.GetChildren(ctx).Render(templ.ClearChildren(ctx), w)
	})
}
