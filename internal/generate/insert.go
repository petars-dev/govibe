package generate

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

// hasMarker reports whether a Go source holds the marker comment on a line of its own.
func hasMarker(src []byte, marker string) bool {
	for _, line := range strings.Split(string(src), "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// insertAtMarker puts snippet (lines, with their own relative indentation) before the
// marker line, indented like it, adds the imports and formats the file. The marker
// stays, so the next generator inserts there too. ok is false when the source has no
// marker: nothing is changed and the caller prints the snippet.
func insertAtMarker(src []byte, marker, snippet string, imports []string) (out []byte, ok bool, err error) {
	lines := strings.Split(string(src), "\n")
	at := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, false, nil
	}
	indent := lines[at][:len(lines[at])-len(strings.TrimLeft(lines[at], " \t"))]
	var ins []string
	for _, l := range strings.Split(strings.TrimRight(snippet, "\n"), "\n") {
		if l == "" {
			ins = append(ins, "")
			continue
		}
		ins = append(ins, indent+l)
	}
	merged := append([]string{}, lines[:at]...)
	merged = append(merged, ins...)
	merged = append(merged, lines[at:]...)
	text := []byte(strings.Join(merged, "\n"))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", text, parser.ParseComments)
	if err != nil {
		return nil, true, fmt.Errorf("after inserting at %s the file does not parse: %w", marker, err)
	}
	for _, imp := range imports {
		astutil.AddImport(fset, f, imp)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, true, err
	}
	return buf.Bytes(), true, nil
}

// insertUnderHeading appends lines to the end of the markdown section that starts with
// the level-two heading (case-insensitive), before the next heading. ok is false when
// the document has no such heading: nothing is changed.
func insertUnderHeading(md []byte, heading string, lines []string) (out []byte, ok bool) {
	headRe := regexp.MustCompile(`(?i)^##\s+` + regexp.QuoteMeta(heading) + `\s*$`)
	anyHead := regexp.MustCompile(`^#{1,6}\s`)
	src := strings.Split(string(md), "\n")
	start := -1
	for i, l := range src {
		if headRe.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, false
	}
	end := len(src)
	for i := start + 1; i < len(src); i++ {
		if anyHead.MatchString(src[i]) {
			end = i
			break
		}
	}
	// The section's last line of content, then a blank line before the next heading.
	last := end
	for last > start+1 && strings.TrimSpace(src[last-1]) == "" {
		last--
	}
	var merged []string
	merged = append(merged, src[:last]...)
	merged = append(merged, lines...)
	if end < len(src) {
		merged = append(merged, "")
	}
	merged = append(merged, src[end:]...)
	text := strings.Join(merged, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text), true
}

// appendSection adds a level-two section at the end of a markdown document.
func appendSection(md []byte, heading string, lines []string) []byte {
	text := strings.TrimRight(string(md), "\n")
	if text != "" {
		text += "\n\n"
	}
	text += "## " + heading + "\n" + strings.Join(lines, "\n") + "\n"
	return []byte(text)
}
