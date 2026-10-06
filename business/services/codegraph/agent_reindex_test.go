package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func layersRan(rep *Report) []string {
	var out []string
	for _, l := range rep.Layers {
		if l.Ran {
			out = append(out, l.Name)
		}
	}
	return out
}

// graph_reindex {layers}: the named layers are rebuilt, the rest left alone.
func TestRunLayersRebuildsOnlyWhatWasAskedFor(t *testing.T) {
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	opts := fixtureOptions()
	opts.Root = root
	ix := New(h.QuadStore, opts)
	ctx := context.Background()
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(ix.LayerNames(), ","); got != "code,routes,templates,config,mcp,db,docs" {
		t.Errorf("layer names: %s", got)
	}

	// nothing is stale: the named layer runs although its inputs did not change,
	// and so does the one built on it (docs resolves against config's keys)
	rep, err := ix.RunLayers(ctx, []string{"config"})
	if err != nil || rep == nil {
		t.Fatalf("%v %v", rep, err)
	}
	if got := strings.Join(layersRan(rep), ","); got != "config,docs" {
		t.Errorf("ran %s", got)
	}
	// a layer nothing is built on runs alone; the `layer:` prefix and case are accepted
	rep, err = ix.RunLayers(ctx, []string{"layer:Docs"})
	if err != nil || strings.Join(layersRan(rep), ",") != "docs" {
		t.Errorf("%v %v", layersRan(rep), err)
	}
	rep, err = ix.RunLayers(ctx, []string{" mcp ", "templates"})
	if err != nil || strings.Join(layersRan(rep), ",") != "templates,mcp,docs" {
		t.Errorf("two named: %v %v", layersRan(rep), err)
	}

	// a layer that is stale because its input changed comes along, with what was named
	writeFile(t, root, "docs/public/GUIDE.md", "# Guide\n\nChanged.\n")
	rep, err = ix.RunLayers(ctx, []string{"templates"})
	if err != nil || strings.Join(layersRan(rep), ",") != "templates,docs" {
		t.Errorf("a stale layer follows: %v %v", layersRan(rep), err)
	}

	// no names is a full run
	rep, err = ix.RunLayers(ctx, nil)
	if err != nil || len(layersRan(rep)) != 7 {
		t.Errorf("all: %v %v", layersRan(rep), err)
	}

	// an unknown name changes nothing and lists the real ones
	before, _ := ix.Stale(ctx)
	if rep, err = ix.RunLayers(ctx, []string{"docs", "nope"}); err == nil || rep != nil ||
		!strings.Contains(err.Error(), `unknown layer "nope"`) || !strings.Contains(err.Error(), "code, routes") {
		t.Errorf("unknown: %v %v", rep, err)
	}
	if _, err := ix.RunLayers(ctx, []string{"meta"}); err == nil {
		t.Error("meta is written by every run, not rebuilt by name")
	}
	if after, _ := ix.Stale(ctx); len(after) != len(before) {
		t.Errorf("a refused run changed state: %v -> %v", before, after)
	}
}

// A forced layer re-reads its sources, and the layers built on it follow: an edit
// that fingerprints cannot see (same size, same mtime) is picked up everywhere,
// and nothing is left pointing at what the edit removed.
func TestRunLayersIgnoresCachedIndexes(t *testing.T) {
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	opts := fixtureOptions()
	opts.Root = root
	ix := New(h.QuadStore, opts)
	ctx := context.Background()
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "controllers/hello.go")
	info, _ := os.Stat(file)
	data, _ := os.ReadFile(file)
	edited := strings.ReplaceAll(string(data), "HelloAPI", "HelloAPY")
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if stale, _ := ix.Stale(ctx); len(stale) != 0 {
		t.Fatalf("the edit should be invisible to fingerprints, stale: %v", stale)
	}
	if _, err := ix.RunLayers(ctx, []string{"code"}); err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if m.Node("func:controllers.HelloAPY") == nil || m.Node("func:controllers.HelloAPI") != nil {
		t.Error("a forced code layer should re-read the sources, and its dependents follow")
	}
	if got := m.Node("route:GET:/api/hello"); got == nil || got.first("handled_by") != "func:controllers.HelloAPY" {
		t.Errorf("the route should follow the rename, not point at a function that is gone: %+v", got)
	}
}
