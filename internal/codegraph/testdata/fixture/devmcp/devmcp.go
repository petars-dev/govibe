// Package devmcp stands in for the dev-only tooling package.
package devmcp

import (
	"context"
	"net/http"

	"example.com/fixture/mcp"
	"example.com/fixture/storage/orderstore"
)

// NewHandler returns the dev tooling handler. Dev packages may import storage.
func NewHandler(store *orderstore.Store) http.Handler {
	srv := mcp.NewServer()
	registerTools(srv)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("dev tools"))
	})
}

type echoIn struct {
	Message string `json:"message" jsonschema:"text to echo back"`
	Repeat  int    `json:"repeat,omitempty" jsonschema:"how many times to repeat it"`
}

type echoOut struct {
	Text string `json:"text"`
}

func registerTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fixture_echo",
		Description: "Echo a message. " + "Read-only.",
	}, func(ctx context.Context, _ *mcp.Request, in echoIn) (echoOut, error) {
		return echoOut{Text: in.Message}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{Name: "fixture_ping", Description: "Check that the dev tooling is up."},
		func(ctx context.Context, _ *mcp.Request, _ struct{}) (echoOut, error) {
			return echoOut{}, nil
		})
}
