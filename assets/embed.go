package assets

import (
	"embed"
)

//go:embed htmx/htmx.min.js
//go:embed templates/**
//go:embed static/**
var FS embed.FS
