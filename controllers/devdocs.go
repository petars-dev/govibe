package controllers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/a-h/templ"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type DocItem struct {
	Title string
	Slug  string
	Path  string
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a base filename into a kebab-case slug; optionally strips a prefix first.
func slugify(base, stripPrefix string) string {
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if stripPrefix != "" && strings.HasPrefix(name, stripPrefix) {
		name = strings.TrimPrefix(name, stripPrefix)
	}
	name = strings.ToLower(name)
	name = nonAlnum.ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}

func humanTitleFromFilename(base, stripPrefix string) string {
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if stripPrefix != "" && strings.HasPrefix(name, stripPrefix) {
		name = strings.TrimPrefix(name, stripPrefix)
	}
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	parts := strings.Fields(name)
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
		}
	}
	return strings.Join(parts, " ")
}

// extractTitle returns the first H1 ("# ") from markdown or a fallback if none.
func extractTitle(md []byte, fallback string) string {
	for _, line := range strings.Split(string(md), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return fallback
}

func listDocs(dir, pattern, stripPrefix string) ([]DocItem, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	items := make([]DocItem, 0, len(matches))
	for _, p := range matches {
		base := filepath.Base(p)
		slug := slugify(base, stripPrefix)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		title := extractTitle(b, humanTitleFromFilename(base, stripPrefix))
		// For prefixed collections (e.g., SPEC_), strip the prefix from titles and prettify.
		if stripPrefix != "" {
			if strings.HasPrefix(title, stripPrefix) {
				title = strings.TrimPrefix(title, stripPrefix)
			}
			title = strings.ReplaceAll(title, "_", " ")
			title = strings.ReplaceAll(title, "-", " ")
			parts := strings.Fields(title)
			for i := range parts {
				if len(parts[i]) > 0 {
					parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
				}
			}
			title = strings.Join(parts, " ")
		}
		items = append(items, DocItem{Title: title, Slug: slug, Path: p})
	}
	return items, nil
}

func markdownRenderer() goldmark.Markdown {
	return goldmark.New(
		// Give every heading a stable id so sections can be deep-linked.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithExtensions(
			extension.GFM,
			extension.Typographer,
			highlighting.NewHighlighting(
				highlighting.WithGuessLanguage(true),
				// Emit chroma's semantic classes (e.g. ".kd", ".s") instead of
				// baked-in inline colors, so highlightCSS() below can supply a
				// dark and a light palette that follow the docs theme toggle.
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
					chromahtml.WithLineNumbers(false),
				),
			),
		),
	)
}

// renderMarkdown converts markdown bytes to HTML with GFM + theme-aware highlighting,
// then layers on heading anchors and code-block copy buttons.
func renderMarkdown(b []byte) (string, error) {
	var buf bytes.Buffer
	if err := markdownRenderer().Convert(b, &buf); err != nil {
		return "", err
	}
	html := addCodeCopyButtons(buf.String())
	html = addHeadingAnchors(html)
	return html, nil
}

var headingIDRe = regexp.MustCompile(`<h([1-6]) id="([^"]+)">`)

// addHeadingAnchors inserts a hidden "#" permalink into every heading goldmark
// gave an id, so a reader can hover a section and copy a link straight to it.
func addHeadingAnchors(html string) string {
	return headingIDRe.ReplaceAllString(html,
		`<h$1 id="$2"><a class="heading-anchor" href="#$2" aria-label="Link to this section">#</a>`)
}

var preBlockRe = regexp.MustCompile(`(?s)<pre([^>]*)>(.*?)</pre>`)

// addCodeCopyButtons wraps each rendered code block in a container with a
// "Copy" button (wired up by the script in docsPage) for one-click copying.
func addCodeCopyButtons(html string) string {
	return preBlockRe.ReplaceAllString(html,
		`<div class="code-block"><pre$1>$2</pre><button type="button" class="copy-btn" data-copy-btn>Copy</button></div>`)
}

