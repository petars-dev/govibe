// Package routeinfo reads a live chi router into plain data: method, path and
// the runtime's description of each handler and middleware. It depends on chi,
// reflect and runtime only, so the app binary can use it (inspect --json) while
// the heavy graph code stays out of a default build.
package routeinfo

import (
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// AnyMethod is the method token of a route registered for every method
// (chi's Handle). chi.Walk reports such a route once per HTTP method; it is one
// route, so Walk collapses it.
const AnyMethod = "ANY"

// Route is one route as the running router reports it.
type Route struct {
	Method  string // upper-case HTTP method, or AnyMethod
	Path    string // chi pattern, e.g. /dev/db/api/tables/{table}
	Handler Ref
	MW      []Ref // middleware, outermost first
}

// Ref is a function as the Go runtime describes it.
type Ref struct {
	Symbol string // runtime function name; "" when it could not be determined
	File   string // source file the runtime reports: absolute, or module-relative under -trimpath
	Line   int    // line of the function's declaration or closure literal
}

type middlewareFunc = func(http.Handler) http.Handler

// Sort orders routes by path, then method, and returns them.
func Sort(rs []Route) []Route {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Path != rs[j].Path {
			return rs[i].Path < rs[j].Path
		}
		return rs[i].Method < rs[j].Method
	})
	return rs
}

// Key is the identity of a route in a table: "METHOD path".
func (r Route) Key() string { return r.Method + " " + r.Path }

// Walk walks a chi router the way chi.Walk does (sub-routers, inline middleware
// groups, the middleware stack of every level) with one difference: a catch-all
// registration is reported once, as AnyMethod, instead of once per method.
func Walk(r chi.Routes) []Route {
	var out []Route
	walk(r, "", nil, &out)
	return Sort(out)
}

func walk(r chi.Routes, parent string, parentMW []middlewareFunc, out *[]Route) {
	for _, route := range r.Routes() {
		mws := append(append([]middlewareFunc(nil), parentMW...), r.Middlewares()...)
		if route.SubRoutes != nil {
			walk(route.SubRoutes, parent+route.Pattern, mws, out)
			continue
		}
		path := strings.ReplaceAll(parent+route.Pattern, "/*/", "/")

		emit := func(method string, h http.Handler) {
			chain := mws
			if ch, ok := h.(*chi.ChainHandler); ok {
				chain = append(append([]middlewareFunc(nil), mws...), ch.Middlewares...)
				h = ch.Endpoint
			}
			rr := Route{Method: method, Path: path, Handler: HandlerRef(h)}
			for _, m := range chain {
				rr.MW = append(rr.MW, FuncRef(reflect.ValueOf(m)))
			}
			*out = append(*out, rr)
		}

		if all, ok := route.Handlers["*"]; ok {
			emit(AnyMethod, all)
			// A specific method registered over a catch-all keeps its own route.
			for m, h := range route.Handlers {
				if m != "*" && HandlerRef(unwrap(h)) != HandlerRef(unwrap(all)) {
					emit(m, h)
				}
			}
			continue
		}
		for m, h := range route.Handlers {
			emit(m, h)
		}
	}
}

func unwrap(h http.Handler) http.Handler {
	if ch, ok := h.(*chi.ChainHandler); ok {
		return ch.Endpoint
	}
	return h
}

// HandlerRef identifies the function that serves h: the function itself for a
// http.HandlerFunc, the ServeHTTP method for any other handler type.
func HandlerRef(h http.Handler) Ref {
	v := reflect.ValueOf(h)
	if !v.IsValid() {
		return Ref{}
	}
	if v.Kind() == reflect.Func {
		return FuncRef(v)
	}
	if m, ok := v.Type().MethodByName("ServeHTTP"); ok {
		return FuncRef(m.Func)
	}
	return Ref{}
}

// FuncRef describes the function held by v, or the zero Ref when v is not a
// non-nil func.
func FuncRef(v reflect.Value) Ref {
	if !v.IsValid() || v.Kind() != reflect.Func || v.IsNil() {
		return Ref{}
	}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return Ref{}
	}
	file, line := fn.FileLine(fn.Entry())
	return Ref{Symbol: fn.Name(), File: filepath.ToSlash(file), Line: line}
}
