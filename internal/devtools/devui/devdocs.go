package devui

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

	"github.com/petars-dev/govibe/internal/frameworkdocs"
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
func docsPage(title string, section string, nav []DocItem, activeSlug string, contentHTML string, groups ...navGroup) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
		_, _ = io.WriteString(w, "<title>"+templ.EscapeString(title)+"</title>")
		_, _ = io.WriteString(w, "<link rel=\"icon\" href=\"/_govibe/static/govibe-logo.png\">")
		_, _ = io.WriteString(w, "<link rel=\"stylesheet\" href=\"/_govibe/static/theme.css\">")
		_, _ = io.WriteString(w, "<link rel=\"stylesheet\" href=\"/_govibe/static/docs.css\">")
		// Per-theme syntax highlighting palettes (dark: dracula, light: github),
		// scoped so they follow the same html.theme-* toggle as the rest of the page.
		_, _ = io.WriteString(w, "<style>"+highlightCSSOnce()+"</style>")
		// Theme toggle bootstraps before first paint. Default to dark, persisted in localStorage.
		_, _ = io.WriteString(w, "<script src=\"/_govibe/static/theme.js\"></script>")
		_, _ = io.WriteString(w, "</head><body class=\"docs-body\">")
		// Top header with brand, tabs, and theme toggle with icons
		secLabel := sectionLabel(section)
		_, _ = io.WriteString(w, "<header class=\"site-header\">")
		_, _ = io.WriteString(w, "<div class=\"brand\"><a class=\"brand-link\" href=\"/\"><img class=\"brand-logo\" src=\"/_govibe/static/govibe-logo.png\" alt=\"GoVibe\"></a><span class=\"brand-name\">GoVibe Dev Docs</span></div>")
		_, _ = io.WriteString(w, "<nav class=\"top-tabs\"><a class=\"tab\" href=\"/dev\">Dev</a><a class=\"tab"+boolClass(section == "app", " active")+"\" href=\"/dev/docs/app\">Docs</a><a class=\"tab"+boolClass(section == "specs", " active")+"\" href=\"/dev/docs/specs\">Specs</a>"+frameworkTab(section, groups)+"<a class=\"tab\" href=\"/dev/graph\">Graph</a><a class=\"tab\" href=\"/dev/db\">DB</a><a class=\"tab\" href=\"/dev/schedule\">Schedule</a></nav>")
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
			_, _ = io.WriteString(w, "</ul></nav>")
			// The framework's docs come after the app's own, as a separate, muted group.
			for _, g := range groups {
				writeNavGroup(w, g, section == g.Section, activeSlug)
			}
			_, _ = io.WriteString(w, "</aside>")
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

// navGroup is a group of docs in the sidebar. The framework's come after the app's own, muted.
type navGroup struct {
	Title   string
	Section string // the URL section: govibe or govibe-specs
	Items   []DocItem
}

// sectionLabel is how a docs section is named in the page.
func sectionLabel(section string) string {
	switch section {
	case "specs":
		return "Specs"
	case "govibe":
		return "GoVibe docs"
	case "govibe-specs":
		return "GoVibe specs (contributors)"
	}
	return "Docs"
}

// frameworkTab is the tab that leads to the framework's docs, muted next to the app's own.
func frameworkTab(section string, groups []navGroup) string {
	for _, g := range groups {
		if g.Section == "govibe" || section == "govibe" {
			return "<a class=\"tab tab-muted" + boolClass(section == "govibe", " active") + "\" href=\"/dev/docs/govibe\">GoVibe</a>"
		}
	}
	if section == "govibe" {
		return "<a class=\"tab tab-muted active\" href=\"/dev/docs/govibe\">GoVibe</a>"
	}
	return ""
}

// writeNavGroup writes one muted sidebar group of framework docs.
func writeNavGroup(w io.Writer, g navGroup, current bool, active string) {
	_, _ = io.WriteString(w, "<div class=\"sidebar-group sidebar-muted\"><div class=\"sidebar-title\">"+templ.EscapeString(g.Title)+"</div><ul class=\"nav-list\">")
	for _, it := range g.Items {
		cls := "nav-link"
		if current && it.Slug == active {
			cls += " active"
		}
		_, _ = io.WriteString(w, "<li><a class=\""+cls+"\" href=\"/dev/docs/"+g.Section+"/"+it.Slug+"\">"+templ.EscapeString(it.Title)+"</a></li>")
	}
	_, _ = io.WriteString(w, "</ul></div>")
}

