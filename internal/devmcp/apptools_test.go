package devmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/devext"
)

type summaryIn struct {
	Status string `json:"status,omitempty" jsonschema:"only orders in this status"`
}

type summaryOut struct {
	Count int `json:"count"`
}

func appToolsOptions() Options {
	return Options{AppName: "shop", AppTools: []devext.Tool{
		devext.ToolFunc("orders_summary", "Count the orders.", func(_ context.Context, in summaryIn) (summaryOut, error) {
			if in.Status == "bogus" {
				return summaryOut{}, errors.New("no such status")
			}
			return summaryOut{Count: 3}, nil
		}),
		{Name: "cache_flush", Description: "Flush the cache.", Mutates: true, Run: func(context.Context, json.RawMessage) (any, error) { return "flushed", nil }},
		{Name: "boom", Description: "Panics.", Run: func(context.Context, json.RawMessage) (any, error) { panic("kaboom") }},
	}}
}

func listTools(t *testing.T, h http.Handler) map[string]map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/dev/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data:") {
			body = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	var env struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("tools/list: %v: %s", err, body)
	}
	out := map[string]map[string]any{}
	for _, tool := range env.Result.Tools {
		out[tool["name"].(string)] = tool
	}
	return out
}

// The app's tools are listed next to the framework's, with a schema from their Input,
// and answer with structured content; an error or a panic is a failed call, not a dead
// server.
func TestAppToolsAreServedWithTheirSchema(t *testing.T) {
	h := NewHandler(nil, nil, nil, appToolsOptions())
	tools := listTools(t, h)
	if _, ok := tools["schedule_status"]; !ok {
		t.Fatalf("the framework's tools are gone: %v", tools)
	}
	summary, ok := tools["orders_summary"]
	if !ok {
		t.Fatalf("orders_summary is not listed: %v", tools)
	}
	schema, _ := json.Marshal(summary["inputSchema"])
	if !strings.Contains(string(schema), `"status"`) || !strings.Contains(string(schema), "only orders in this status") {
		t.Errorf("schema = %s", schema)
	}
	if ann, _ := summary["annotations"].(map[string]any); ann["readOnlyHint"] != true {
		t.Errorf("orders_summary annotations = %v", summary["annotations"])
	}
	if ann, _ := tools["cache_flush"]["annotations"].(map[string]any); ann["readOnlyHint"] != false {
		t.Errorf("cache_flush annotations = %v", tools["cache_flush"]["annotations"])
	}

	out := callTool(t, h, "orders_summary", `{"status":"open"}`)
	if !strings.Contains(out, `\"count\":3`) || strings.Contains(out, `"isError":true`) {
		t.Errorf("orders_summary: %s", out)
	}
	out = callTool(t, h, "cache_flush", `{}`)
	if !strings.Contains(out, "flushed") {
		t.Errorf("cache_flush: %s", out)
	}
	out = callTool(t, h, "orders_summary", `{"status":"bogus"}`)
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "no such status") {
		t.Errorf("an error must be a failed call: %s", out)
	}
	out = callTool(t, h, "boom", `{}`)
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "kaboom") {
		t.Errorf("a panic must be a failed call: %s", out)
	}
	// The server is still up after the panic.
	if out := callTool(t, h, "orders_summary", `{}`); !strings.Contains(out, `\"count\":3`) {
		t.Errorf("after the panic: %s", out)
	}
}

func TestInputSchemaOfAcceptsStructsOnly(t *testing.T) {
	for _, in := range []any{nil, struct{}{}, &struct{}{}} {
		s, err := inputSchemaOf(in)
		if err != nil || s.Type != "object" || len(s.Properties) != 0 {
			t.Errorf("%T: %+v %v", in, s, err)
		}
	}
	if _, err := inputSchemaOf("text"); err == nil {
		t.Error("a string Input has a schema")
	}
	s, err := inputSchemaOf(summaryIn{})
	if err != nil || s.Properties["status"] == nil {
		t.Errorf("summaryIn: %+v %v", s, err)
	}
}
