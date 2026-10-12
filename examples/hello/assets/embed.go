package assets

import (
	"embed"
)

// FS is the application's static files and templates, handed to the framework as
// App.Assets and served under /assets. The dev panel's files and the htmx build
// are the framework's and are not here.
//
//go:embed templates/**
//go:embed static/**
var FS embed.FS