// setItems lists the docs of a framework set as nav items (their titles carry the prefix).
func setItems(set frameworkdocs.Set) []DocItem {
	if set == nil {
		return nil
	}
	var items []DocItem
	for _, d := range set.List() {
		items = append(items, DocItem{Title: d.Title, Slug: d.Slug})
	}
	return items
}

// frameworkGroups are the groups of framework docs a page shows after the app's own, except
// the one the page is itself.
func frameworkGroups(d Docs, exclude string) []navGroup {
	var groups []navGroup
	if items := setItems(d.Framework); len(items) > 0 && exclude != "govibe" {
		groups = append(groups, navGroup{Title: "GoVibe docs (the framework)", Section: "govibe", Items: items})
	}
	if items := setItems(d.FrameworkSpecs); len(items) > 0 && exclude != "govibe-specs" {
		groups = append(groups, navGroup{Title: "GoVibe specs (contributors)", Section: "govibe-specs", Items: items})
	}
	return groups
}

const indexSearchScript = `<script>(function(){var q=function(id){return document.getElementById(id)};var input=q('indexSearch');var list=q('indexList');if(!input||!list)return;input.addEventListener('input',function(e){var term=(e.target.value||'').toLowerCase();Array.prototype.forEach.call(list.children,function(li){var t=li.getAttribute('data-title')||'';li.style.display = t.indexOf(term)!==-1 ? '' : 'none';});});})();</script>`

// indexHTML is an index page: a searchable list of the section's docs, then (muted) what the
// framework offers.
func indexHTML(heading, section string, items []DocItem, groups []navGroup, empty string) string {
	var b strings.Builder
	b.WriteString("<h1>" + templ.EscapeString(heading) + "</h1><input id=\"indexSearch\" class=\"search-input\" type=\"search\" placeholder=\"Search titles...\" aria-label=\"Search titles\"><ul id=\"indexList\" class=\"index-list\">")
	for _, it := range items {
		b.WriteString("<li data-title=\"" + strings.ToLower(templ.EscapeString(it.Title)) + "\"><a href=\"/dev/docs/" + section + "/" + it.Slug + "\">" + templ.EscapeString(it.Title) + "</a></li>")
	}
	b.WriteString("</ul>")
	if len(items) == 0 && empty != "" {
		b.WriteString("<p class=\"muted\">" + templ.EscapeString(empty) + "</p>")
	}
	b.WriteString(indexSearchScript)
	for _, g := range groups {
		b.WriteString("<section class=\"index-muted\"><h2>" + templ.EscapeString(g.Title) + "</h2><ul class=\"index-list\">")
		for _, it := range g.Items {
			b.WriteString("<li><a href=\"/dev/docs/" + g.Section + "/" + it.Slug + "\">" + templ.EscapeString(it.Title) + "</a></li>")
		}
		b.WriteString("</ul></section>")
	}
	return b.String()
}

// appDocs lists the application's public docs: every .md directly in the profile's doc
// directories.
func appDocs(d Docs) []DocItem {
	var items []DocItem
	for _, dir := range d.Dirs {
		got, _ := listDocs(d.docsDir(dir), "*.md", "")
		items = append(items, got...)
	}
	return items
}

// specDocs lists the application's specs: the prefixed files of the specs directory.
func specDocs(d Docs) []DocItem {
	if d.Specs == "" || d.SpecPrefix == "" {
		return nil // no specs directory, or no prefix that makes a file a spec: the profile says there are none
	}
	items, _ := listDocs(d.docsDir(d.Specs), d.SpecPrefix+"*.md", d.SpecPrefix)
	return items
}

// DevDocsIndex is /dev/docs: it opens on the application's own docs.
func DevDocsIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dev/docs/app", http.StatusFound)
}

// /dev/docs/app index: the app's docs first, the framework's after them.
func DevDocsAppIndex(w http.ResponseWriter, r *http.Request) {
	d := docsOf(r)
	items := appDocs(d)
	groups := frameworkGroups(d, "app")
	templ.Handler(docsPage("Application Docs", "app", items, "", indexHTML("Application Docs", "app", items, groups, "This application has no docs yet: add Markdown files to docs/public."), groups...)).ServeHTTP(w, r)
}

