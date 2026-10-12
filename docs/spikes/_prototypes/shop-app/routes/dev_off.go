//go:build !govibe_dev

package routes

import (
	"github.com/go-chi/chi/v5"

	"petars-dev/govibe"
)

func devRoutes(rt *govibe.Runtime, r chi.Router) {}
