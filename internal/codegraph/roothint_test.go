package codegraph

import (
	"testing"

	"github.com/cayleygraph/quad"
)

// The compile-time root of a module is recovered from one of its functions, in each
// way the runtime can spell the file.
func TestRootFromHint(t *testing.T) {
	const module = "github.com/acme/shop"
	for name, tc := range map[string]struct {
		ref  handlerRef
		want string
	}{
		"a function below the root":                       {handlerRef{Symbol: module + "/routes.Register", File: "/work/shop/routes/routes.go"}, "/work/shop/"},
		"a deeper package":                                {handlerRef{Symbol: module + "/internal/web/pages.Home.func1", File: "/work/shop/internal/web/pages/home.go"}, "/work/shop/"},
		"the root package":                                {handlerRef{Symbol: module + ".newApp.func1", File: "/work/shop/main.go"}, "/work/shop/"},
		"a method":                                        {handlerRef{Symbol: module + "/routes.(*Router).Mount", File: "/work/shop/routes/router.go"}, "/work/shop/"},
		"under -trimpath":                                 {handlerRef{Symbol: module + "/routes.Register", File: module + "/routes/routes.go"}, module + "/"},
		"a module cache checkout":                         {handlerRef{Symbol: module + "/routes.Register", File: "/home/dev/go/pkg/mod/github.com/acme/shop@v1.2.0/routes/routes.go"}, "/home/dev/go/pkg/mod/github.com/acme/shop@v1.2.0/"},
		"a function of another module":                    {handlerRef{Symbol: "github.com/other/lib/routes.Register", File: "/work/lib/routes/routes.go"}, ""},
		"a sibling module whose path starts the same":     {handlerRef{Symbol: module + "-extra/routes.Register", File: "/work/extra/routes/routes.go"}, ""},
		"a file that does not sit where the package says": {handlerRef{Symbol: module + "/routes.Register", File: "/work/shop/handlers/routes.go"}, ""},
		"no symbol":                                       {handlerRef{File: "/work/shop/main.go"}, ""},
	} {
		if got := rootFromHint(module, tc.ref); got != tc.want {
			t.Errorf("%s: rootFromHint = %q, want %q", name, got, tc.want)
		}
	}
}

// An application that is a module of its own is indexed by a process whose indexer
// was compiled elsewhere (the framework is a dependency of the app): a handler's
// file, spelled as the runtime spells it in that other checkout, resolves to the
// indexed source only when the process says where the app was compiled.
func TestRoutesResolveHandlersCompiledInAnotherCheckoutWithARootHint(t *testing.T) {
	routes := func() ([]runtimeRoute, error) {
		return []runtimeRoute{{
			Method: "GET", Path: "/api/hello",
			Handler: handlerRef{Symbol: "example.com/fixture/controllers.HelloAPI", File: "/ci/build/fixture/controllers/hello.go", Line: 10},
		}}, nil
	}
	handled := func(opts Options) bool {
		opts.RouteSource = routes
		out := buildLayer(t, newTestWorld(t, fixtureRoot, opts), routesLayer{})
		for _, q := range out.quads {
			if hintIRI(q.Subject) == "route:GET:/api/hello" && hintIRI(q.Predicate) == "handled_by" {
				return hintIRI(q.Object) == "func:controllers.HelloAPI"
			}
		}
		return false
	}

	if handled(Options{}) {
		t.Fatal("without a hint the handler of another checkout must not resolve (the indexer was compiled in this module)")
	}
	hint := handlerRef{Symbol: "example.com/fixture/routes.Register", File: "/ci/build/fixture/routes/routes.go"}
	if !handled(Options{RootHint: hint}) {
		t.Error("with the app's Routes function as a hint, the handler must resolve to the fixture's HelloAPI")
	}
	if handled(Options{RootHint: handlerRef{Symbol: "example.com/elsewhere/routes.Register", File: "/ci/build/fixture/routes/routes.go"}}) {
		t.Error("a hint from another module must be ignored")
	}
}

func hintIRI(v quad.Value) string {
	if i, ok := v.(quad.IRI); ok {
		return string(i)
	}
	return ""
}
