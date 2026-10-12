package main

import (
	"embed"

	"example.com/shop/assets"
	"example.com/shop/routes"
	migrations "example.com/shop/storage/migrations"
	"petars-dev/govibe"
)

//go:embed env.yml
var configFS embed.FS

func newApp() govibe.App {
	return govibe.App{
		Name:       "shop",
		Config:     configFS,
		Migrations: migrations.FS,
		Assets:     assets.FS,
		Routes:     routes.Register,
		Commands:   routes.Console,
	}
}

func main() { govibe.Main(newApp()) }
