package devext

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func noop(http.ResponseWriter, *http.Request) {}

func TestToolsAreValidatedAndCollected(t *testing.T) {
	d := New()
	run := func(context.Context, json.RawMessage) (any, error) { return "ok", nil }
	d.Tool(Tool{Name: "orders_summary", Description: "Sum the orders", Run: run})
	d.Tool(Tool{Name: "orders_summary", Run: run})  // twice
	d.Tool(Tool{Name: "graph_mine", Run: run})      // the framework's family
	d.Tool(Tool{Name: "Bad-Name", Run: run})        // not a name
	d.Tool(Tool{Name: "no_run"})                    // nothing to run
	d.Tool(Tool{Description: "nameless", Run: run}) // no name
	if got := d.Tools(); len(got) != 1 || got[0].Name != "orders_summary" {
		t.Fatalf("tools = %+v", got)
	}
	problems := strings.Join(d.Problems(), "\n")
	for _, want := range []string{"registered twice", "graph_ are the framework's", `"Bad-Name"`, "has no Run", "no name"} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems lack %q:\n%s", want, problems)
		}
	}
	if d.Empty() {
		t.Error("Empty with a tool registered")
	}
}

func TestPagesAndHandlersLiveUnderThePrefix(t *testing.T) {
	d := New()
	d.Page("orders/", "Orders", http.HandlerFunc(noop))
	d.Page("/", "root", http.HandlerFunc(noop)) // nothing named
	d.Page("/untitled", "", http.HandlerFunc(noop))
	d.Handle("post", "/orders/refresh", http.HandlerFunc(noop))
	d.Handle("", "/orders/{id}", nil)
	pages := d.Pages()
	if len(pages) != 2 || pages[0].Path != "/orders" || pages[0].Title != "Orders" || pages[1].Title != "untitled" {
		t.Fatalf("pages = %+v", pages)
	}
	routes := d.Routes()
	if len(routes) != 3 || routes[0].Method != "GET" || routes[2].Method != "POST" || routes[2].Pattern != "/orders/refresh" {
		t.Fatalf("routes = %+v", routes)
	}
	if p := d.Problems(); len(p) != 2 {
		t.Errorf("problems = %v", p)
	}
	if New().Empty() != true {
		t.Error("a new Dev is not empty")
	}
}

func TestToolFuncDecodesTypedArguments(t *testing.T) {
	type in struct {
		N int `json:"n"`
	}
	tool := ToolFunc("double", "Double n", func(_ context.Context, a in) (map[string]int, error) {
		return map[string]int{"result": a.N * 2}, nil
	})
	if _, ok := tool.Input.(in); !ok || tool.Name != "double" || tool.Mutates {
		t.Fatalf("tool = %+v", tool)
	}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"n": 21}`))
	if err != nil || out.(map[string]int)["result"] != 42 {
		t.Fatalf("run = %v, %v", out, err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"n": "x"}`)); err == nil || !strings.Contains(err.Error(), "arguments") {
		t.Errorf("bad arguments: %v", err)
	}
	if out, err := tool.Run(context.Background(), nil); err != nil || out.(map[string]int)["result"] != 0 {
		t.Errorf("no arguments: %v, %v", out, err)
	}
}
