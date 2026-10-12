package codegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"strings"
)

// mcpLayer indexes the MCP tools the application registers with
// mcp.AddTool(srv, &mcp.Tool{Name: ..., Description: ...}, handler). A tool is
// traced to code by the function that registers it (`func --registers--> tool`):
// a handler literal is a closure, and a closure resolves to its enclosing
// function, so that is the registering function. Everything comes from the code
// index, so the layer has no inputs of its own.
type mcpLayer struct{}

func (mcpLayer) name() string                       { return "mcp" }
func (mcpLayer) deps() []string                     { return []string{"code"} }
func (mcpLayer) fingerprint(*world) (string, error) { return "", nil }

func (mcpLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}
	set := newQuadSet("mcp")
	described := map[string]bool{}
	for _, t := range ci.tools {
		id := toolID(t.name)
		if !described[t.name] {
			described[t.name] = true
			set.add(id, PredType, KindMCPTool)
			set.str(id, PredName, t.name)
			set.str(id, PredPath, t.file)
			set.num(id, PredLine, t.line)
			if t.description != "" {
				set.str(id, PredDescription, t.description)
			}
			if schema := ci.inputSchema(t); schema != "" {
				set.str(id, PredInputSchema, schema)
			}
		}
		if t.fn != "" {
			set.add(t.fn, PredRegisters, id)
		}
	}
	return &layerOutput{quads: set.quads, warnings: ci.toolWarns}, nil
}

// inputSchema renders the JSON schema of a tool's input from the handler's
// input struct (its json and jsonschema tags), or "" when the handler does not
// name a struct of the registering package. It is the shape an agent sends, in
// the same words the SDK derives: json names, `omitempty` makes a property
// optional, and the jsonschema tag is its description.
func (ci *codeIndex) inputSchema(t toolReg) string {
	if t.emptyInput {
		return `{"type":"object"}`
	}
	if t.input == "" {
		return ""
	}
	st := ci.typeNamed(t.dir, t.input)
	if st == nil || st.fields == nil {
		return ""
	}
	props := map[string]any{}
	var required []string
	for _, f := range st.fields {
		if !ast.IsExported(f.name) {
			continue
		}
		name := f.name
		omit := false
		if tag, ok := f.tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
			for _, o := range parts[1:] {
				omit = omit || o == "omitempty" || o == "omitzero"
			}
		}
		prop := map[string]any{}
		if typ := jsonType(f.typ); typ != "" {
			prop["type"] = typ
		}
		if desc := f.tag.Get("jsonschema"); desc != "" {
			prop["description"] = desc
		}
		props[name] = prop
		if !omit && !strings.HasPrefix(f.typ, "*") {
			required = append(required, name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema) // map keys are sorted: deterministic
	if err != nil {
		return ""
	}
	return string(b)
}

// jsonType maps a Go type expression to a JSON schema type; "" for any.
func jsonType(typ string) string {
	typ = strings.TrimLeft(typ, "*")
	switch {
	case typ == "string":
		return "string"
	case typ == "bool":
		return "boolean"
	case strings.HasPrefix(typ, "int"), strings.HasPrefix(typ, "uint"):
		return "integer"
	case strings.HasPrefix(typ, "float"):
		return "number"
	case strings.HasPrefix(typ, "[]"), strings.HasPrefix(typ, "["):
		return "array"
	case typ == "any", typ == "interface{}":
		return ""
	}
	return "object"
}
