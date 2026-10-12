package codegraph

import "strings"

// scanSQLTables finds the tables a SQL string names, split by what it does to
// them: reads (FROM, JOIN) and writes (INSERT/REPLACE INTO, UPDATE, DELETE FROM,
// TRUNCATE). It walks tokens rather than matching text, so quoted strings and
// comments cannot be mistaken for SQL, `schema.table` and backticked names read
// right, and a comma list (`FROM a, b`) and a statement that both writes and
// reads (`INSERT ... SELECT ... FROM`) are understood. Names are returned as
// written; the caller keeps the ones that are real tables. Dynamic SQL
// (`FROM %s`, `FROM ` + name) names nothing.
func scanSQLTables(text string) (reads, writes []string) {
	toks := lexSQL(text)
	n := len(toks)

	// ref reads a table reference at i: [schema.]name. A name qualified by a
	// system schema (information_schema.TABLES) is not a table of the app.
	ref := func(i int) (name string, next int) {
		if i >= n || (toks[i].kind != 'w' && toks[i].kind != 'i') {
			return "", i
		}
		if toks[i].kind == 'w' && notATable[strings.ToUpper(toks[i].text)] {
			return "", i
		}
		name, j, system := toks[i].text, i+1, false
		for j+1 < n && toks[j].isP(".") && (toks[j+1].kind == 'w' || toks[j+1].kind == 'i') {
			if systemSchemas[strings.ToLower(name)] {
				system = true
			}
			name = toks[j+1].text
			j += 2
		}
		if system {
			return "", j
		}
		return name, j
	}
	// skipAlias steps over `[AS] alias` after a table reference.
	skipAlias := func(i int) int {
		if i < n && toks[i].is("AS") {
			i++
			if i < n && (toks[i].kind == 'w' || toks[i].kind == 'i') {
				i++
			}
			return i
		}
		if i < n && (toks[i].kind == 'i' || (toks[i].kind == 'w' && !clauseWords[strings.ToUpper(toks[i].text)])) {
			i++
		}
		return i
	}
	// refList reads `t1 [alias], t2 [alias] ...` from i and returns the names.
	refList := func(i int) (names []string, next int) {
		for {
			name, j := ref(i)
			if name == "" {
				return names, i
			}
			names = append(names, name)
			i = skipAlias(j)
			if i < n && toks[i].isP(",") {
				i++
				continue
			}
			return names, i
		}
	}

	deleting := false
	for i := 0; i < n; {
		t := toks[i]
		if t.kind != 'w' {
			i++
			continue
		}
		switch strings.ToUpper(t.text) {
		case "INSERT", "REPLACE":
			j := i + 1
			for j < n && (toks[j].is("LOW_PRIORITY") || toks[j].is("DELAYED") || toks[j].is("HIGH_PRIORITY") || toks[j].is("IGNORE")) {
				j++
			}
			if j < n && toks[j].is("INTO") {
				if name, k := ref(j + 1); name != "" {
					writes = append(writes, name)
					i = k
					continue
				}
			}
		case "UPDATE":
			j := i + 1
			for j < n && (toks[j].is("LOW_PRIORITY") || toks[j].is("IGNORE")) {
				j++
			}
			names, k := refList(j)
			writes = append(writes, names...)
			if len(names) > 0 {
				i = k
				continue
			}
		case "DELETE":
			deleting = true
		case "TRUNCATE":
			j := i + 1
			if j < n && toks[j].is("TABLE") {
				j++
			}
			if name, k := ref(j); name != "" {
				writes = append(writes, name)
				i = k
				continue
			}
		case "FROM":
			names, k := refList(i + 1)
			if deleting {
				writes = append(writes, names...)
			} else {
				reads = append(reads, names...)
			}
			deleting = false
			if len(names) > 0 {
				i = k
				continue
			}
		case "JOIN":
			if name, k := ref(i + 1); name != "" {
				reads = append(reads, name)
				i = k
				continue
			}
		}
		i++
	}
	return reads, writes
}

// systemSchemas hold the server's own tables, never the app's.
var systemSchemas = map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true}

// notATable are words that can follow FROM, JOIN or UPDATE without naming a
// table.
var notATable = map[string]bool{
	"SELECT": true, "DUAL": true, "LATERAL": true, "VALUES": true, "SET": true, "WHERE": true, "WITH": true,
}

// clauseWords end a table reference: what follows is not an alias.
var clauseWords = map[string]bool{
	"WHERE": true, "JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "CROSS": true, "NATURAL": true, "OUTER": true,
	"FULL": true, "STRAIGHT_JOIN": true, "ON": true, "USING": true, "SET": true, "ORDER": true, "GROUP": true,
	"HAVING": true, "LIMIT": true, "UNION": true, "EXCEPT": true, "INTERSECT": true, "FOR": true, "LOCK": true,
	"USE": true, "FORCE": true, "IGNORE": true, "PARTITION": true, "VALUES": true, "SELECT": true, "WINDOW": true,
	"INTO": true, "RETURNING": true, "OFFSET": true,
}
