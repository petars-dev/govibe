// Package devui holds the files the dev panel's pages load: stylesheets, scripts,
// the graph renderer and its vendored library, and the logo. They belong to the
// framework, not to the application, and are served under /_govibe/static; the
// application's /assets never carries them, and a build without the dev tools
// links none of this.
package devui

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static is the dev panel's files, rooted at the directory the pages name:
// theme.css is served at /_govibe/static/theme.css.
func Static() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err) // the embed pattern above guarantees the directory
	}
	return sub
}
