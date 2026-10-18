package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/generate"
	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/internal/rules"
	"github.com/petars-dev/govibe/internal/scaffold"
)

func init() {
	rootCmd.AddCommand(doctorCmd)
	f := doctorCmd.Flags()
	f.String("root", ".", "the project root: the directory holding go.mod")
	f.Bool("no-build", false, "skip the checks that build the app (inspect --json, the default build's dependency list)")
	doctorCmd.SilenceUsage = true
	doctorCmd.SilenceErrors = true
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the project's health: versions, managed files, embeds, specs, the baseline, the build",
	Long: `Report what is wrong with the project before it costs time: the tool's version against
the framework version go.mod pins, the managed files against their manifest, every spec
against the sections the rules require, the baseline's entries against the rule that
each says why, a script tag that loads from the network (htmx is served by the
framework), the markers the generators need, and (unless --no-build) the app's own
"inspect --json" run with an empty environment (an invalid schedule or a declaration
that opens a connection fails here, naming the command), static files or migrations
that exist but are not handed to the framework, and a dev package linked into the
default build.

Exit status: 0 when nothing fails, 1 on a failure, 2 when the doctor could not run.
"govibe check" runs the checks that need no build before the gate.`,
	Example: `  go tool govibe doctor
  go tool govibe doctor --no-build`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		root, _ := f.GetString("root")
		noBuild, _ := f.GetBool("no-build")
		err := runDoctor(cmd.Context(), doctorOptions{root: root, noBuild: noBuild}, cmd.OutOrStdout())
		var ee *exitError
		if err != nil && !errors.As(err, &ee) {
			return &exitError{code: 2, msg: "govibe doctor: " + err.Error()}
		}
		return err
	},
}

type doctorOptions struct {
	root    string
	noBuild bool

	inspect  func(ctx context.Context, root string) (*routeinfo.Inspect, error)
	listDeps func(ctx context.Context, root string) (string, error)
}

// diag is one line of the doctor's report.
type diag struct {
	Level   string // "ok", "note", "warn", "fail"
	Check   string
	Message string
}

func runDoctor(ctx context.Context, o doctorOptions, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	prof, err := profile.Load(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "govibe doctor: %s\n", root)
	diags := doctorStatic(root, prof)
	if !o.noBuild {
		diags = append(diags, doctorBuild(ctx, o, root, prof)...)
	}
	writeDiags(out, diags)
	counts := map[string]int{}
	for _, d := range diags {
		counts[d.Level]++
	}
	fmt.Fprintf(out, "\n%d checks: %d ok, %d notes, %d warnings, %d failures\n", len(diags), counts["ok"], counts["note"], counts["warn"], counts["fail"])
	if counts["fail"] > 0 {
		return &exitError{code: 1, msg: fmt.Sprintf("govibe doctor: %d %s", counts["fail"], plural(counts["fail"], "failure", "failures"))}
	}
	return nil
}

func writeDiags(out io.Writer, diags []diag) {
	for _, d := range diags {
		fmt.Fprintf(out, "  %-5s %-10s %s\n", d.Level, d.Check, d.Message)
	}
}

// doctorStatic are the checks that read files and need no build.
func doctorStatic(root string, prof profile.Profile) []diag {
	var ds []diag
	ds = append(ds, checkVersions(root, prof)...)
	ds = append(ds, checkManagedFiles(root, prof)...)
	ds = append(ds, checkSpecs(root, prof)...)
	ds = append(ds, checkBaseline(root, prof)...)
	ds = append(ds, checkMarkers(root)...)
	ds = append(ds, checkCDN(root, prof)...)
	return ds
}

// checkVersions compares the tool's version with the framework version go.mod pins.
func checkVersions(root string, prof profile.Profile) []diag {
	fw := prof.FrameworkModule()
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return []diag{{"fail", "go.mod", "not the root of a Go module (no go.mod)"}}
	}
	mf, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		return []diag{{"fail", "go.mod", err.Error()}}
	}
	tool := toolVersion()
	for _, r := range mf.Replace {
		if r.Old.Path == fw {
			return []diag{{"note", "framework", fmt.Sprintf("go.mod replaces %s with %s (a checkout); the tool is %s", fw, r.New.Path, tool)}}
		}
	}
	for _, r := range mf.Require {
		if r.Mod.Path != fw {
			continue
		}
		if r.Mod.Version == tool {
			return []diag{{"ok", "framework", fmt.Sprintf("%s %s, and the tool is the same version", fw, tool)}}
		}
		return []diag{{"warn", "framework", fmt.Sprintf("go.mod pins %s %s but this tool is %s: run it as `go tool govibe`, so the tool that judges the project is the version it builds against", fw, r.Mod.Version, tool)}}
	}
	return []diag{{"note", "framework", fmt.Sprintf("go.mod does not require %s: not an application of the framework (the app checks do not apply)", fw)}}
}

