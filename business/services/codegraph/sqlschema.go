package codegraph

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// This file is the static half of the db layer: a best-effort reader for the
// MySQL/MariaDB DDL of a project's goose migrations. It tokenizes (so strings,
// comments and quoted identifiers cannot confuse it) and understands CREATE,
// ALTER and DROP TABLE, CREATE and DROP INDEX, RENAME TABLE, and the column,
// key and foreign-key syntax inside them. It applies the migrations in order to
// build the schema they leave behind, remembering which migration introduced
// each table, column and index, and it follows MySQL's own naming rules for
// what a migration leaves unnamed (an unnamed unique key is named after its
// first column, an unnamed foreign key `<table>_ibfk_<n>`, and the index InnoDB
// adds for a foreign key), so the result can be compared with a live database.
//
// Anything else (INSERT, triggers, views, procedures) is ignored. DDL it
// recognises but cannot read is reported as a warning rather than guessed at.

// sqlTok is one token of SQL.
type sqlTok struct {
	kind byte // 'w' word, 'i' quoted identifier, 's' string, 'n' number, 'p' punctuation
	text string
	pos  int // byte offset of the token's first character
	end  int // and just past its last
}

func (t sqlTok) is(kw string) bool { return t.kind == 'w' && strings.EqualFold(t.text, kw) }
func (t sqlTok) isP(c string) bool { return t.kind == 'p' && t.text == c }