// highlightCSS renders chroma's stylesheet for styleName, scoped under the
// given html.<scopeClass> selector so dark and light code themes can coexist
// and switch with the rest of the docs theme toggle.
func highlightCSS(styleName, scopeClass string) string {
	style := chromastyles.Get(styleName)
	if style == nil {
		style = chromastyles.Fallback
	}
	var raw bytes.Buffer
	if err := chromahtml.New(chromahtml.WithClasses(true)).WriteCSS(&raw, style); err != nil {
		return ""
	}
	var out strings.Builder
	for _, line := range strings.Split(raw.String(), "\n") {
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "*/ "); idx != -1 {
			out.WriteString(line[:idx+3])
			out.WriteString("html." + scopeClass + " ")
			out.WriteString(line[idx+3:])
		} else {
			out.WriteString(line)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

var highlightCSSOnce = sync.OnceValue(func() string {
	return highlightCSS("dracula", "theme-dark") + highlightCSS("github", "theme-light")
})

// docsPage renders a dark/light themed docs layout with an optional sidebar nav.
// section is either "app" or "specs" and controls links and labels.
func docsPage(title string, section string, nav []DocItem, activeSlug string, contentHTML string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
		_, _ = io.WriteString(w, "<title>"+templ.EscapeString(title)+"</title>")
		_, _ = io.WriteString(w, "<link rel=\"icon\" href=\"/assets/static/govibe-logo.png\">")
		_, _ = io.WriteString(w, "<link rel=\"stylesheet\" href=\"/assets/static/theme.css\">")
		_, _ = io.WriteString(w, "<link rel=\"stylesheet\" href=\"/assets/static/docs.css\">")
		// Per-theme syntax highlighting palettes (dark: dracula, light: github),
		// scoped so they follow the same html.theme-* toggle as the rest of the page.
		_, _ = io.WriteString(w, "<style>"+highlightCSSOnce()+"</style>")
		// Theme toggle bootstraps before first paint. Default to dark, persisted in localStorage.
		_, _ = io.WriteString(w, "<script src=\"/assets/static/theme.js\"></script>")
		_, _ = io.WriteString(w, "</head><body class=\"docs-body\">")
		// Top header with brand, tabs, and theme toggle with icons
		secLabel := "Docs"
		if section == "specs" {
			secLabel = "Specs"
		}
		_, _ = io.WriteString(w, "<header class=\"site-header\">")
		_, _ = io.WriteString(w, "<div class=\"brand\"><a class=\"brand-link\" href=\"/\"><img class=\"brand-logo\" src=\"/assets/static/govibe-logo.png\" alt=\"GoVibe\"></a><span class=\"brand-name\">GoVibe Dev Docs</span></div>")
		_, _ = io.WriteString(w, "<nav class=\"top-tabs\"><a class=\"tab"+boolClass(section == "app", " active")+"\" href=\"/dev/docs/app\">Docs</a><a class=\"tab"+boolClass(section == "specs", " active")+"\" href=\"/dev/docs/specs\">Specs</a><a class=\"tab\" href=\"/dev/graph\">Graph</a><a class=\"tab\" href=\"/dev/db\">DB</a></nav>")
		_, _ = io.WriteString(w, "<div class=\"header-actions\"><a class=\"btn btn-ghost\" href=\"/\">← App</a><button class=\"theme-toggle\" onclick=\"toggleTheme()\" aria-label=\"Toggle theme\"><svg class=\"icon-sun\" width=\"18\" height=\"18\" viewBox=\"0 0 24 24\" fill=\"currentColor\" aria-hidden=\"true\"><path d=\"M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z\"/></svg><svg class=\"icon-moon\" width=\"18\" height=\"18\" viewBox=\"0 0 24 24\" fill=\"currentColor\" aria-hidden=\"true\"><path d=\"M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z\"/></svg></button></div>")
		_, _ = io.WriteString(w, "</header>")
		_, _ = io.WriteString(w, "<div class=\"docs-layout\">")

		if len(nav) > 0 {
			_, _ = io.WriteString(w, "<aside class=\"docs-sidebar\"><div class=\"sidebar-group\"><div class=\"sidebar-title\">"+secLabel+"</div><input id=\"navSearch\" class=\"search-input\" type=\"search\" placeholder=\"Search titles...\" aria-label=\"Search titles\"></div><nav><ul id=\"navList\" class=\"nav-list\">")
			// Link to the section index as the first item
			_, _ = io.WriteString(w, "<li data-title=\"all\"><a class=\"nav-link"+boolClass(activeSlug == "", " active")+"\" href=\"/dev/docs/"+section+"\">All "+secLabel+" Index</a></li>")
			for _, it := range nav {
				href := "/dev/docs/" + section + "/" + it.Slug
				cls := "nav-link"
				if it.Slug == activeSlug {
					cls += " active"
				}
				_, _ = io.WriteString(w, "<li data-title=\""+strings.ToLower(templ.EscapeString(it.Title))+"\"><a class=\""+cls+"\" href=\""+href+"\">"+templ.EscapeString(it.Title)+"</a></li>")
			}
			_, _ = io.WriteString(w, "</ul></nav></aside>")
		}

		_, _ = io.WriteString(w, "<main class=\"docs-content\">")
		// Back to index link / breadcrumb
		_, _ = io.WriteString(w, "<div class=\"breadcrumb\"><a class=\"back-link\" href=\"/dev/docs/"+section+"\">← Back to "+secLabel+" Index</a></div>")
		_, _ = io.WriteString(w, "<article class=\"md-content\">")
		_, _ = io.WriteString(w, contentHTML)
		_, _ = io.WriteString(w, "</article></main>")
		// Client-side search filter for sidebar and index lists.
		_, _ = io.WriteString(w, `<script>(function(){var q=function(id){return document.getElementById(id)};var navInput=q('navSearch');var navList=q('navList');function filter(list, term){if(!list)return;term=(term||'').toLowerCase();Array.prototype.forEach.call(list.children,function(li){var t=li.getAttribute('data-title')||'';li.style.display = t.indexOf(term) !== -1 ? '' : 'none';});}if(navInput){navInput.addEventListener('input',function(e){filter(navList,e.target.value);});}})();</script>`)
		// Wire up the "Copy" button on each code block.
		_, _ = io.WriteString(w, `<script>(function(){document.querySelectorAll('[data-copy-btn]').forEach(function(btn){btn.addEventListener('click',function(){var pre=btn.previousElementSibling;var text=pre?pre.innerText:'';navigator.clipboard.writeText(text).then(function(){btn.textContent='Copied';btn.setAttribute('data-copied','1');setTimeout(function(){btn.textContent='Copy';btn.removeAttribute('data-copied');},1500);}).catch(function(){});});});})();</script>`)
		_, _ = io.WriteString(w, "</div></body></html>")
		return nil
	})
}