// checkManagedFiles reports the managed files against the manifest.
func checkManagedFiles(root string, prof profile.Profile) []diag {
	if prof.Managed == nil {
		return []diag{{"note", "managed", "none (no manifest in govibe.yml: the project was not generated by govibe new)"}}
	}
	drift := scaffold.CheckManaged(root, prof.Managed)
	if len(drift) == 0 {
		return []diag{{"ok", "managed", fmt.Sprintf("%d files as generated (framework %s)", len(prof.Managed.Files), prof.Managed.Version)}}
	}
	var ds []diag
	for _, d := range drift {
		level := "note"
		if d.State == "missing" || d.State == "unreadable" {
			level = "warn"
		}
		ds = append(ds, diag{level, "managed", d.String()})
	}
	return ds
}

var headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)

// checkSpecs reports a spec that lacks one of the required sections.
func checkSpecs(root string, prof profile.Profile) []diag {
	if prof.Docs.Specs == "" {
		return nil
	}
	dir := filepath.Join(root, filepath.FromSlash(prof.Docs.Specs))
	matches, _ := filepath.Glob(filepath.Join(dir, prof.Docs.SpecPrefix+"*.md"))
	sort.Strings(matches)
	if len(matches) == 0 {
		return []diag{{"ok", "specs", fmt.Sprintf("none yet in %s (govibe make spec <name> writes one)", prof.Docs.Specs)}}
	}
	var ds []diag
	bad := 0
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var headings []string
		for _, h := range headingRe.FindAllStringSubmatch(string(b), -1) {
			headings = append(headings, h[1])
		}
		rel, _ := filepath.Rel(root, m)
		if missing := rules.MissingSpecSections(headings); len(missing) > 0 {
			bad++
			ds = append(ds, diag{"warn", "specs", fmt.Sprintf("%s lacks the sections: %s", filepath.ToSlash(rel), strings.Join(missing, ", "))})
		}
	}
	if bad == 0 {
		ds = append(ds, diag{"ok", "specs", fmt.Sprintf("%d with every required section", len(matches))})
	}
	return ds
}

// checkBaseline reports a baseline entry that says no reason.
func checkBaseline(root string, prof profile.Profile) []diag {
	p := filepath.Join(root, filepath.FromSlash(prof.Graph.Dir), "baseline.json")
	if !fileExists(p) {
		return []diag{{"note", "baseline", "no " + filepath.ToSlash(filepath.Join(prof.Graph.Dir, "baseline.json")) + ": the gate accepts no finding"}}
	}
	b, err := codegraph.LoadBaseline(p)
	if err != nil {
		return []diag{{"fail", "baseline", err.Error()}}
	}
	if nr := b.WithoutReason(); len(nr) > 0 {
		return []diag{{"fail", "baseline", fmt.Sprintf("%d %s no reason (the gate fails on them): %s", len(nr), plural(len(nr), "entry says", "entries say"), strings.Join(nr, ", "))}}
	}
	return []diag{{"ok", "baseline", fmt.Sprintf("%d accepted %s, each with a reason", len(b.Entries), plural(len(b.Entries), "finding", "findings"))}}
}

// checkMarkers reports whether the generators' markers are in the wiring.
func checkMarkers(root string) []diag {
	p, err := generate.Load(root)
	if err != nil {
		return nil
	}
	var ds []diag
	for _, m := range []struct{ marker, what string }{{generate.RoutesMarker, "a route"}, {generate.CommandsMarker, "a command"}} {
		if file, _ := p.MarkerFile(m.marker); file != "" {
			ds = append(ds, diag{"ok", "markers", fmt.Sprintf("%s in %s", m.marker, file)})
		} else {
			ds = append(ds, diag{"note", "markers", fmt.Sprintf("no %s in the wiring: govibe make prints the snippet for %s instead of inserting it", m.marker, m.what)})
		}
	}
	return ds
}

var cdnScriptRe = regexp.MustCompile(`(?i)<script[^>]+src\s*=\s*["']https?://[^"']*["']`)

// checkCDN reports a template that loads a script from the network.
func checkCDN(root string, prof profile.Profile) []diag {
	dirs := prof.Layout["views"]
	if len(dirs) == 0 {
		dirs = []string{"assets"}
	}
	var hits []string
	for _, d := range dirs {
		filepath.WalkDir(filepath.Join(root, filepath.FromSlash(d)), func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() || !(strings.HasSuffix(p, ".templ") || strings.HasSuffix(p, ".html")) {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			if m := cdnScriptRe.Find(b); m != nil {
				rel, _ := filepath.Rel(root, p)
				hits = append(hits, filepath.ToSlash(rel)+": "+string(m))
			}
			return nil
		})
	}
	if len(hits) == 0 {
		return []diag{{"ok", "scripts", "no template loads a script from the network (htmx comes from web.HTMXScript)"}}
	}
	var ds []diag
	for _, h := range hits {
		ds = append(ds, diag{"warn", "scripts", h + " (vendor it under assets, or use web.HTMXScript for htmx: one binary, no CDN)"})
	}
	return ds
}

