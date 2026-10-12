package kernel

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/web"
)

// FindRoot is the application's root directory: the configured root (the `root`
// setting, GOVIBE_ROOT) when there is one, else the nearest directory above the
// working directory that holds govibe.yml or go.mod, else the working directory.
// Everything that must not depend on where the process was started from (the dev
// assets directory, the graph) resolves against it.
func FindRoot(configured string) string {
	if configured != "" {
		if abs, err := filepath.Abs(configured); err == nil {
			return abs
		}
		return configured
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; ; {
		for _, marker := range []string{"govibe.yml", "go.mod"} {
			if st, err := os.Stat(filepath.Join(dir, marker)); err == nil && !st.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return wd
		}
		dir = parent
	}
}

// mountAssets serves the application's static files under /assets/ and says where
// they come from. In local and dev the app's assets directory is served from disk
// without caching when it exists (an edit shows on reload); otherwise the embedded
// App.Assets are served. The directory is resolved against the application root,
// never the working directory, and the choice is logged.
func mountAssets(r chi.Router, cfg config.Config, embedded fs.FS, root string) string {
	if cfg.IsLocal() || cfg.IsDev() {
		// Prefer a local override dir if present, else the tracked assets/ directory.
		for _, rel := range []string{"local/assets", "assets"} {
			dir := filepath.Join(root, rel)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				fsServer := http.FileServer(http.Dir(dir))
				logrus.WithField("path", dir).Info("serving assets from filesystem (dev mode)")
				r.Group(func(g chi.Router) {
					g.Use(web.DevNoCache)
					g.Handle("/assets/*", http.StripPrefix("/assets", fsServer))
				})
				return "disk " + dir
			}
		}
	}
	if embedded == nil {
		return "none"
	}
	logrus.Info("serving embedded assets")
	r.Handle("/assets/*", http.StripPrefix("/assets", http.FileServerFS(embedded)))
	return "embedded"
}