// boolClass appends the suffix when condition is true.
func boolClass(ok bool, suffix string) string {
	if ok {
		return suffix
	}
	return ""
}

// /dev/docs/app index
func DevDocsAppIndex(w http.ResponseWriter, r *http.Request) {
	items, _ := listDocs("docs/public", "*.md", "")
	var b strings.Builder
	b.WriteString("<h1>Application Docs</h1><input id=\"indexSearch\" class=\"search-input\" type=\"search\" placeholder=\"Search titles...\" aria-label=\"Search titles\"><ul id=\"indexList\" class=\"index-list\">")
	for _, it := range items {
		b.WriteString("<li data-title=\"" + strings.ToLower(templ.EscapeString(it.Title)) + "\"><a href=\"/dev/docs/app/" + it.Slug + "\">" + templ.EscapeString(it.Title) + "</a></li>")
	}
	b.WriteString("</ul>")
	b.WriteString(`<script>(function(){var q=function(id){return document.getElementById(id)};var input=q('indexSearch');var list=q('indexList');if(!input||!list)return;input.addEventListener('input',function(e){var term=(e.target.value||'').toLowerCase();Array.prototype.forEach.call(list.children,function(li){var t=li.getAttribute('data-title')||'';li.style.display = t.indexOf(term)!==-1 ? '' : 'none';});});})();</script>`)
	templ.Handler(docsPage("Application Docs", "app", items, "", b.String())).ServeHTTP(w, r)
}

