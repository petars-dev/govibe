package codegraph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cayleygraph/quad"

	"petars-dev/govibe/storage/dbexplorer"
)

// Where a table, column or index comes from. With no live database everything is
// "migration". With one, an element only the migrations describe was never
// applied (or was dropped by hand), and one only the database has was made
// outside the migrations: that is drift.
const (
	srcMigration = "migration"
	srcLive      = "live"
	srcBoth      = "both"
)

// dbModel is the schema the db layer writes: the migrations' schema, merged with
// the live database's when there is one.
type dbModel struct {
	tables     []*dbTable // by lower-case name
	byKey      map[string]*dbTable
	migrations []*dbMigration
	live       bool // a live snapshot was merged in
	warnings   []string
}

type dbMigration struct {
	stem    string // 0002_add_index
	version string // 0002
	num     int64
	path    string
	applied *bool // nil when there is no live database to ask
}

type dbTable struct {
	name       string
	engine     string
	comment    string
	source     string
	introduced string // migration stem, "" if not from a migration
	columns    []*dbColumn
	indexes    []*dbIndex
	fks        []*dbFK
}

type dbColumn struct {
	name       string
	typ        string
	nullable   bool
	key        string // PRI, UNI, MUL or ""
	def        string
	hasDef     bool
	extra      string
	comment    string
	source     string
	introduced string
	drift      string // how migration and live disagree about it, if they do
	ordinal    int    // 1-based position in the table
}

type dbIndex struct {
	name       string
	unique     bool
	primary    bool
	implicit   bool // added by the server for a foreign key
	columns    []string
	source     string
	introduced string
	drift      string
}

type dbFK struct {
	name       string // "" when the migration left it unnamed and there is no live name
	columns    []string
	refTable   string
	refColumns []string
	onUpdate   string
	onDelete   string
	source     string
}

func (m *dbModel) table(name string) *dbTable { return m.byKey[strings.ToLower(name)] }

func (t *dbTable) column(name string) *dbColumn {
	for _, c := range t.columns {
		if strings.EqualFold(c.name, name) {
			return c
		}
	}
	return nil
}

func (t *dbTable) primary() *dbIndex {
	for _, ix := range t.indexes {
		if ix.primary {
			return ix
		}
	}
	return nil
}

// leadingIndexed reports whether col is the first column of some index.
func (t *dbTable) leadingIndexed(col string) bool {
	for _, ix := range t.indexes {
		if len(ix.columns) > 0 && strings.EqualFold(ix.columns[0], col) {
			return true
		}
	}
	return false
}

// uniqueOn reports whether a primary key or unique index consists of exactly cols.
func (t *dbTable) uniqueOn(cols []string) bool {
	for _, ix := range t.indexes {
		if ix.unique && sameColumnSet(ix.columns, cols) {
			return true
		}
	}
	return false
}

func sameColumnSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, x := range a {
		set[strings.ToLower(x)] = true
	}
	for _, y := range b {
		if !set[strings.ToLower(y)] {
			return false
		}
	}
	return true
}

func (m *dbModel) add(t *dbTable) {
	m.tables = append(m.tables, t)
	m.byKey[strings.ToLower(t.name)] = t
}

func (m *dbModel) sort() {
	sort.Slice(m.tables, func(i, j int) bool { return strings.ToLower(m.tables[i].name) < strings.ToLower(m.tables[j].name) })
}

func newDBModel() *dbModel { return &dbModel{byKey: map[string]*dbTable{}} }

// modelFromSQL turns the schema the migrations leave behind into a model.
func modelFromSQL(sch *sqlSchema) *dbModel {
	m := newDBModel()
	for _, st := range sch.tables {
		t := &dbTable{name: st.name, engine: st.engine, comment: st.comment, source: srcMigration, introduced: st.introduced}
		for i, c := range st.columns {
			t.columns = append(t.columns, &dbColumn{
				name: c.name, typ: c.typ, nullable: c.nullable, def: c.def, hasDef: c.hasDefault, extra: c.extra,
				comment: c.comment, source: srcMigration, introduced: c.introduced, ordinal: i + 1,
			})
		}
		for _, ix := range st.indexes {
			t.indexes = append(t.indexes, &dbIndex{
				name: ix.name, unique: ix.unique, primary: ix.primary, implicit: ix.implicit,
				columns: append([]string(nil), ix.columns...), source: srcMigration, introduced: ix.introduced,
			})
		}
		for _, fk := range st.fks {
			f := &dbFK{
				columns: append([]string(nil), fk.columns...), refTable: fk.refTable,
				refColumns: append([]string(nil), fk.refColumns...), onUpdate: fk.onUpdate, onDelete: fk.onDelete, source: srcMigration,
			}
			if !fk.autoNamed {
				f.name = fk.name
			}
			t.fks = append(t.fks, f)
		}
		computeKeys(t)
		m.add(t)
	}
	m.sort()
	return m
}

