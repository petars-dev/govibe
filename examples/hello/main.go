package main

import (
	"embed"

	"github.com/petars-dev/govibe"
	"github.com/petars-dev/govibe/examples/hello/assets"
	"github.com/petars-dev/govibe/examples/hello/routes"
	migrations "github.com/petars-dev/govibe/examples/hello/storage/migrations"
)

// env.yml sits next to this file and is embedded, so the binary stays one file.
//
//go:embed env.yml
var configFS embed.FS

// newApp declares what this application contributes; the framework boots it.
func newApp() govibe.App {
	return govibe.App{
		Name:       "hello",
		Config:     configFS,
		Migrations: migrations.FS,
		Assets:     assets.FS,
		Routes:     routes.Register,
		Commands:   routes.Console,
	}
}

func main() { govibe.Main(newApp()) }
