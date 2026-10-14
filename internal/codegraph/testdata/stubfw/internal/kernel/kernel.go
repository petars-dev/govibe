// Package kernel boots an application; it is the framework's own.
package kernel

import (
	"log"
	"net/http"
)

// Run serves h.
func Run(name string, h http.Handler) {
	log.Println("running", name)
	log.Fatal(http.ListenAndServe(":8080", h))
}