// /dev/docs/app/{slug}
func DevDocsAppFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	items, _ := listDocs("docs/public", "*.md", "")
	var fp, title string
	for _, it := range items {
		if it.Slug == slug {
			fp, title = it.Path, it.Title
			break
		}
	}
	if fp == "" {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(fp)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	html, _ := renderMarkdown(b)
	templ.Handler(docsPage(title, "app", items, slug, html)).ServeHTTP(w, r)
}

// /dev/docs/specs index (SPEC_*.md, slugs drop SPEC_ and are lowercase hyphenated)
func DevDocsSpecs(w http.ResponseWriter, r *http.Request) {
	items, _ := listDocs("docs/specs", "SPEC_*.md", "SPEC_")
	var b strings.Builder
	b.WriteString("<h1>Specifications</h1><input id=\"indexSearch\" class=\"search-input\" type=\"search\" placeholder=\"Search titles...\" aria-label=\"Search titles\"><ul id=\"indexList\" class=\"index-list\">")
	for _, it := range items {
		b.WriteString("<li data-title=\"" + strings.ToLower(templ.EscapeString(it.Title)) + "\"><a href=\"/dev/docs/specs/" + it.Slug + "\">" + templ.EscapeString(it.Title) + "</a></li>")
	}
	b.WriteString("</ul>")
	b.WriteString(`<script>(function(){var q=function(id){return document.getElementById(id)};var input=q('indexSearch');var list=q('indexList');if(!input||!list)return;input.addEventListener('input',function(e){var term=(e.target.value||'').toLowerCase();Array.prototype.forEach.call(list.children,function(li){var t=li.getAttribute('data-title')||'';li.style.display = t.indexOf(term)!==-1 ? '' : 'none';});});})();</script>`)
	templ.Handler(docsPage("Specifications", "specs", items, "", b.String())).ServeHTTP(w, r)
}

// /dev/docs/specs/{slug}
func DevDocsSpecFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	items, _ := listDocs("docs/specs", "SPEC_*.md", "SPEC_")
	var fp, title string
	for _, it := range items {
		if it.Slug == slug {
			fp, title = it.Path, it.Title
			break
		}
	}
	if fp == "" {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(fp)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Avoid showing SPEC_ in top-level heading if present and prettify the title word.
	if bytes.HasPrefix(b, []byte("# SPEC_")) {
		// Find first line
		nl := bytes.IndexByte(b, '\n')
		var first, rest []byte
		if nl == -1 {
			first, rest = b, nil
		} else {
			first, rest = b[:nl], b[nl:]
		}
		// first like: "# SPEC_FOO_BAR"
		t := strings.TrimPrefix(string(first), "# SPEC_")
		t = strings.ReplaceAll(t, "_", " ")
		t = strings.ReplaceAll(t, "-", " ")
		parts := strings.Fields(t)
		for i := range parts {
			if len(parts[i]) > 0 {
				parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
			}
		}
		pretty := "# " + strings.Join(parts, " ")
		if rest != nil {
			b = append([]byte(pretty), rest...)
		} else {
			b = []byte(pretty)
		}
	}
	html, _ := renderMarkdown(b)
	templ.Handler(docsPage(title, "specs", items, slug, html)).ServeHTTP(w, r)
}
