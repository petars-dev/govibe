//go:build govibe_dev

package kernel

import "github.com/petars-dev/govibe/internal/devtools"

func init() {
	mountDev = func(b *Built) {
		doc := b.Inspect()
		deps := devtools.Deps{
			Router:  b.Router,
			DB:      b.Rt.DB,
			Config:  b.Rt.Config,
			Root:    b.Root,
			Inspect: &doc,
		}
		if b.Scheduler != nil { // not a nil *Runner in the interface: no scheduler is no value at all
			deps.Scheduler = b.Scheduler
		}
		stop := devtools.Mount(deps)
		// Closed before the database pool: the indexer reads the schema through it.
		b.closers = append(b.closers, func() error { stop(); return nil })
	}
}
