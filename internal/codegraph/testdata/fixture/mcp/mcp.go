// Package mcp stands in for the MCP SDK so the fixture stays standard-library only.
package mcp

import "context"

// Server collects the tools an application registers.
type Server struct{}

// Tool describes one tool.
type Tool struct {
	Name        string
	Description string
}

// Request is the incoming tool call.
type Request struct{}

// NewServer returns an empty server.
func NewServer() *Server { return &Server{} }

// AddTool registers a typed tool.
func AddTool[In, Out any](s *Server, t *Tool, h func(ctx context.Context, req *Request, in In) (Out, error)) {
}
