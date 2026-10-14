package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/dbexplorer"
)

const (
	maxMigrationBytes = 1 << 20
	maxMigrations     = 5000

	// liveSchemaTTL is how long a live schema read is reused. The live database
	// can change without any file changing, so a freshness check has to look at
	// it, but not on every page load and tool call.
	liveSchemaTTL     = 5 * time.Second
	liveSchemaTimeout = 10 * time.Second
)

// liveSchema is the live database's schema, or why it could not be read.
type liveSchema struct {
	configured bool
	snap       *dbexplorer.Snapshot
	err        error
	fp         string
}

// migrationScan lists the goose migrations of the directory the profile names
// (storage/migrations/sql by default). A project with none names no directory.
func (w *world) migrationScan() (*scan, error) {
	return w.migFiles.get(func() (*scan, error) {
		prof, err := w.profile()
		if err != nil {
			return nil, err
		}
		if prof.Migrations == "" {
			return &scan{}, nil
		}
		return walkFiles(w.root, walkSpec{
			start:    prof.Migrations,
			accept:   func(name string) bool { return strings.HasSuffix(name, ".sql") && !strings.HasPrefix(name, ".") },
			maxBytes: maxMigrationBytes,
			maxFiles: maxMigrations,
		})
	})
}

// live reads the live database's schema once per run, reusing a read from the
// last few seconds unless this is an explicit reindex.
func (w *world) live() *liveSchema {
	res, _ := w.liveV.get(func() (*liveSchema, error) {
		fn := w.x.opts.Schema
		if fn == nil {
			return &liveSchema{}, nil
		}
		ttl := w.x.opts.liveTTL
		if ttl == 0 {
			ttl = liveSchemaTTL
		}
		if c := w.x.liveCache; !w.force && ttl > 0 && c.res != nil && time.Since(c.at) < ttl {
			return c.res, nil
		}
		ctx, cancel := context.WithTimeout(w.ctx, liveSchemaTimeout)
		defer cancel()
		res := &liveSchema{configured: true}
		res.snap, res.err = fn(ctx)
		if res.err == nil {
			b, err := json.Marshal(res.snap)
			if err != nil {
				res.snap, res.err = nil, err
			} else {
				sum := sha256.Sum256(b)
				res.fp = hex.EncodeToString(sum[:])[:16]
			}
		}
		w.x.liveCache.at, w.x.liveCache.res = time.Now(), res
		return res, nil
	})
	return res
}

var migrationName = regexp.MustCompile(`^([0-9]+)_`)

// dbModel returns the schema: the migrations applied in order, merged with the
// live database's when there is one. It never reads rows.
func (w *world) dbModel() (*dbModel, error) {
	return w.dbIdx.get(func() (*dbModel, error) {
		sc, err := w.migrationScan()
		if err != nil {
			return nil, err
		}
		sch := newSQLSchema()
		var warnings []string
		var migs []*dbMigration
		for _, sf := range sc.files {
			stem := strings.TrimSuffix(path.Base(sf.rel), ".sql")
			m := migrationName.FindStringSubmatch(stem)
			if m == nil {
				warnings = append(warnings, fmt.Sprintf("%s: not a goose migration name (no leading version number), skipped", sf.rel))
				continue
			}
			num, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: version out of range, skipped", sf.rel))
				continue
			}
			migs = append(migs, &dbMigration{stem: stem, version: m[1], num: num, path: sf.rel})
		}
		sort.Slice(migs, func(i, j int) bool {
			if migs[i].num != migs[j].num {
				return migs[i].num < migs[j].num
			}
			return migs[i].stem < migs[j].stem
		})
		for _, mg := range migs {
			if err := w.ctx.Err(); err != nil {
				return nil, err
			}
			src, err := os.ReadFile(path.Join(w.root, mg.path))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", mg.path, err))
				continue
			}
			up, skipped := gooseUp(string(src))
			if skipped > 0 {
				warnings = append(warnings, fmt.Sprintf("%s: %d stored-program block(s) between StatementBegin and StatementEnd are not read", mg.path, skipped))
			}
			for _, st := range splitStmts(up, lexSQL(up)) {
				sch.apply(st, mg.stem)
			}
		}
		static := modelFromSQL(sch)
		static.migrations = migs
		static.warnings = append(warnings, sch.warnings...)

		lv := w.live()
		switch {
		case !lv.configured:
			return static, nil
		case lv.err != nil:
			static.warnings = append(static.warnings, "live schema unavailable, using the migrations only: "+lv.err.Error())
			return static, nil
		}
		merged := mergeModels(static, modelFromSnapshot(lv.snap))
		if lv.snap.Goose {
			applied := map[int64]bool{}
			for _, v := range lv.snap.Applied {
				applied[v] = true
			}
			for _, mg := range merged.migrations {
				ap := applied[mg.num]
				mg.applied = &ap
			}
		}
		return merged, nil
	})
}

