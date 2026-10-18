package devmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/devext"
)

// registerAppTools adds the tools the application declared through App.DevTools. The
// schema of each comes from its Input the way the SDK derives one for the framework's
// own tools; a tool whose Input cannot be described is registered with an open object
// schema and a warning, so the panel comes up whatever the app wrote.
func registerAppTools(srv *mcp.Server, tools []devext.Tool) {
	for _, t := range tools {
		t := t
		schema, err := inputSchemaOf(t.Input)
		if err != nil {
			logrus.WithError(err).Warnf("dev tools: tool %s: its Input has no JSON schema; agents get an open object", t.Name)
			schema = &jsonschema.Schema{Type: "object"}
		}
		srv.AddTool(&mcp.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !t.Mutates, OpenWorldHint: boolPtr(false)},
		}, appToolHandler(t))
	}
}

// inputSchemaOf is the JSON schema of a tool's arguments: an empty object for no
// Input or struct{}, the struct's fields otherwise.
func inputSchemaOf(input any) (*jsonschema.Schema, error) {
	if input == nil {
		return &jsonschema.Schema{Type: "object"}, nil
	}
	rt := reflect.TypeOf(input)
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("Input is %s, not a struct", rt)
	}
	if rt.NumField() == 0 {
		return &jsonschema.Schema{Type: "object"}, nil
	}
	return jsonschema.ForType(rt, nil)
}

// appToolHandler runs one of the app's tools: the arguments go to Run as JSON, the
// result comes back as structured content and as text, and an error is a failed call
// the agent reads, never a server failure.
func appToolHandler(t devext.Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args json.RawMessage
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		out, err := safeRun(ctx, t, args)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: t.Name + ": " + err.Error()}}}, nil
		}
		res := &mcp.CallToolResult{}
		switch v := out.(type) {
		case nil:
			res.Content = []mcp.Content{&mcp.TextContent{Text: "ok"}}
		case string:
			res.Content = []mcp.Content{&mcp.TextContent{Text: v}}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: t.Name + ": the result could not be encoded: " + err.Error()}}}, nil
			}
			res.StructuredContent = json.RawMessage(b)
			res.Content = []mcp.Content{&mcp.TextContent{Text: string(b)}}
		}
		return res, nil
	}
}

// safeRun calls Run and turns a panic into an error: the app's code must not take the
// dev server down.
func safeRun(ctx context.Context, t devext.Tool, args json.RawMessage) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("dev tools: tool %s panicked: %v", t.Name, r)
			out, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()
	return t.Run(ctx, args)
}