// /dev/docs/app/{slug}
func DevDocsAppFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	d := docsOf(r)
	items := appDocs(d)
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
	templ.Handler(docsPage(title, "app", items, slug, html, frameworkGroups(d, "app")...)).ServeHTTP(w, r)
}

// /dev/docs/specs index (SPEC_*.md, slugs drop SPEC_ and are lowercase hyphenated)
func DevDocsSpecs(w http.ResponseWriter, r *http.Request) {
	d := docsOf(r)
	items := specDocs(d)
	groups := frameworkGroups(d, "specs")
	templ.Handler(docsPage("Specifications", "specs", items, "", indexHTML("Specifications", "specs", items, groups, "No specs yet: add a SPEC_<FEATURE>.md to docs/specs."), groups...)).ServeHTTP(w, r)
}

// /dev/docs/specs/{slug}
func DevDocsSpecFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	d := docsOf(r)
	items := specDocs(d)
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
	b = prettySpecHeading(b, d.SpecPrefix)
	html, _ := renderMarkdown(b)
	templ.Handler(docsPage(title, "specs", items, slug, html, frameworkGroups(d, "specs")...)).ServeHTTP(w, r)
}

// prettySpecHeading avoids showing the SPEC_ prefix in the top-level heading and prettifies the title words.
func prettySpecHeading(b []byte, prefix string) []byte {
	head := []byte("# " + prefix)
	if !bytes.HasPrefix(b, head) {
		return b
	}
	nl := bytes.IndexByte(b, '\n')
	var first, rest []byte
	if nl == -1 {
		first, rest = b, nil
	} else {
		first, rest = b[:nl], b[nl:]
	}
	t := strings.TrimPrefix(string(first), "# "+prefix)
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
		return append([]byte(pretty), rest...)
	}
	return []byte(pretty)
}

// serveFrameworkIndex serves the index of a set of framework docs under a section.
func serveFrameworkIndex(w http.ResponseWriter, r *http.Request, section string, set func(Docs) frameworkdocs.Set, heading string) {
	d := docsOf(r)
	s := set(d)
	if s == nil {
		http.NotFound(w, r)
		return
	}
	items := setItems(s)
	groups := frameworkGroups(d, section)
	templ.Handler(docsPage(heading, section, items, "", indexHTML(heading, section, items, groups, "Nothing here."), groups...)).ServeHTTP(w, r)
}

// serveFrameworkFile serves one doc of a set of framework docs.
func serveFrameworkFile(w http.ResponseWriter, r *http.Request, section string, set func(Docs) frameworkdocs.Set) {
	d := docsOf(r)
	s := set(d)
	slug := chi.URLParam(r, "slug")
	if s == nil || slug == "" {
		http.NotFound(w, r)
		return
	}
	var title string
	for _, it := range setItems(s) {
		if it.Slug == slug {
			title = it.Title
		}
	}
	text, err := s.Read(slug)
	if title == "" || err != nil {
		http.NotFound(w, r)
		return
	}
	html, _ := renderMarkdown([]byte(text))
	templ.Handler(docsPage(title, section, setItems(s), slug, html, frameworkGroups(d, section)...)).ServeHTTP(w, r)
}

// /dev/docs/govibe: the framework's reference docs, every title prefixed "GoVibe:".
func DevDocsFrameworkIndex(w http.ResponseWriter, r *http.Request) {
	serveFrameworkIndex(w, r, "govibe", func(d Docs) frameworkdocs.Set { return d.Framework }, "GoVibe Docs")
}

// /dev/docs/govibe/{slug}
func DevDocsFrameworkFile(w http.ResponseWriter, r *http.Request) {
	serveFrameworkFile(w, r, "govibe", func(d Docs) frameworkdocs.Set { return d.Framework })
}

// /dev/docs/govibe-specs: the framework's development specs, for contributors only (a
// checkout of the framework is configured); 404 in every application.
func DevDocsFrameworkSpecsIndex(w http.ResponseWriter, r *http.Request) {
	serveFrameworkIndex(w, r, "govibe-specs", func(d Docs) frameworkdocs.Set { return d.FrameworkSpecs }, "GoVibe Specs (contributors)")
}

// /dev/docs/govibe-specs/{slug}
func DevDocsFrameworkSpecFile(w http.ResponseWriter, r *http.Request) {
	serveFrameworkFile(w, r, "govibe-specs", func(d Docs) frameworkdocs.Set { return d.FrameworkSpecs })
}