// doctorBuild are the checks that build the app: inspect --json with no environment,
// the embeds it reports, and the default build's dependency list.
func doctorBuild(ctx context.Context, o doctorOptions, root string, prof profile.Profile) []diag {
	if !codegraph.RequiresFramework(root) {
		return nil
	}
	var ds []diag
	inspect := o.inspect
	if inspect == nil {
		inspect = inspectApp
	}
	doc, err := inspect(ctx, root)
	if err != nil {
		ds = append(ds, diag{"fail", "inspect", "the app could not say what it declares (inspect --json, with an empty environment): " + firstLine(err.Error()) + "\n" + indent(rest(err.Error()), "        ")})
	} else {
		scheduled := 0
		for _, c := range doc.Commands {
			if c.Schedule != nil {
				scheduled++
			}
		}
		ds = append(ds, diag{"ok", "inspect", fmt.Sprintf("the app declares %d routes and %d commands (%d scheduled) with no environment", countMountedBy(doc, routeinfo.MountedByApp), len(doc.Commands), scheduled)})
		ds = append(ds, checkEmbeds(root, prof, doc)...)
	}
	list := o.listDeps
	if list == nil {
		list = listDefaultDeps
	}
	deps, err := list(ctx, root)
	switch {
	case err != nil:
		ds = append(ds, diag{"fail", "build", "the default build's dependencies could not be listed: " + firstLine(err.Error())})
	default:
		var linked []string
		for _, d := range strings.Fields(deps) {
			if devOnlyRe.MatchString(d) {
				linked = append(linked, d)
			}
		}
		if len(linked) > 0 {
			ds = append(ds, diag{"fail", "build", fmt.Sprintf("the default build links dev tooling: %s (only a govibe_dev build may; a file that imports it needs the build tag)", strings.Join(linked, ", "))})
		} else {
			ds = append(ds, diag{"ok", "build", "the default build links no dev tooling"})
		}
	}
	return ds
}

// devOnlyRe names what only the dev tools may link (the keystone and the demo's
// prod_test.go say the same).
var devOnlyRe = regexp.MustCompile(`cayleygraph|modelcontextprotocol|golang\.org/x/tools|yuin/goldmark|alecthomas/chroma|petars-dev/govibe/internal/(devtools|codegraph|dbexplorer|devmcp|graphdb)`)

// checkEmbeds reports static files or migrations on disk that the app does not hand to
// the framework.
func checkEmbeds(root string, prof profile.Profile, doc *routeinfo.Inspect) []diag {
	var ds []diag
	views := "assets"
	if d := prof.Layout["views"]; len(d) > 0 {
		views = d[0]
	}
	if n := countFiles(filepath.Join(root, filepath.FromSlash(views), "static")); n > 0 && !doc.App.Contributes.Assets {
		ds = append(ds, diag{"warn", "embeds", fmt.Sprintf("%s/static holds %d %s but App.Assets is nil: they are not embedded or served", views, n, plural(n, "file", "files"))})
	}
	if prof.Migrations != "" {
		if n := countFiles(filepath.Join(root, filepath.FromSlash(prof.Migrations))); n > 0 && !doc.App.Contributes.Migrations {
			ds = append(ds, diag{"warn", "embeds", fmt.Sprintf("%s holds %d %s but App.Migrations is nil: they never run", prof.Migrations, n, plural(n, "migration", "migrations"))})
		}
	}
	if !doc.App.Contributes.Config {
		ds = append(ds, diag{"warn", "embeds", "App.Config is nil: env.yml is not embedded, the framework's defaults alone apply"})
	}
	if len(ds) == 0 {
		ds = append(ds, diag{"ok", "embeds", "the settings, the migrations and the static files are handed to the framework"})
	}
	return ds
}

// countFiles counts the files in a directory tree, .gitkeep files aside.
func countFiles(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != ".gitkeep" {
			n++
		}
		return nil
	})
	return n
}

// listDefaultDeps is `go list -deps -tags ” .` in root: what a production build links.
func listDefaultDeps(ctx context.Context, root string) (string, error) {
	c := exec.CommandContext(ctx, "go", "list", "-deps", "-tags", "", ".")
	c.Dir = root
	c.Env = inspectEnv(os.Environ())
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, lastLines(string(out), 10))
	}
	return string(out), nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func rest(s string) string {
	_, r, _ := strings.Cut(s, "\n")
	return r
}
