package codegraph

import (
	"context"
	"fmt"
)

// externalsLayer owns the `external` nodes: the packages and functions outside the
// project that other layers point at (the framework's public API the project imports and
// calls, the libraries a handler or a call goes into, the framework's internals as one
// leaf). The layers that point at them write only the edge (imports_external,
// calls_external, handled_by_external), with an ID that names the node; the node itself, with
// its module, its version and its origin, is written once, here, whichever layers
// refer to it. It is built from the indexes of the layers it reads, not from their
// quads, so it is as fresh as they are.
type externalsLayer struct{}

func (externalsLayer) name() string { return "externals" }
func (externalsLayer) deps() []string {
	return []string{"code", "routes", "templates", callsLayerName}
}

// fingerprint is a constant: the layers it depends on are its inputs.
func (externalsLayer) fingerprint(*world) (string, error) { return "derived", nil }

func (externalsLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	mods := w.modules()
	set := newQuadSet("externals")
	ex := newExternals(mods, set)

	// What the project's Go packages import from the framework.
	if ci, err := w.code(); err == nil {
		for _, r := range ci.extImports {
			ex.pkg(r.name)
		}
	}
	// What its templates import: their generated Go is not indexed.
	if ti, err := w.templates(); err == nil {
		for _, f := range ti.files {
			for _, imp := range f.imports {
				if mods.isFramework(imp) {
					ex.pkg(imp)
				}
			}
		}
	}
	// What serves its routes, when it is not its own code.
	if ci, err := w.code(); err == nil {
		if rt, err := w.routeTable(); err == nil && !rt.absent {
			for _, r := range rt.routes {
				if w.resolveFunc(ci, r.Handler) != nil {
					continue
				}
				if sym := externalSymbol(ci, r.Handler); sym != "" {
					if path, name, ok := externalSpec(mods, sym); ok {
						ex.leaf(path, name)
					}
				}
			}
		}
	}
	// What its functions call, when the calls layer is on.
	if w.x.enabled[callsLayerName] {
		idx, err := w.calls()
		if err != nil {
			// The calls layer failed and keeps its old edges; their leaves must stay, so this
			// layer fails with it and keeps its own old nodes instead of dropping them.
			return nil, fmt.Errorf("calls index: %w", err)
		}
		for _, c := range idx.external {
			ex.leaf(c.importPath, c.name)
		}
	}
	return &layerOutput{quads: set.quads}, nil
}