type dbLayer struct{}

func (dbLayer) name() string   { return "db" }
func (dbLayer) deps() []string { return []string{"code"} }

// fingerprint covers the migration files and the live schema, which can change
// with no file changing.
func (dbLayer) fingerprint(w *world) (string, error) {
	sc, err := w.migrationScan()
	if err != nil {
		return "", err
	}
	live := "none"
	switch lv := w.live(); {
	case !lv.configured:
	case lv.err != nil:
		live = "unavailable"
	default:
		live = lv.fp
	}
	sum := sha256.Sum256([]byte(fingerprint(sc.files) + "|" + live))
	return hex.EncodeToString(sum[:])[:16], nil
}

func (dbLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	m, err := w.dbModel()
	if err != nil {
		return nil, err
	}
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}
	set := newQuadSet("db")
	warnings := append([]string(nil), m.warnings...)

	known := map[string]bool{}
	for _, mg := range m.migrations {
		known[mg.stem] = true
		id := migID(mg.stem)
		set.add(id, PredType, KindMigration)
		set.str(id, PredName, mg.stem)
		set.str(id, PredVersion, mg.version)
		set.str(id, PredPath, mg.path)
		if mg.applied != nil {
			set.flag(id, PredApplied, *mg.applied)
		}
	}
	introduced := func(subject quad.IRI, stem string) {
		if stem != "" && known[stem] {
			set.add(subject, PredIntroducedIn, migID(stem))
		}
	}

	for _, t := range m.tables {
		tid := tableID(t.name)
		set.add(tid, PredType, KindTable)
		set.str(tid, PredName, t.name)
		set.str(tid, PredSource, t.source)
		if t.engine != "" {
			set.str(tid, PredEngine, t.engine)
		}
		if t.comment != "" {
			set.str(tid, PredComment, t.comment)
		}
		introduced(tid, t.introduced)

		for _, c := range t.columns {
			cid := colID(t.name, c.name)
			set.add(tid, PredHasColumn, cid)
			set.add(cid, PredType, KindColumn)
			set.str(cid, PredName, c.name)
			set.str(cid, PredDataType, c.typ)
			set.flag(cid, PredNullable, c.nullable)
			if c.key != "" {
				set.str(cid, PredKey, c.key)
			}
			// A default is schema, but a sensitive column's is never recorded.
			if c.hasDef && !sensitiveRe.MatchString(c.name) {
				set.str(cid, PredDefault, c.def)
			}
			if c.extra != "" {
				set.str(cid, PredExtra, c.extra)
			}
			if c.comment != "" {
				set.str(cid, PredComment, c.comment)
			}
			set.num(cid, PredOrdinal, c.ordinal)
			set.str(cid, PredSource, c.source)
			if c.drift != "" {
				set.str(cid, PredDrift, c.drift)
			}
			introduced(cid, c.introduced)
		}

		for _, ix := range t.indexes {
			iid := idxID(t.name, ix.name)
			set.add(tid, PredHasIndex, iid)
			set.add(iid, PredType, KindIndex)
			set.str(iid, PredName, ix.name)
			set.flag(iid, PredUnique, ix.unique)
			set.flag(iid, PredPrimary, ix.primary)
			set.str(iid, PredColumns, strings.Join(ix.columns, ","))
			if ix.implicit {
				set.flag(iid, PredImplicit, true)
			}
			set.str(iid, PredSource, ix.source)
			if ix.drift != "" {
				set.str(iid, PredDrift, ix.drift)
			}
			introduced(iid, ix.introduced)
			for _, name := range ix.columns {
				if t.column(name) != nil {
					set.add(iid, PredHasColumn, colID(t.name, name))
				}
			}
		}

		fkDone := map[string]bool{}
		for _, f := range t.fks {
			ref := m.table(f.refTable)
			if ref == nil {
				warnings = append(warnings, fmt.Sprintf("table %s: a foreign key refers to %s, which is not in the schema; edge skipped", t.name, f.refTable))
				continue
			}
			set.add(tid, PredFKTo, tableID(ref.name))
			for i, name := range f.columns {
				col := t.column(name)
				if col == nil || i >= len(f.refColumns) || ref.column(f.refColumns[i]) == nil {
					continue
				}
				cid := colID(t.name, col.name)
				set.add(cid, PredFKTo, colID(ref.name, f.refColumns[i]))
				if fkDone[strings.ToLower(col.name)] {
					continue
				}
				fkDone[strings.ToLower(col.name)] = true
				if f.name != "" {
					set.str(cid, PredFKName, f.name)
				}
				if f.onDelete != "" {
					set.str(cid, PredOnDelete, f.onDelete)
				}
				if f.onUpdate != "" {
					set.str(cid, PredOnUpdate, f.onUpdate)
				}
				// A foreign key that is itself unique points at one row, and one row
				// points back: one to one. Otherwise many rows share a target.
				if len(f.columns) == 1 {
					card := "one_to_many"
					if t.uniqueOn(f.columns) {
						card = "one_to_one"
					}
					set.str(cid, PredCardinality, card)
				}
			}
		}
	}

	// What the schema implies but does not declare, each with an annotation that
	// says how it was inferred and how sure that is.
	for _, r := range inferRelations(m) {
		set.add(r.from, r.pred, r.to)
		aid := annID(r.from, r.pred, r.to)
		set.add(aid, PredType, KindAnnotation)
		set.add(aid, PredSubject, r.from)
		set.str(aid, PredPredicate, string(r.pred))
		set.add(aid, PredObject, r.to)
		set.str(aid, PredMethod, r.method)
		set.num(aid, PredConfidence, r.confidence)
		if r.via != "" {
			set.add(aid, PredVia, r.via)
		}
	}

	// Code to schema: the tables the SQL in a function names, and the struct
	// types whose db tags are a table's columns.
	for _, st := range ci.sql {
		reads, writes := scanSQLTables(st.text)
		for _, name := range reads {
			if t := m.table(name); t != nil {
				set.add(st.fn, PredReadsTable, tableID(t.name))
			}
		}
		for _, name := range writes {
			if t := m.table(name); t != nil {
				set.add(st.fn, PredWritesTable, tableID(t.name))
			}
		}
	}
	for _, ty := range ci.types {
		if t := mapTypeToTable(m, ty); t != nil {
			set.add(ty.id, PredMapsTo, tableID(t.name))
		}
	}
	return &layerOutput{quads: set.quads, warnings: warnings}, nil
}

// mapTypeToTable finds the table a struct is a row of, from its `db:"col"` tags:
// the one table that has every tagged column, or, when several do, the one the
// type's name points at (Item -> items, ItemTag -> item_tags). It says
// nothing rather than guess.
func mapTypeToTable(m *dbModel, ty *typeInfo) *dbTable {
	var cols []string
	for _, f := range ty.fields {
		name := strings.Split(f.tag.Get("db"), ",")[0]
		if name != "" && name != "-" {
			cols = append(cols, name)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	var candidates []*dbTable
	for _, t := range m.tables {
		all := true
		for _, c := range cols {
			if t.column(c) == nil {
				all = false
				break
			}
		}
		if all {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	var named []*dbTable
	snake := snakeCase(ty.name)
	for _, t := range candidates {
		if tableNameMatches(snake, t.name) {
			named = append(named, t)
		}
	}
	if len(named) == 1 {
		return named[0]
	}
	return nil
}

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z])([A-Z][a-z])`)

// snakeCase turns OrderItem into order_item.
func snakeCase(s string) string {
	return strings.ToLower(camelBoundary.ReplaceAllString(s, "${1}${3}_${2}${4}"))
}