// computeKeys sets each column's Key the way MySQL reports it: PRI for a primary
// key column, UNI for the only column of a unique index, MUL for the first column
// of any other index.
func computeKeys(t *dbTable) {
	for _, c := range t.columns {
		c.key = ""
		for _, ix := range t.indexes {
			if len(ix.columns) == 0 {
				continue
			}
			switch {
			case ix.primary && containsFold(ix.columns, c.name):
				c.key = "PRI"
			case c.key != "PRI" && ix.unique && len(ix.columns) == 1 && strings.EqualFold(ix.columns[0], c.name):
				c.key = "UNI"
			case c.key == "" && strings.EqualFold(ix.columns[0], c.name):
				c.key = "MUL"
			}
		}
	}
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// modelFromSnapshot turns the live database's schema into a model.
func modelFromSnapshot(snap *dbexplorer.Snapshot) *dbModel {
	m := newDBModel()
	m.live = true
	for _, st := range snap.Tables {
		t := &dbTable{name: st.Name, engine: st.Engine, comment: st.Comment, source: srcLive}
		for i, c := range st.Columns {
			col := &dbColumn{
				name: c.Name, typ: strings.ToLower(c.Type), nullable: c.Nullable, key: c.Key, extra: c.Extra,
				comment: c.Comment, source: srcLive, ordinal: i + 1,
			}
			// MariaDB reports a nullable column with no default as the text NULL.
			if c.Default != nil && !strings.EqualFold(*c.Default, "NULL") {
				col.def, col.hasDef = *c.Default, true
			}
			t.columns = append(t.columns, col)
		}
		for _, ix := range st.Indexes {
			t.indexes = append(t.indexes, &dbIndex{
				name: ix.Name, unique: ix.Unique, primary: ix.Name == "PRIMARY",
				columns: append([]string(nil), ix.Columns...), source: srcLive,
			})
		}
		for _, fk := range st.ForeignKeys {
			t.fks = append(t.fks, &dbFK{
				name: fk.Name, columns: append([]string(nil), fk.Columns...), refTable: fk.RefTable,
				refColumns: append([]string(nil), fk.RefColumns...), onUpdate: fk.OnUpdate, onDelete: fk.OnDelete, source: srcLive,
			})
		}
		m.add(t)
	}
	m.sort()
	return m
}

var intWidth = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint)\(\d+\)`)

// normType reduces a column type to a form two spellings of the same type share,
// for comparing what a migration says with what the server reports: lower case,
// no display widths on integers (int(11) is int; tinyint(1) is how bool is
// spelled and stays), no space after commas, decimal with its defaults spelled out.
func normType(t string) string {
	t = strings.ToLower(strings.Join(strings.Fields(t), " "))
	t = strings.ReplaceAll(t, ", ", ",")
	t = strings.TrimSuffix(strings.ReplaceAll(t, " signed", ""), " zerofill")
	if rest, ok := strings.CutPrefix(t, "integer"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '(') {
		t = "int" + rest
	}
	if loc := intWidth.FindStringSubmatchIndex(t); loc != nil && !strings.HasPrefix(t, "tinyint(1)") {
		t = t[loc[2]:loc[3]] + t[loc[1]:]
	}
	switch {
	case t == "bool" || t == "boolean":
		t = "tinyint(1)"
	case t == "decimal" || t == "numeric" || t == "dec":
		t = "decimal(10,0)"
	case strings.HasPrefix(t, "numeric("):
		t = "decimal" + strings.TrimPrefix(t, "numeric")
	}
	if strings.HasPrefix(t, "decimal(") && !strings.Contains(t, ",") {
		t = strings.Replace(t, ")", ",0)", 1)
	}
	return t
}

// mergeModels combines the migrations' schema with the live database's. The live
// database is the truth about what exists, so its attributes win; what the
// migrations add is where each thing was introduced. Where they disagree the
// element says so (source, and drift for a column or index that exists on both
// sides but differs).
func mergeModels(st, lv *dbModel) *dbModel {
	m := newDBModel()
	m.live = true
	m.migrations = st.migrations
	m.warnings = append(m.warnings, st.warnings...)
	keys := map[string]bool{}
	for k := range st.byKey {
		keys[k] = true
	}
	for k := range lv.byKey {
		keys[k] = true
	}
	for k := range keys {
		s, l := st.byKey[k], lv.byKey[k]
		switch {
		case s != nil && l != nil:
			m.add(mergeTable(s, l))
		case l != nil:
			m.add(l)
		default:
			// Only the migrations know it. The server's own indexes for foreign
			// keys are emulated, and a live database that has no such table has
			// no such index either.
			m.add(s)
		}
	}
	m.sort()
	return m
}

func mergeTable(s, l *dbTable) *dbTable {
	t := &dbTable{name: l.name, engine: l.engine, comment: l.comment, source: srcBoth, introduced: s.introduced}

	for _, lc := range l.columns {
		c := *lc
		c.source = srcBoth
		if sc := s.column(lc.name); sc != nil {
			c.introduced = sc.introduced
			var diffs []string
			if normType(sc.typ) != normType(lc.typ) {
				diffs = append(diffs, fmt.Sprintf("data_type: migration %s, live %s", sc.typ, lc.typ))
			}
			if sc.nullable != lc.nullable {
				diffs = append(diffs, fmt.Sprintf("nullable: migration %t, live %t", sc.nullable, lc.nullable))
			}
			c.drift = strings.Join(diffs, "; ")
		} else {
			c.source = srcLive
		}
		t.columns = append(t.columns, &c)
	}
	for _, sc := range s.columns {
		if l.column(sc.name) == nil {
			c := *sc
			c.source = srcMigration
			c.ordinal = len(t.columns) + 1
			t.columns = append(t.columns, &c)
		}
	}

	liveIdx := map[string]*dbIndex{}
	for _, li := range l.indexes {
		liveIdx[strings.ToLower(li.name)] = li
	}
	for _, li := range l.indexes {
		ix := *li
		ix.source = srcBoth
		if si := indexByName(s, li.name); si != nil {
			ix.introduced = si.introduced
			ix.implicit = si.implicit
			var diffs []string
			if !equalFoldSlices(si.columns, li.columns) {
				diffs = append(diffs, fmt.Sprintf("columns: migration %s, live %s", strings.Join(si.columns, ","), strings.Join(li.columns, ",")))
			}
			if si.unique != li.unique {
				diffs = append(diffs, fmt.Sprintf("unique: migration %t, live %t", si.unique, li.unique))
			}
			ix.drift = strings.Join(diffs, "; ")
		} else {
			ix.source = srcLive
		}
		t.indexes = append(t.indexes, &ix)
	}
	for _, si := range s.indexes {
		if liveIdx[strings.ToLower(si.name)] != nil || si.implicit {
			continue // present on both sides, or an emulated server index the live database lacks
		}
		ix := *si
		ix.source = srcMigration
		t.indexes = append(t.indexes, &ix)
	}

	staticFKs := map[string]*dbFK{}
	for _, f := range s.fks {
		staticFKs[fkKey(f)] = f
	}
	seen := map[string]bool{}
	for _, lf := range l.fks {
		f := *lf
		f.source = srcBoth
		if staticFKs[fkKey(lf)] == nil {
			f.source = srcLive
		}
		seen[fkKey(lf)] = true
		t.fks = append(t.fks, &f)
	}
	for _, sf := range s.fks {
		if !seen[fkKey(sf)] {
			f := *sf
			f.source = srcMigration
			t.fks = append(t.fks, &f)
		}
	}
	// A foreign key only one side has is drift on its columns.
	for _, f := range t.fks {
		if f.source == srcBoth {
			continue
		}
		for _, name := range f.columns {
			if c := t.column(name); c != nil {
				c.drift = strings.TrimPrefix(c.drift+"; foreign key to "+f.refTable+" only in "+f.source, "; ")
			}
		}
	}
	return t
}

func indexByName(t *dbTable, name string) *dbIndex {
	for _, ix := range t.indexes {
		if strings.EqualFold(ix.name, name) {
			return ix
		}
	}
	return nil
}

func equalFoldSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func fkKey(f *dbFK) string {
	return strings.ToLower(strings.Join(f.columns, ",") + ">" + f.refTable + "(" + strings.Join(f.refColumns, ",") + ")")
}

// dbRel is an edge the layer infers between schema elements, with how and how
// sure.
type dbRel struct {
	pred       quad.IRI
	from, to   quad.IRI
	via        quad.IRI // the junction table, for many_to_many
	method     string
	confidence int
}

// Inference methods, as recorded on the annotation of each inferred edge.
const (
	methodNameMatch      = "name_match"      // a column named <x>_id next to a table called x
	methodJunctionPK     = "junction_pk"     // two foreign keys that together are the primary key
	methodJunctionUnique = "junction_unique" // two foreign keys that together are a unique key
)

var idSuffix = regexp.MustCompile(`(?i)^(.+)_id$`)

// implicitBase returns x for a column named x_id (or xId), else "".
func implicitBase(col string) string {
	if m := idSuffix.FindStringSubmatch(col); m != nil {
		return strings.ToLower(m[1])
	}
	if len(col) > 2 && strings.HasSuffix(col, "Id") {
		prev := col[len(col)-3]
		if prev >= 'a' && prev <= 'z' {
			return strings.ToLower(col[:len(col)-2])
		}
	}
	return ""
}

// tableNameMatches reports whether a table is called base, or its plural.
func tableNameMatches(base, table string) bool {
	table = strings.ToLower(table)
	if table == base || table == base+"s" || table == base+"es" {
		return true
	}
	return strings.HasSuffix(base, "y") && len(base) > 1 && table == base[:len(base)-1]+"ies"
}

func intFamilyType(t string) bool {
	t = strings.TrimSuffix(t, " unsigned")
	return t == "tinyint" || t == "smallint" || t == "mediumint" || t == "int" || t == "bigint"
}

// inferRelations derives the edges the schema implies but does not declare:
// implicit_ref for an x_id column with no foreign key next to a table called x,
// and many_to_many for a junction table.
func inferRelations(m *dbModel) []dbRel {
	var rels []dbRel

	for _, t := range m.tables {
		inFK := map[string]bool{}
		for _, f := range t.fks {
			for _, c := range f.columns {
				inFK[strings.ToLower(c)] = true
			}
		}
		for _, c := range t.columns {
			if inFK[strings.ToLower(c.name)] {
				continue
			}
			base := implicitBase(c.name)
			if base == "" {
				continue
			}
			var best *dbRel
			var bestTable *dbTable
			for _, r := range m.tables {
				if r == t || !tableNameMatches(base, r.name) {
					continue
				}
				pk := r.primary()
				if pk == nil || len(pk.columns) != 1 {
					continue
				}
				pc := r.column(pk.columns[0])
				if pc == nil {
					continue
				}
				conf := 70 // the name matches
				switch a, b := normType(c.typ), normType(pc.typ); {
				case a == b:
					conf += 20
				case intFamilyType(a) && intFamilyType(b):
					conf += 10
				default:
					conf -= 30
				}
				if strings.EqualFold(pc.name, "id") {
					conf += 5
				}
				if t.leadingIndexed(c.name) {
					conf += 5
				}
				if conf > 95 {
					conf = 95
				}
				if conf < 50 {
					continue
				}
				cand := dbRel{pred: PredImplicitRef, from: colID(t.name, c.name), to: colID(r.name, pc.name), method: methodNameMatch, confidence: conf}
				if best == nil || cand.confidence > best.confidence || (cand.confidence == best.confidence && cand.to < best.to) {
					cand := cand
					best, bestTable = &cand, r
				}
			}
			if best != nil {
				rels = append(rels, *best,
					dbRel{pred: PredImplicitRef, from: tableID(t.name), to: tableID(bestTable.name), method: best.method, confidence: best.confidence})
			}
		}
	}

	for _, j := range m.tables {
		var single []*dbFK
		for _, f := range j.fks {
			if len(f.columns) == 1 && m.table(f.refTable) != nil {
				single = append(single, f)
			}
		}
		pk := j.primary()
		for a := 0; a < len(single); a++ {
			for b := a + 1; b < len(single); b++ {
				fa, fb := single[a], single[b]
				if strings.EqualFold(fa.refTable, fb.refTable) || strings.EqualFold(fa.refTable, j.name) || strings.EqualFold(fb.refTable, j.name) {
					continue
				}
				cols := []string{fa.columns[0], fb.columns[0]}
				method, conf := "", 0
				switch {
				case pk != nil && sameColumnSet(pk.columns, cols):
					method, conf = methodJunctionPK, 90
				case j.uniqueOn(cols):
					method, conf = methodJunctionUnique, 70
				default:
					continue
				}
				ta, tb := m.table(fa.refTable), m.table(fb.refTable)
				via := tableID(j.name)
				rels = append(rels,
					dbRel{pred: PredManyToMany, from: tableID(ta.name), to: tableID(tb.name), via: via, method: method, confidence: conf},
					dbRel{pred: PredManyToMany, from: tableID(tb.name), to: tableID(ta.name), via: via, method: method, confidence: conf})
			}
		}
	}

	sort.Slice(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.pred != b.pred {
			return a.pred < b.pred
		}
		if a.from != b.from {
			return a.from < b.from
		}
		if a.to != b.to {
			return a.to < b.to
		}
		return a.via < b.via
	})
	return rels
}
