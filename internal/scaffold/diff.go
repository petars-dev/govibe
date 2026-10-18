package scaffold

import (
	"fmt"
	"strings"
)

// Diff is a unified diff of old against new (three lines of context), "" when they are
// equal. It is what `govibe upgrade` shows for a managed file it rewrites or refuses.
func Diff(name string, old, new []byte) string {
	if string(old) == string(new) {
		return ""
	}
	a, b := splitLines(string(old)), splitLines(string(new))
	ops := diffLines(a, b)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s (yours)\n+++ %s (framework)\n", name, name)
	const ctx = 3
	// Group the edits into hunks separated by more than 2*ctx equal lines.
	type hunk struct{ start, end int } // indices into ops
	var hunks []hunk
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		h := hunk{start: i, end: i}
		for j := i; j < len(ops); j++ {
			if ops[j].kind != ' ' {
				h.end = j
				continue
			}
			// Equal lines: stop the hunk when the next edit is too far.
			far := true
			for k := j; k < len(ops) && k <= j+2*ctx; k++ {
				if ops[k].kind != ' ' {
					far = false
					break
				}
			}
			if far {
				break
			}
		}
		hunks = append(hunks, h)
		i = h.end + 1
	}
	for _, h := range hunks {
		s := max(h.start-ctx, 0)
		e := min(h.end+ctx, len(ops)-1)
		aStart, bStart, aN, bN := ops[s].a+1, ops[s].b+1, 0, 0
		for _, op := range ops[s : e+1] {
			switch op.kind {
			case ' ':
				aN++
				bN++
			case '-':
				aN++
			case '+':
				bN++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aN, bStart, bN)
		for _, op := range ops[s : e+1] {
			out.WriteString(string(op.kind) + op.text + "\n")
		}
	}
	return out.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// op is one line of a diff: kept (' '), removed ('-') or added ('+'); a and b are the
// line indices before the op in old and new.
type op struct {
	kind byte
	text string
	a, b int
}

// diffLines is the edit script from a to b by the longest common subsequence. For
// inputs too large for the table it reports the whole of a removed and b added.
func diffLines(a, b []string) []op {
	n, m := len(a), len(b)
	var ops []op
	if n*m > 4_000_000 {
		for i, l := range a {
			ops = append(ops, op{'-', l, i, 0})
		}
		for j, l := range b {
			ops = append(ops, op{'+', l, n, j})
		}
		return ops
	}
	// lcs[i][j] is the length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i], i, j})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', a[i], i, j}) // a removal before an insertion, as diff prints it
			i++
		default:
			ops = append(ops, op{'+', b[j], i, j})
			j++
		}
	}
	return ops
}
