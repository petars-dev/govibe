package devui

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cayleygraph/cayley/graph"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/codegraph"
)

// GraphUI serves the data the /dev/graph page draws: the freshness banner, the
// bounded views, a node's inspector, search, findings, and what refers to a
// table. Every call first brings stale layers up to date, as a page load and the
// MCP tools do, so what the page shows is never older than the files on disk.
type GraphUI struct {
	Indexer *codegraph.Indexer
	// Store is the graph store, for the hand-added quads (the one thing the
	// indexer's model does not hold).
	Store graph.QuadStore
	// DB reports whether the database explorer is mounted at /dev/db, which
	// needs a configured database: links into it exist only then.
	DB bool
}

// scopeOf reads the `scope` parameter of a request: app (the default), framework or all.
func scopeOf(r *http.Request) (codegraph.Scope, error) {
	return codegraph.ParseScope(r.URL.Query().Get("scope"))
}

// model refreshes stale layers and reads the graph for the request's scope (the app's
// own nodes unless it asks for more). stale is what is still stale afterwards: a layer
// that failed is not retried until its inputs change. A scope that does not exist is
// reported as errBadScope, which the handlers answer with 400.
func (ui *GraphUI) model(r *http.Request) (*codegraph.Model, []string, error) {
	scope, err := scopeOf(r)
	if err != nil {
		return nil, nil, &badScopeError{err}
	}
	ctx := r.Context()
	rctx, cancel := context.WithTimeout(ctx, graphRefreshTimeout)
	defer cancel()
	if _, err := ui.Indexer.Refresh(rctx); err != nil {
		logrus.WithError(err).Warn("graph index: refresh for the graph page failed; serving the stored graph")
	}
	m, err := ui.Indexer.Model(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	stale, _ := ui.Indexer.Stale(ctx)
	return m, stale, nil
}

// badScopeError is a request that names a scope there is not.
type badScopeError struct{ error }

// modelErr answers a failed model read: 400 for a bad scope, 500 for the rest.
func modelErr(w http.ResponseWriter, err error) {
	var bad *badScopeError
	if errors.As(err, &bad) {
		writeErr(w, http.StatusBadRequest, bad.error)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func (ui *GraphUI) links() codegraph.LinkOptions {
	return codegraph.LinkOptions{Root: ui.Indexer.RootDir(), DB: ui.DB}
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Status handles GET /dev/graph/data/status: the banner.
func (ui *GraphUI) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, stale, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m.Status(stale))
	}
}

// View handles GET /dev/graph/data/view?preset=&focus=&depth=&kinds=&limit=: a
// bounded subgraph to draw. preset=manual is the hand-added quads.
func (ui *GraphUI) View() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("preset") == "manual" && q.Get("focus") == "" {
			res, err := codegraph.ManualView(r.Context(), ui.Store, queryInt(r, "limit", 0))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
		m, _, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		res, err := m.View(codegraph.ViewQuery{
			Preset: q.Get("preset"), Focus: q.Get("focus"), Depth: queryInt(r, "depth", 0),
			Kinds: splitList(q.Get("kinds")), Limit: queryInt(r, "limit", 0),
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// Node handles GET /dev/graph/data/node?id=: the inspector of any node (an ID or
// any reference graph_context takes).
func (ui *GraphUI) Node() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		res, err := m.Inspect(r.URL.Query().Get("id"), ui.links())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// Search handles GET /dev/graph/data/search?q=&kinds=&limit=: nodes by words in
// their ID, name, path, title, signature or doc.
func (ui *GraphUI) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		m, _, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		res, err := m.SearchNodes(codegraph.SearchQuery{Text: q.Get("q"), Kinds: splitList(q.Get("kinds")), Limit: queryInt(r, "limit", 0)})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// Findings handles GET /dev/graph/data/findings?severity=&rule=&limit=: the
// findings panel.
func (ui *GraphUI) Findings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		m, _, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m.Findings(codegraph.FindingsQuery{Severity: q.Get("severity"), Rule: q.Get("rule"), Limit: queryInt(r, "limit", 200)}))
	}
}

// TableRefs handles GET /dev/graph/data/table?name=items: what refers to a
// table, for the "referenced by" panel of the database explorer.
func (ui *GraphUI) TableRefs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _, err := ui.model(r)
		if err != nil {
			modelErr(w, err)
			return
		}
		res, err := m.TableRefs(r.URL.Query().Get("name"), ui.links())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}
