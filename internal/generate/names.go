package generate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Name is the forms a generated thing's name takes: the words of the input, and the
// Go, file, package and URL spellings derived from them.
type Name struct {
	Input  string   // as given
	Words  []string // lower-case words: "rate-limit", "RateLimit", "rate_limit" are all [rate limit]
	Pascal string   // RateLimit
	Camel  string   // rateLimit
	Snake  string   // rate_limit (file names)
	Kebab  string   // rate-limit (URL paths)
	Pkg    string   // ratelimit (package names)
	Upper  string   // RATE_LIMIT (spec names)
}

var nameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// ParseName reads a name in any of the usual spellings. A name that is not letters,
// digits, '-' and '_', or that is a Go keyword, is an error.
func ParseName(s string) (Name, error) {
	s = strings.TrimSpace(s)
	if !nameRe.MatchString(s) {
		return Name{}, fmt.Errorf("name %q: use letters, digits, '-' and '_', starting with a letter", s)
	}
	n := Name{Input: s, Words: words(s)}
	for i, w := range n.Words {
		cap := strings.ToUpper(w[:1]) + w[1:]
		n.Pascal += cap
		if i == 0 {
			n.Camel += w
		} else {
			n.Camel += cap
		}
	}
	n.Snake = strings.Join(n.Words, "_")
	n.Kebab = strings.Join(n.Words, "-")
	n.Pkg = strings.Join(n.Words, "")
	n.Upper = strings.ToUpper(n.Snake)
	if keywords[n.Camel] || keywords[n.Pkg] {
		return Name{}, fmt.Errorf("name %q is a Go keyword", s)
	}
	return n, nil
}

var keywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true, "defer": true, "else": true,
	"fallthrough": true, "for": true, "func": true, "go": true, "goto": true, "if": true, "import": true, "interface": true,
	"map": true, "package": true, "range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// words splits on '-', '_' and the boundaries of camel case, and lower-cases.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '-' || r == '_':
			flush()
		case unicode.IsUpper(r):
			// A boundary before an upper-case letter that follows a lower-case one or a digit
			// (rateLimit), or that starts a new word after a run of capitals (HTTPServer -> http server).
			if i > 0 {
				prev := runes[i-1]
				next := rune(0)
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && unicode.IsLower(next)) {
					flush()
				}
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// Plural is the plural of a lower-case English noun, well enough for a table or a
// route: order -> orders, category -> categories, box -> boxes, status -> statuses.
func Plural(w string) string {
	switch {
	case w == "":
		return w
	case strings.HasSuffix(w, "s") || strings.HasSuffix(w, "x") || strings.HasSuffix(w, "z") || strings.HasSuffix(w, "ch") || strings.HasSuffix(w, "sh"):
		return w + "es"
	case strings.HasSuffix(w, "y") && len(w) > 1 && !strings.ContainsRune("aeiou", rune(w[len(w)-2])):
		return w[:len(w)-1] + "ies"
	}
	return w + "s"
}

// Singular undoes Plural: orders -> order, categories -> category, boxes -> box. A
// word that does not look plural is returned as it is.
func Singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 3:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "zes") || strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "sses") || strings.HasSuffix(w, "uses"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 1:
		return w[:len(w)-1]
	}
	return w
}

// pluralName is n with its last word made plural.
func pluralName(n Name) Name {
	ws := append([]string(nil), n.Words...)
	ws[len(ws)-1] = Plural(ws[len(ws)-1])
	out, _ := ParseName(strings.Join(ws, "_"))
	return out
}

// singularName is n with its last word made singular.
func singularName(n Name) Name {
	ws := append([]string(nil), n.Words...)
	ws[len(ws)-1] = Singular(ws[len(ws)-1])
	out, _ := ParseName(strings.Join(ws, "_"))
	return out
}

// Title is the words capitalized and spaced: "rate limit" -> "Rate limit".
func (n Name) Title() string {
	s := strings.Join(n.Words, " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// Spaced is the words spaced: "rate limit".
func (n Name) Spaced() string { return strings.Join(n.Words, " ") }
