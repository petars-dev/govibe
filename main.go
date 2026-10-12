package main

import (
	"embed"

	"petars-dev/govibe/assets"
	"petars-dev/govibe/internal/kernel"
	"petars-dev/govibe/routes"
	migrations "petars-dev/govibe/storage/migrations"
)

// env.yml sits next to this file and is embedded, so the binary stays one file.
//
//go:embed env.yml
var configFS embed.FS

// newApp declares what this application contributes; the framework boots it.
func newApp() kernel.App {
	return kernel.App{
		Name:       "govibe",
		Config:     configFS,
		Migrations: migrations.FS,
		Assets:     assets.FS,
		Routes:     routes.Register,
	}
}

func main() { kernel.Main(newApp()) }