// lexSQL splits SQL into tokens, dropping whitespace and comments. MySQL's
// conditional comments (/*!40101 ... */) hold statements that do run, so their
// markers are dropped and their content kept.
func lexSQL(src string) []sqlTok {
	var out []sqlTok
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#' || (c == '-' && i+1 < n && src[i+1] == '-' && (i+2 >= n || src[i+2] == ' ' || src[i+2] == '\t' || src[i+2] == '\n' || src[i+2] == '\r')):
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			if i+2 < n && src[i+2] == '!' {
				i += 3
				for i < n && src[i] >= '0' && src[i] <= '9' {
					i++
				}
				break
			}
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '*' && i+1 < n && src[i+1] == '/':
			i += 2 // the close of a conditional comment
		case c == '\'' || c == '"':
			start := i
			i++
			var b strings.Builder
			for i < n {
				if src[i] == '\\' && i+1 < n {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				if src[i] == c {
					if i+1 < n && src[i+1] == c { // a doubled quote
						b.WriteByte(c)
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(src[i])
				i++
			}
			out = append(out, sqlTok{kind: 's', text: b.String(), pos: start, end: i})
		case c == '`':
			start := i
			i++
			var b strings.Builder
			for i < n {
				if src[i] == '`' {
					if i+1 < n && src[i+1] == '`' {
						b.WriteByte('`')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(src[i])
				i++
			}
			out = append(out, sqlTok{kind: 'i', text: b.String(), pos: start, end: i})
		case c >= '0' && c <= '9':
			start := i
			for i < n && (src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			out = append(out, sqlTok{kind: 'n', text: src[start:i], pos: start, end: i})
		case c == '_' || c == '$' || c >= 0x80 || unicode.IsLetter(rune(c)):
			start := i
			for i < n && (src[i] == '_' || src[i] == '$' || src[i] >= 0x80 || (src[i] >= 'a' && src[i] <= 'z') || (src[i] >= 'A' && src[i] <= 'Z') || (src[i] >= '0' && src[i] <= '9')) {
				i++
			}
			out = append(out, sqlTok{kind: 'w', text: src[start:i], pos: start, end: i})
		default:
			out = append(out, sqlTok{kind: 'p', text: string(c), pos: i, end: i + 1})
			i++
		}
	}
	return out
}

// sqlStmt is one statement: its tokens, and its text as written.
type sqlStmt struct {
	toks []sqlTok
	text string
}

// splitStmts splits tokens at semicolons outside parentheses.
func splitStmts(src string, toks []sqlTok) []sqlStmt {
	var out []sqlStmt
	depth, start := 0, 0
	emit := func(end int) {
		if end > start {
			part := toks[start:end]
			out = append(out, sqlStmt{toks: part, text: strings.TrimSpace(src[part[0].pos:part[len(part)-1].end])})
		}
	}
	for i, t := range toks {
		switch {
		case t.isP("("):
			depth++
		case t.isP(")"):
			if depth > 0 {
				depth--
			}
		case t.isP(";") && depth == 0:
			emit(i)
			start = i + 1
		}
	}
	emit(len(toks))
	return out
}

var gooseDirective = regexp.MustCompile(`(?i)^\s*--\s*\+goose\s+(\w+)`)

// gooseUp returns the Up section of a goose SQL migration: the text after
// `-- +goose Up` and before `-- +goose Down`, without the stored-program blocks
// between StatementBegin and StatementEnd. A file with no directives at all is
// taken to be all Up. The second result counts the stored-program blocks
// skipped.
func gooseUp(src string) (string, int) {
	lines := strings.Split(src, "\n")
	hasDirective := false
	for _, l := range lines {
		if m := gooseDirective.FindStringSubmatch(l); m != nil {
			hasDirective = true
			break
		}
	}
	if !hasDirective {
		return src, 0
	}
	var out []string
	up, inBlock, skipped := false, false, 0
	for _, l := range lines {
		if m := gooseDirective.FindStringSubmatch(l); m != nil {
			switch strings.ToLower(m[1]) {
			case "up":
				up = true
			case "down":
				up = false
			case "statementbegin":
				if up {
					inBlock = true
					skipped++
				}
			case "statementend":
				inBlock = false
			}
			continue
		}
		if up && !inBlock {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n"), skipped
}

// sqlSchema is a schema built by applying DDL statements in order.
type sqlSchema struct {
	tables   map[string]*sqlTable // by lower-case name
	warnings []string
}

type sqlTable struct {
	name       string
	engine     string
	comment    string
	columns    []*sqlColumn
	indexes    []*sqlIndex
	fks        []*sqlFK
	introduced string // the migration that created it
}

type sqlColumn struct {
	name       string
	typ        string
	nullable   bool
	hasDefault bool
	def        string
	extra      string
	comment    string
	introduced string
}

type sqlIndex struct {
	name       string
	unique     bool
	primary    bool
	implicit   bool // created by the server for a foreign key, not by the DDL
	columns    []string
	introduced string
}

type sqlFK struct {
	name       string
	columns    []string
	refTable   string
	refColumns []string
	onUpdate   string
	onDelete   string
	introduced string
	autoNamed  bool
	indexHint  string // what to name the index the server adds for it: the constraint symbol, else the FK index name
}

func newSQLSchema() *sqlSchema { return &sqlSchema{tables: map[string]*sqlTable{}} }

func (s *sqlSchema) warn(mig, format string, args ...any) {
	s.warnings = append(s.warnings, mig+": "+fmt.Sprintf(format, args...))
}

func (t *sqlTable) column(name string) *sqlColumn {
	for _, c := range t.columns {
		if strings.EqualFold(c.name, name) {
			return c
		}
	}
	return nil
}

func (t *sqlTable) index(name string) *sqlIndex {
	for _, ix := range t.indexes {
		if strings.EqualFold(ix.name, name) && !ix.primary {
			return ix
		}
	}
	return nil
}

func (t *sqlTable) primary() *sqlIndex {
	for _, ix := range t.indexes {
		if ix.primary {
			return ix
		}
	}
	return nil
}

// freeIndexName returns base, or base_2, base_3, ... the first one no index of
// the table uses: how MySQL names an index the DDL did not name.
func (t *sqlTable) freeIndexName(base string) string {
	name := base
	for n := 2; t.index(name) != nil || strings.EqualFold(name, "PRIMARY"); n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	return name
}

// indexed reports whether cols are the leftmost columns of some index.
func (t *sqlTable) indexed(cols []string) bool {
	for _, ix := range t.indexes {
		if len(ix.columns) < len(cols) {
			continue
		}
		match := true
		for i, c := range cols {
			if !strings.EqualFold(ix.columns[i], c) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// sp is a cursor over one statement's tokens.
type sp struct {
	t []sqlTok
	i int
}

func (p *sp) eof() bool { return p.i >= len(p.t) }
func (p *sp) peek() sqlTok {
	if p.eof() {
		return sqlTok{}
	}
	return p.t[p.i]
}
func (p *sp) next() sqlTok {
	t := p.peek()
	if !p.eof() {
		p.i++
	}
	return t
}

// accept consumes the keywords in order, or nothing at all.
func (p *sp) accept(kws ...string) bool {
	if p.i+len(kws) > len(p.t) {
		return false
	}
	for j, kw := range kws {
		if !p.t[p.i+j].is(kw) {
			return false
		}
	}
	p.i += len(kws)
	return true
}

func (p *sp) isKw(kw string) bool { return p.peek().is(kw) }
func (p *sp) isP(c string) bool   { return p.peek().isP(c) }
func (p *sp) acceptP(c string) bool { // consumes punctuation c
	if p.isP(c) {
		p.i++
		return true
	}
	return false
}

// ident reads a word or quoted identifier.
func (p *sp) ident() (string, bool) {
	t := p.peek()
	if t.kind == 'w' || t.kind == 'i' {
		p.i++
		return t.text, true
	}
	return "", false
}

// tableName reads [schema.]name and returns name.
func (p *sp) tableName() (string, bool) {
	name, ok := p.ident()
	if !ok {
		return "", false
	}
	for p.isP(".") {
		p.i++
		n, ok := p.ident()
		if !ok {
			return "", false
		}
		name = n
	}
	return name, true
}

// balanced consumes a parenthesised group and returns the tokens inside it.
func (p *sp) balanced() ([]sqlTok, bool) {
	if !p.isP("(") {
		return nil, false
	}
	depth, start := 0, p.i+1
	for j := p.i; j < len(p.t); j++ {
		switch {
		case p.t[j].isP("("):
			depth++
		case p.t[j].isP(")"):
			depth--
			if depth == 0 {
				inner := p.t[start:j]
				p.i = j + 1
				return inner, true
			}
		}
	}
	p.i = len(p.t)
	return nil, false
}

// splitTop splits tokens at commas outside parentheses.
func splitTop(toks []sqlTok) [][]sqlTok {
	var out [][]sqlTok
	depth, start := 0, 0
	for i, t := range toks {
		switch {
		case t.isP("("):
			depth++
		case t.isP(")"):
			depth--
		case t.isP(",") && depth == 0:
			out = append(out, toks[start:i])
			start = i + 1
		}
	}
	if start < len(toks) {
		out = append(out, toks[start:])
	}
	return out
}

// columnList reads `(a, b(10), c DESC)` as the plain column names; a part that
// is not a plain column (a functional key part) is left out.
func (p *sp) columnList() []string {
	inner, ok := p.balanced()
	if !ok {
		return nil
	}
	var cols []string
	for _, part := range splitTop(inner) {
		if len(part) > 0 && (part[0].kind == 'w' || part[0].kind == 'i') {
			cols = append(cols, part[0].text)
		}
	}
	return cols
}

// apply runs one statement against the schema. mig names the migration it
// comes from. Statements that are not table DDL are ignored; table DDL it
// cannot read is reported as a warning.
func (s *sqlSchema) apply(st sqlStmt, mig string) {
	p := &sp{t: st.toks}
	switch {
	case p.accept("CREATE", "TEMPORARY", "TABLE"), p.accept("CREATE", "TABLE"):
		if err := s.createTable(p, mig); err != nil {
			s.warn(mig, "%v: %s", err, abbreviate(st.text))
		}
	case p.accept("ALTER", "IGNORE", "TABLE"), p.accept("ALTER", "TABLE"):
		if err := s.alterTable(p, mig); err != nil {
			s.warn(mig, "%v: %s", err, abbreviate(st.text))
		}
	case p.accept("DROP", "TEMPORARY", "TABLE"), p.accept("DROP", "TABLE"):
		s.dropTable(p)
	case p.accept("RENAME", "TABLE"):
		s.renameTables(p, mig)
	case p.accept("CREATE", "UNIQUE", "INDEX"), p.accept("CREATE", "FULLTEXT", "INDEX"), p.accept("CREATE", "SPATIAL", "INDEX"), p.accept("CREATE", "INDEX"):
		unique := st.toks[1].is("UNIQUE")
		if err := s.createIndex(p, unique, mig); err != nil {
			s.warn(mig, "%v: %s", err, abbreviate(st.text))
		}
	case p.accept("DROP", "INDEX"):
		if err := s.dropIndexOn(p); err != nil {
			s.warn(mig, "%v: %s", err, abbreviate(st.text))
		}
	}
}

func abbreviate(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}

func (s *sqlSchema) createTable(p *sp, mig string) error {
	ifNotExists := p.accept("IF", "NOT", "EXISTS")
	name, ok := p.tableName()
	if !ok {
		return fmt.Errorf("cannot read the table name")
	}
	if ifNotExists && s.tables[strings.ToLower(name)] != nil {
		return nil // the server does nothing
	}
	t := &sqlTable{name: name, introduced: mig}
	if p.accept("LIKE") {
		src, _ := p.tableName()
		from := s.tables[strings.ToLower(src)]
		if from == nil {
			return fmt.Errorf("CREATE TABLE LIKE an unknown table")
		}
		t = cloneTable(from, name, mig)
		s.tables[strings.ToLower(name)] = t
		return nil
	}
	body, ok := p.balanced()
	if !ok {
		return fmt.Errorf("unsupported CREATE TABLE form (no column list)")
	}
	for _, item := range splitTop(body) {
		if err := s.tableItem(t, &sp{t: item}, mig); err != nil {
			return err
		}
	}
	for !p.eof() {
		switch {
		case p.accept("ENGINE"):
			p.acceptP("=")
			if v, ok := p.ident(); ok {
				t.engine = v
			}
		case p.accept("COMMENT"):
			p.acceptP("=")
			if v := p.next(); v.kind == 's' {
				t.comment = v.text
			}
		default:
			p.next()
		}
	}
	finishTable(t, mig)
	s.tables[strings.ToLower(name)] = t
	return nil
}

func cloneTable(from *sqlTable, name, mig string) *sqlTable {
	t := &sqlTable{name: name, engine: from.engine, comment: from.comment, introduced: mig}
	for _, c := range from.columns {
		cc := *c
		cc.introduced = mig
		t.columns = append(t.columns, &cc)
	}
	for _, ix := range from.indexes {
		ii := *ix
		ii.columns = append([]string(nil), ix.columns...)
		ii.introduced = mig
		t.indexes = append(t.indexes, &ii)
	}
	return t
}

// finishTable adds what the server adds on its own once a whole statement is
// processed: NOT NULL for primary key columns, and an index for every foreign key
// that no declared index covers (named after the constraint symbol, else the
// foreign key's index name, else its first column).
func finishTable(t *sqlTable, mig string) {
	if pk := t.primary(); pk != nil {
		for _, c := range pk.columns {
			if col := t.column(c); col != nil {
				col.nullable = false
			}
		}
	}
	for _, fk := range t.fks {
		if len(fk.columns) == 0 || t.indexed(fk.columns) {
			continue
		}
		name := fk.indexHint
		if name == "" {
			name = fk.columns[0]
		}
		t.indexes = append(t.indexes, &sqlIndex{name: t.freeIndexName(name), implicit: true, columns: append([]string(nil), fk.columns...), introduced: fk.introduced})
	}
}

// tableItem reads one entry of a CREATE TABLE column list: a column, or a key.
func (s *sqlSchema) tableItem(t *sqlTable, p *sp, mig string) error {
	symbol := ""
	hadConstraint := false
	if p.accept("CONSTRAINT") {
		hadConstraint = true
		if !p.isKw("PRIMARY") && !p.isKw("UNIQUE") && !p.isKw("FOREIGN") && !p.isKw("CHECK") {
			symbol, _ = p.ident()
		}
	}
	switch {
	case p.accept("PRIMARY", "KEY"):
		return s.addPrimary(t, p, mig)
	case p.accept("UNIQUE", "INDEX"), p.accept("UNIQUE", "KEY"), p.accept("UNIQUE"):
		return s.addKey(t, p, true, symbol, mig)
	case p.accept("FOREIGN", "KEY"):
		return s.addForeignKey(t, p, symbol, mig)
	case p.isKw("CHECK"), p.isKw("PERIOD"):
		return nil
	case hadConstraint:
		return fmt.Errorf("unsupported constraint")
	case p.accept("FULLTEXT", "INDEX"), p.accept("FULLTEXT", "KEY"), p.accept("FULLTEXT"),
		p.accept("SPATIAL", "INDEX"), p.accept("SPATIAL", "KEY"), p.accept("SPATIAL"),
		p.accept("INDEX"), p.accept("KEY"):
		return s.addKey(t, p, false, "", mig)
	}
	col, extras, err := parseColumn(p, mig)
	if err != nil {
		return err
	}
	t.columns = append(t.columns, col)
	return s.applyColumnExtras(t, col, extras, mig)
}

// columnExtras are the key-like clauses written on a column.
type columnExtras struct {
	primary bool
	unique  bool
	fk      *sqlFK
}

func (s *sqlSchema) applyColumnExtras(t *sqlTable, col *sqlColumn, e columnExtras, mig string) error {
	if e.primary {
		t.indexes = append([]*sqlIndex{{name: "PRIMARY", unique: true, primary: true, columns: []string{col.name}, introduced: mig}}, t.indexes...)
		col.nullable = false
	}
	if e.unique {
		t.indexes = append(t.indexes, &sqlIndex{name: t.freeIndexName(col.name), unique: true, columns: []string{col.name}, introduced: mig})
	}
	if e.fk != nil {
		e.fk.columns = []string{col.name}
		s.attachFK(t, e.fk, "", "", mig)
	}
	return nil
}

func (s *sqlSchema) addPrimary(t *sqlTable, p *sp, mig string) error {
	cols := p.columnList()
	if len(cols) == 0 {
		return fmt.Errorf("cannot read the primary key columns")
	}
	pk := &sqlIndex{name: "PRIMARY", unique: true, primary: true, columns: cols, introduced: mig}
	// A new primary key replaces any earlier one, and goes first.
	rest := t.indexes[:0:0]
	for _, ix := range t.indexes {
		if !ix.primary {
			rest = append(rest, ix)
		}
	}
	t.indexes = append([]*sqlIndex{pk}, rest...)
	for _, c := range cols {
		if col := t.column(c); col != nil {
			col.nullable = false
		}
	}
	return nil
}

// addKey reads `[name] [USING x] (cols)` after INDEX, KEY or UNIQUE [KEY].
func (s *sqlSchema) addKey(t *sqlTable, p *sp, unique bool, symbol, mig string) error {
	name := ""
	if !p.isP("(") && !p.isKw("USING") {
		name, _ = p.ident()
	}
	if p.accept("USING") {
		p.next()
	}
	cols := p.columnList()
	if len(cols) == 0 {
		return fmt.Errorf("cannot read the index columns")
	}
	if name == "" {
		name = symbol // a constraint symbol names the unique key it creates
	}
	if name == "" {
		name = cols[0]
	}
	t.indexes = append(t.indexes, &sqlIndex{name: t.freeIndexName(name), unique: unique, columns: cols, introduced: mig})
	return nil
}

// addForeignKey reads `[index_name] (cols) REFERENCES t (cols) [ON ...]`.
func (s *sqlSchema) addForeignKey(t *sqlTable, p *sp, symbol, mig string) error {
	indexName := ""
	if !p.isP("(") {
		indexName, _ = p.ident()
	}
	cols := p.columnList()
	if len(cols) == 0 || !p.accept("REFERENCES") {
		return fmt.Errorf("cannot read the foreign key")
	}
	fk, err := parseReference(p)
	if err != nil {
		return err
	}
	fk.columns = cols
	s.attachFK(t, fk, symbol, indexName, mig)
	return nil
}

// parseReference reads `tbl (cols) [MATCH x] [ON DELETE a] [ON UPDATE b]`.
func parseReference(p *sp) (*sqlFK, error) {
	table, ok := p.tableName()
	if !ok {
		return nil, fmt.Errorf("cannot read the referenced table")
	}
	fk := &sqlFK{refTable: table}
	if p.isP("(") {
		fk.refColumns = p.columnList()
	}
	for !p.eof() {
		switch {
		case p.accept("MATCH"):
			p.next()
		case p.accept("ON", "DELETE"):
			fk.onDelete = readAction(p)
		case p.accept("ON", "UPDATE"):
			fk.onUpdate = readAction(p)
		default:
			return fk, nil
		}
	}
	return fk, nil
}

func readAction(p *sp) string {
	switch {
	case p.accept("SET", "NULL"):
		return "SET NULL"
	case p.accept("SET", "DEFAULT"):
		return "SET DEFAULT"
	case p.accept("NO", "ACTION"):
		return "NO ACTION"
	case p.accept("CASCADE"):
		return "CASCADE"
	case p.accept("RESTRICT"):
		return "RESTRICT"
	}
	return ""
}

// attachFK adds a foreign key to the table. The constraint is named by its
// symbol, else (as MariaDB does) by the FOREIGN KEY index name, else it gets a
// placeholder name only this parser uses to find it again (the servers differ on
// what they call it: MySQL says `<table>_ibfk_<n>`, MariaDB just `<n>`), and
// autoNamed says not to report it. The index the server creates for it is added
// by finishTable, once the whole statement is known.
func (s *sqlSchema) attachFK(t *sqlTable, fk *sqlFK, symbol, indexName, mig string) {
	fk.introduced = mig
	switch {
	case symbol != "":
		fk.name = symbol
	case indexName != "":
		fk.name = indexName
	default:
		fk.autoNamed = true
		fk.name = fmt.Sprintf("%s_ibfk_%d", t.name, nextAutoFK(t))
	}
	fk.indexHint = symbol
	if fk.indexHint == "" {
		fk.indexHint = indexName
	}
	if len(fk.refColumns) == 0 {
		// REFERENCES t without columns means t's primary key.
		if ref := s.tables[strings.ToLower(fk.refTable)]; ref != nil {
			if pk := ref.primary(); pk != nil {
				fk.refColumns = append([]string(nil), pk.columns...)
			}
		}
	}
	t.fks = append(t.fks, fk)
}

func nextAutoFK(t *sqlTable) int {
	n := 0
	for _, fk := range t.fks {
		if fk.autoNamed {
			if i := strings.LastIndex(fk.name, "_ibfk_"); i >= 0 {
				if v, err := strconv.Atoi(fk.name[i+6:]); err == nil && v > n {
					n = v
				}
			}
		}
	}
	return n + 1
}

// parseColumn reads `name type [attributes...]`.
func parseColumn(p *sp, mig string) (*sqlColumn, columnExtras, error) {
	var e columnExtras
	name, ok := p.ident()
	if !ok {
		return nil, e, fmt.Errorf("cannot read a column name")
	}
	col := &sqlColumn{name: name, nullable: true, introduced: mig}
	typ, ok := parseType(p)
	if !ok {
		return nil, e, fmt.Errorf("cannot read the type of column %s", name)
	}
	col.typ = typ
	if typ == "serial" {
		col.typ, col.nullable, col.extra = "bigint unsigned", false, "auto_increment"
		e.unique = true
	}
	for !p.eof() {
		switch {
		case p.accept("NOT", "NULL"):
			col.nullable = false
		case p.accept("NULL"):
			col.nullable = true
		case p.accept("DEFAULT"):
			col.def, col.hasDefault = readDefault(p)
		case p.accept("AUTO_INCREMENT"):
			col.extra = joinExtra(col.extra, "auto_increment")
		case p.accept("ON", "UPDATE"):
			v, _ := readDefault(p)
			col.extra = joinExtra(col.extra, "on update "+v)
		case p.accept("PRIMARY", "KEY"), p.accept("KEY"):
			e.primary = true
		case p.accept("UNIQUE", "KEY"), p.accept("UNIQUE"):
			e.unique = true
		case p.accept("COMMENT"):
			if v := p.next(); v.kind == 's' {
				col.comment = v.text
			}
		case p.accept("REFERENCES"):
			fk, err := parseReference(p)
			if err != nil {
				return nil, e, err
			}
			e.fk = fk
		case p.accept("CHARACTER", "SET"), p.accept("CHARSET"), p.accept("COLLATE"):
			p.next()
		case p.accept("GENERATED", "ALWAYS", "AS"), p.accept("AS"):
			p.balanced()
			col.extra = joinExtra(col.extra, "generated")
		case p.accept("CHECK"):
			p.balanced()
		default:
			p.next()
		}
	}
	return col, e, nil
}

func joinExtra(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// parseType reads a column type: the name, its arguments, and the UNSIGNED and
// ZEROFILL modifiers, in the lower-case form MySQL reports.
func parseType(p *sp) (string, bool) {
	t := p.peek()
	if t.kind != 'w' {
		return "", false
	}
	p.next()
	name := strings.ToLower(t.text)
	switch name {
	case "double":
		p.accept("PRECISION")
	case "character", "char":
		if p.accept("VARYING") {
			name = "varchar"
		} else if name == "character" {
			name = "char"
		}
	case "national":
		return parseType(p)
	case "integer", "int4":
		name = "int"
	case "bool", "boolean":
		return "tinyint(1)", true
	case "numeric", "dec", "fixed":
		name = "decimal"
	case "real", "float8":
		name = "double"
	case "int1":
		name = "tinyint"
	case "int2":
		name = "smallint"
	case "int3", "middleint":
		name = "mediumint"
	case "int8":
		name = "bigint"
	}
	if p.isP("(") {
		inner, _ := p.balanced()
		var parts []string
		for _, tok := range inner {
			switch tok.kind {
			case 's':
				parts = append(parts, "'"+strings.ReplaceAll(tok.text, "'", "''")+"'")
			default:
				parts = append(parts, tok.text)
			}
		}
		name += "(" + strings.Join(parts, "") + ")"
	}
	for {
		switch {
		case p.accept("UNSIGNED"):
			name += " unsigned"
		case p.accept("SIGNED"):
		case p.accept("ZEROFILL"):
			name += " zerofill"
		default:
			return name, true
		}
	}
}

// readDefault reads the value after DEFAULT (or ON UPDATE), spelled the way
// MariaDB reports it: strings quoted, functions lower-case with parentheses.
// ok is false for an explicit NULL, which is no default.
func readDefault(p *sp) (string, bool) {
	t := p.peek()
	switch {
	case t.kind == 's':
		p.next()
		return "'" + strings.ReplaceAll(t.text, "'", "''") + "'", true
	case t.kind == 'n':
		p.next()
		return t.text, true
	case t.isP("-") || t.isP("+"):
		p.next()
		n := p.next()
		return t.text + n.text, true
	case t.isP("("):
		inner, _ := p.balanced()
		var b strings.Builder
		for _, tok := range inner {
			b.WriteString(tok.text)
		}
		return "(" + b.String() + ")", true
	case t.kind == 'w':
		p.next()
		if strings.EqualFold(t.text, "NULL") {
			return "", false
		}
		v := strings.ToLower(t.text)
		if p.isP("(") {
			inner, _ := p.balanced()
			var b strings.Builder
			for _, tok := range inner {
				b.WriteString(tok.text)
			}
			return v + "(" + b.String() + ")", true
		}
		switch v {
		case "current_timestamp", "now", "localtime", "localtimestamp", "current_date", "curdate", "current_time", "curtime":
			return v + "()", true
		}
		return v, true
	}
	return "", false
}

func (s *sqlSchema) dropTable(p *sp) {
	p.accept("IF", "EXISTS")
	for {
		name, ok := p.tableName()
		if !ok {
			return
		}
		delete(s.tables, strings.ToLower(name))
		if !p.acceptP(",") {
			return
		}
	}
}

func (s *sqlSchema) renameTables(p *sp, mig string) {
	for {
		from, ok := p.tableName()
		if !ok || !p.accept("TO") {
			return
		}
		to, ok := p.tableName()
		if !ok {
			return
		}
		s.rename(from, to)
		if !p.acceptP(",") {
			return
		}
	}
}

// rename moves a table to a new name, and re-points the foreign keys of every
// table that references it.
func (s *sqlSchema) rename(from, to string) {
	t := s.tables[strings.ToLower(from)]
	if t == nil {
		return
	}
	delete(s.tables, strings.ToLower(from))
	t.name = to
	s.tables[strings.ToLower(to)] = t
	for _, o := range s.tables {
		for _, fk := range o.fks {
			if strings.EqualFold(fk.refTable, from) {
				fk.refTable = to
			}
		}
	}
}

func (s *sqlSchema) createIndex(p *sp, unique bool, mig string) error {
	name, ok := p.ident()
	if !ok {
		return fmt.Errorf("cannot read the index name")
	}
	if p.accept("USING") {
		p.next()
	}
	if !p.accept("ON") {
		return fmt.Errorf("CREATE INDEX without ON")
	}
	table, _ := p.tableName()
	t := s.tables[strings.ToLower(table)]
	if t == nil {
		return fmt.Errorf("index on an unknown table %s", table)
	}
	if p.accept("USING") {
		p.next()
	}
	cols := p.columnList()
	if len(cols) == 0 {
		return fmt.Errorf("cannot read the index columns")
	}
	t.indexes = append(t.indexes, &sqlIndex{name: name, unique: unique, columns: cols, introduced: mig})
	return nil
}

func (s *sqlSchema) dropIndexOn(p *sp) error {
	name, ok := p.ident()
	if !ok || !p.accept("ON") {
		return fmt.Errorf("cannot read DROP INDEX")
	}
	table, _ := p.tableName()
	if t := s.tables[strings.ToLower(table)]; t != nil {
		t.dropIndex(name)
	}
	return nil
}

func (t *sqlTable) dropIndex(name string) {
	rest := t.indexes[:0:0]
	for _, ix := range t.indexes {
		if !(strings.EqualFold(ix.name, name) && !ix.primary) {
			rest = append(rest, ix)
		}
	}
	t.indexes = rest
}

// alterTable applies the comma-separated actions of an ALTER TABLE.
func (s *sqlSchema) alterTable(p *sp, mig string) error {
	name, ok := p.tableName()
	if !ok {
		return fmt.Errorf("cannot read the table name")
	}
	t := s.tables[strings.ToLower(name)]
	if t == nil {
		return fmt.Errorf("ALTER TABLE on an unknown table %s", name)
	}
	for _, action := range splitTop(p.t[p.i:]) {
		a := &sp{t: action}
		if err := s.alterAction(t, a, mig); err != nil {
			return err
		}
	}
	finishTable(t, mig)
	return nil
}

func (s *sqlSchema) alterAction(t *sqlTable, a *sp, mig string) error {
	switch {
	case a.accept("ADD", "CONSTRAINT"), a.accept("ADD"):
		return s.alterAdd(t, a, mig, strings.EqualFold(a.t[1].text, "CONSTRAINT"))
	case a.accept("DROP", "PRIMARY", "KEY"):
		rest := t.indexes[:0:0]
		for _, ix := range t.indexes {
			if !ix.primary {
				rest = append(rest, ix)
			}
		}
		t.indexes = rest
	case a.accept("DROP", "FOREIGN", "KEY"):
		n, _ := a.ident()
		rest := t.fks[:0:0]
		for _, fk := range t.fks {
			if !strings.EqualFold(fk.name, n) {
				rest = append(rest, fk)
			}
		}
		t.fks = rest
	case a.accept("DROP", "INDEX"), a.accept("DROP", "KEY"):
		n, _ := a.ident()
		t.dropIndex(n)
	case a.accept("DROP", "CONSTRAINT"):
		n, _ := a.ident()
		rest := t.fks[:0:0]
		for _, fk := range t.fks {
			if !strings.EqualFold(fk.name, n) {
				rest = append(rest, fk)
			}
		}
		t.fks = rest
	case a.accept("DROP", "COLUMN"), a.accept("DROP"):
		a.accept("IF", "EXISTS")
		n, _ := a.ident()
		t.dropColumn(n)
	case a.accept("MODIFY", "COLUMN"), a.accept("MODIFY"):
		a.accept("IF", "EXISTS")
		return s.alterColumnDef(t, a, "", mig)
	case a.accept("CHANGE", "COLUMN"), a.accept("CHANGE"):
		a.accept("IF", "EXISTS")
		old, _ := a.ident()
		return s.alterColumnDef(t, a, old, mig)
	case a.accept("RENAME", "COLUMN"):
		from, _ := a.ident()
		a.accept("TO")
		to, _ := a.ident()
		t.renameColumn(from, to)
	case a.accept("RENAME", "INDEX"), a.accept("RENAME", "KEY"):
		from, _ := a.ident()
		a.accept("TO")
		to, _ := a.ident()
		if ix := t.index(from); ix != nil {
			ix.name = to
		}
	case a.accept("RENAME", "TO"), a.accept("RENAME", "AS"), a.accept("RENAME"):
		to, _ := a.tableName()
		if to != "" {
			s.rename(t.name, to)
		}
	case a.accept("ENGINE"):
		a.acceptP("=")
		if v, ok := a.ident(); ok {
			t.engine = v
		}
	case a.accept("COMMENT"):
		a.acceptP("=")
		if v := a.next(); v.kind == 's' {
			t.comment = v.text
		}
	case a.accept("ALTER", "COLUMN"), a.accept("ALTER"):
		n, _ := a.ident()
		col := t.column(n)
		if col == nil {
			return nil
		}
		switch {
		case a.accept("SET", "DEFAULT"):
			col.def, col.hasDefault = readDefault(a)
		case a.accept("DROP", "DEFAULT"):
			col.def, col.hasDefault = "", false
		}
	case a.isKw("ALGORITHM"), a.isKw("LOCK"), a.isKw("AUTO_INCREMENT"), a.isKw("CONVERT"), a.isKw("DEFAULT"),
		a.isKw("CHARACTER"), a.isKw("CHARSET"), a.isKw("COLLATE"), a.isKw("ROW_FORMAT"), a.isKw("FORCE"),
		a.isKw("DISABLE"), a.isKw("ENABLE"), a.isKw("ORDER"), a.isKw("WITH"), a.isKw("WITHOUT"):
		// Options that do not change the shape of the schema.
	default:
		return fmt.Errorf("unsupported ALTER TABLE action %q", a.peek().text)
	}
	return nil
}

func (s *sqlSchema) alterAdd(t *sqlTable, a *sp, mig string, hadConstraint bool) error {
	symbol := ""
	if hadConstraint && !a.isKw("PRIMARY") && !a.isKw("UNIQUE") && !a.isKw("FOREIGN") && !a.isKw("CHECK") {
		symbol, _ = a.ident()
	}
	switch {
	case a.accept("PRIMARY", "KEY"):
		return s.addPrimary(t, a, mig)
	case a.accept("UNIQUE", "INDEX"), a.accept("UNIQUE", "KEY"), a.accept("UNIQUE"):
		return s.addKey(t, a, true, symbol, mig)
	case a.accept("FOREIGN", "KEY"):
		return s.addForeignKey(t, a, symbol, mig)
	case a.isKw("CHECK"):
		return nil
	case a.accept("FULLTEXT", "INDEX"), a.accept("FULLTEXT", "KEY"), a.accept("FULLTEXT"),
		a.accept("SPATIAL", "INDEX"), a.accept("SPATIAL", "KEY"), a.accept("SPATIAL"),
		a.accept("INDEX"), a.accept("KEY"):
		return s.addKey(t, a, false, "", mig)
	}
	a.accept("COLUMN")
	a.accept("IF", "NOT", "EXISTS")
	if a.isP("(") { // ADD COLUMN (a INT, b INT)
		inner, _ := a.balanced()
		for _, item := range splitTop(inner) {
			if err := s.tableItem(t, &sp{t: item}, mig); err != nil {
				return err
			}
		}
		return nil
	}
	col, extras, err := parseColumn(a, mig)
	if err != nil {
		return err
	}
	// parseColumn consumed everything, including FIRST / AFTER x; place it.
	t.columns = append(t.columns, col)
	placeColumn(t, col, a)
	return s.applyColumnExtras(t, col, extras, mig)
}

// placeColumn honours a trailing FIRST or AFTER <column> that parseColumn
// skipped over, by looking at the statement's last tokens.
func placeColumn(t *sqlTable, col *sqlColumn, a *sp) {
	n := len(a.t)
	pos := -1
	switch {
	case n >= 2 && a.t[n-2].is("AFTER"):
		after := a.t[n-1].text
		for i, c := range t.columns {
			if strings.EqualFold(c.name, after) {
				pos = i + 1
			}
		}
	case n >= 1 && a.t[n-1].is("FIRST"):
		pos = 0
	}
	if pos < 0 {
		return
	}
	// col is the last element; move it to pos.
	t.columns = t.columns[:len(t.columns)-1]
	if pos > len(t.columns) {
		pos = len(t.columns)
	}
	t.columns = append(t.columns, nil)
	copy(t.columns[pos+1:], t.columns[pos:])
	t.columns[pos] = col
}

func (s *sqlSchema) alterColumnDef(t *sqlTable, a *sp, old string, mig string) error {
	col, extras, err := parseColumn(a, mig)
	if err != nil {
		return err
	}
	target := old
	if target == "" {
		target = col.name
	}
	existing := t.column(target)
	if existing == nil {
		return fmt.Errorf("ALTER TABLE changes an unknown column %s", target)
	}
	col.introduced = existing.introduced
	if old != "" && !strings.EqualFold(old, col.name) {
		t.renameColumn(old, col.name)
	}
	for i, c := range t.columns {
		if c == existing {
			t.columns[i] = col
		}
	}
	// A column changed in place keeps the keys it already had; only keys the new
	// definition adds are new.
	extras.primary = extras.primary && t.primary() == nil
	if err := s.applyColumnExtras(t, col, extras, mig); err != nil {
		return err
	}
	n := len(a.t)
	if n >= 1 && (a.t[n-1].is("FIRST") || (n >= 2 && a.t[n-2].is("AFTER"))) {
		// Move to the requested position: take it out, then place it.
		for i, c := range t.columns {
			if c == col {
				t.columns = append(t.columns[:i], t.columns[i+1:]...)
				break
			}
		}
		t.columns = append(t.columns, col)
		placeColumn(t, col, a)
	}
	return nil
}

func (t *sqlTable) dropColumn(name string) {
	rest := t.columns[:0:0]
	for _, c := range t.columns {
		if !strings.EqualFold(c.name, name) {
			rest = append(rest, c)
		}
	}
	t.columns = rest
	// Take the column out of its indexes, and drop an index it empties.
	var ixs []*sqlIndex
	for _, ix := range t.indexes {
		var cols []string
		for _, c := range ix.columns {
			if !strings.EqualFold(c, name) {
				cols = append(cols, c)
			}
		}
		ix.columns = cols
		if len(cols) > 0 {
			ixs = append(ixs, ix)
		}
	}
	t.indexes = ixs
}

func (t *sqlTable) renameColumn(from, to string) {
	if c := t.column(from); c != nil {
		c.name = to
	}
	swap := func(cols []string) {
		for i, c := range cols {
			if strings.EqualFold(c, from) {
				cols[i] = to
			}
		}
	}
	for _, ix := range t.indexes {
		swap(ix.columns)
	}
	for _, fk := range t.fks {
		swap(fk.columns)
	}
}
