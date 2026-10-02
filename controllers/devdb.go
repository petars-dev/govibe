package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"trace-loop/os/storage/dbexplorer"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// DevDBMeta reports capabilities of the embedded explorer (used by the /dev/db
// UI to decide whether to show mutation controls).
func DevDBMeta(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"allow_mutations": ex.AllowMutations(),
		})
	}
}

// DevDBListTables handles GET /dev/db/api/tables.
func DevDBListTables(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tables, err := ex.ListTables(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
	}
}

// DevDBDescribeTable handles GET /dev/db/api/tables/{table}.
func DevDBDescribeTable(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := chi.URLParam(r, "table")
		schema, err := ex.DescribeTable(r.Context(), table)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, schema)
	}
}

// DevDBBrowseRows handles GET /dev/db/api/tables/{table}/rows?where=&order=&limit=&offset=.
func DevDBBrowseRows(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := chi.URLParam(r, "table")
		opts := dbexplorer.BrowseOptions{
			Where:   r.URL.Query().Get("where"),
			OrderBy: r.URL.Query().Get("order"),
			Limit:   queryInt(r, "limit", 100),
			Offset:  queryInt(r, "offset", 0),
		}
		res, err := ex.BrowseRows(r.Context(), table, opts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// DevDBSampleRows handles GET /dev/db/api/tables/{table}/sample?limit=.
func DevDBSampleRows(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := chi.URLParam(r, "table")
		res, err := ex.SampleRows(r.Context(), table, queryInt(r, "limit", 20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

type sqlRequest struct {
	SQL string `json:"sql"`
}

// DevDBQuery handles POST /dev/db/api/query, body: {"sql": "SELECT ..."}.
func DevDBQuery(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req sqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		res, err := ex.Query(r.Context(), req.SQL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// DevDBExplain handles POST /dev/db/api/explain, body: {"sql": "SELECT ..."}.
func DevDBExplain(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req sqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		res, err := ex.Explain(r.Context(), req.SQL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

type insertRowRequest struct {
	Values map[string]any `json:"values"`
}

// DevDBInsertRow handles POST /dev/db/api/tables/{table}/rows, body: {"values": {...}}.
func DevDBInsertRow(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := chi.URLParam(r, "table")
		var req insertRowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		res, err := ex.InsertTestRow(r.Context(), table, req.Values)
		if err != nil {
			status := http.StatusBadRequest
			if err == dbexplorer.ErrNotAllowed {
				status = http.StatusForbidden
			}
			writeErr(w, status, err)
			return
		}
		writeJSON(w, http.StatusCreated, res)
	}
}

// DevDBResetTable handles DELETE /dev/db/api/tables/{table}/rows.
func DevDBResetTable(ex *dbexplorer.Explorer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		table := chi.URLParam(r, "table")
		res, err := ex.ResetTestData(r.Context(), table)
		if err != nil {
			status := http.StatusBadRequest
			if err == dbexplorer.ErrNotAllowed {
				status = http.StatusForbidden
			}
			writeErr(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// DevDBConsole renders the embedded, Faucet-like database explorer UI. The
// page talks to the store purely through the JSON API mounted at
// /dev/db/api/*.
func DevDBConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(devDBPage))
}
