//go:build govibe_dev

package kernel

import "github.com/petars-dev/govibe/internal/devtools"

func init() {
	mountDev = func(b *Built) {
		stop := devtools.Mount(devtools.Deps{
			Router:   b.Router,
			DB:       b.Rt.DB,
			Config:   b.Rt.Config,
			Root:     b.Root,
			RootHint: rootHint(b.App),
		})
		// Closed before the database pool: the indexer reads the schema through it.
		b.closers = append(b.closers, func() error { stop(); return nil })
	}
}
