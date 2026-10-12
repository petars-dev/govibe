package kernel

import "petars-dev/govibe/internal/devtools"

// devCompiled says the dev tools are part of this binary. Phase 2 moves this file
// behind the govibe_dev build tag, with an untagged twin that says false, so a
// default build links none of devtools.
const devCompiled = true

// mountDev hands the finished router to the dev tools.
func mountDev(b *Built) {
	stop := devtools.Mount(devtools.Deps{
		Router: b.Router,
		DB:     b.Rt.DB,
		Config: b.Rt.Config,
		Root:   b.Root,
	})
	// Closed before the database pool: the indexer reads the schema through it.
	b.closers = append(b.closers, func() error { stop(); return nil })
}
