//go:build govibe_dev

package kernel

import (
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/devext"
	"github.com/petars-dev/govibe/internal/devtools"
)

func init() {
	mountDev = func(b *Built) {
		doc := b.Inspect()
		deps := devtools.Deps{
			Router:  b.Router,
			DB:      b.Rt.DB,
			Config:  b.Rt.Config,
			Root:    b.Root,
			Inspect: &doc,
			App:     appDevTools(b),
		}
		if b.Scheduler != nil { // not a nil *Runner in the interface: no scheduler is no value at all
			deps.Scheduler = b.Scheduler
		}
		stop := devtools.Mount(deps)
		// Closed before the database pool: the indexer reads the schema through it.
		b.closers = append(b.closers, func() error { stop(); return nil })
	}
}

// appDevTools asks the app what it adds to the dev panel. An error leaves the app's
// tools out and is logged, never fatal: the panel comes up whatever the app does, so
// that an agent can read why.
func appDevTools(b *Built) *devext.Dev {
	dev := devext.New()
	if b.App.DevTools == nil {
		return dev
	}
	if err := b.App.DevTools(b.Rt, dev); err != nil {
		logrus.WithError(err).Error("dev tools: App.DevTools failed, so the app's own tools and pages are not mounted")
		return devext.New()
	}
	for _, p := range dev.Problems() {
		logrus.Warn("dev tools: App.DevTools: " + p)
	}
	return dev
}
