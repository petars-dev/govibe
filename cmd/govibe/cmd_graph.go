package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/dbexplorer"
	"github.com/petars-dev/govibe/internal/devtools"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/kernel"
	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

func init() {
	graphCmd.AddCommand(graphIndexCmd)
	rootCmd.AddCommand(graphCmd)

	f := graphIndexCmd.Flags()
	f.String("root", ".", "repository root: the directory holding go.mod")
	f.String("dir", graphdb.DefaultDir, "directory of the graph store, default the profile's graph.dir under --root (a scratch directory is fine, and needed while a dev server holds the default one)")
	f.Bool("calls", false, "also index which function calls which (type-checks the module; needs the Go toolchain)")
	f.Bool("git", false, "also index last commit, churn and co-change from `git log` (CI needs the full history: fetch-depth 0)")
	f.String("fail-on", "none", "exit 1 when there is a finding at or above this severity that the baseline does not accept: error, warning, info or none")
	f.String("baseline", "", "file of accepted findings (see --update-baseline); with --fail-on, only findings it does not list fail")
	f.Bool("update-baseline", false, "write the findings at or above --fail-on (default error) to --baseline and exit 0; findings new to the baseline need --reason")
	f.String("reason", "", "with --update-baseline: why the findings that are new to the baseline are accepted instead of fixed (kept per entry, with the date)")
	f.Bool("no-inspect", false, "do not run the app's `inspect --json` for its routes: index without a route table (the route rules are then not run)")
	f.String("format", "text", "output: text, json, or github (text plus workflow annotations for the new findings)")
	f.Bool("verbose", false, "log every layer as it is indexed")
	graphIndexCmd.SilenceUsage = true
	graphIndexCmd.SilenceErrors = true
}

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Work with the project graph (the model of this codebase behind /dev/graph and the graph_* MCP tools)",
}

var graphIndexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index the project into a graph store and report its findings",
	Long: `Index the project (code, templates, config, MCP tools, the database schema from the
migrations, docs and specs, and the findings derived from them) into a graph store,
print how it went, and optionally fail on findings. It is the offline counterpart of
the dev server's boot indexer and of POST /dev/graph/reindex, for when no server
holds the store's lock, and for CI with --dir on a scratch directory.

A project that depends on the framework is an application: its routes come from its
own binary (go run -tags govibe_dev . inspect --json: no database, no dev tools, no
scheduler), so the route rules run offline. That compiles and runs the project's own code
(with a scrubbed environment: no secret of the job reaches it), so index a project you do not trust with --no-inspect. Any other project has no router, so the
routes layer is skipped and the rules that need it are reported as not run. With
--calls, the call graph is indexed too. The graph covers the project's own code; what
it takes from the framework is labelled external, and the framework's own settings
and tools come from this tool's manifest (the tool and the library are one version).

Exit status: 0 on success, 1 when --fail-on finds a new finding, 2 when indexing
itself failed (a layer failed, the store is locked, a bad flag).`,
	Example: `  # what is wrong with the project, in a scratch store
  govibe graph index --dir /tmp/govibe-graph --calls

  # CI: fail on any error finding that docs/graph/baseline.json does not accept
  govibe graph index --dir "$RUNNER_TEMP/graph" --calls --git --fail-on error --baseline docs/graph/baseline.json --format github

  # accept what is there, saying why (each entry keeps its reason and date)
  govibe graph index --dir /tmp/govibe-graph --baseline docs/graph/baseline.json --update-baseline --reason "legacy handler, replaced in Q4"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		o := graphIndexOptions{}
		f := cmd.Flags()
		o.root, _ = f.GetString("root")
		o.dir, _ = f.GetString("dir")
		o.calls, _ = f.GetBool("calls")
		o.git, _ = f.GetBool("git")
		o.failOn, _ = f.GetString("fail-on")
		o.baseline, _ = f.GetString("baseline")
		o.updateBaseline, _ = f.GetBool("update-baseline")
		o.reason, _ = f.GetString("reason")
		o.noInspect, _ = f.GetBool("no-inspect")
		o.format, _ = f.GetString("format")
		if !f.Changed("dir") {
			// The store lives where the project's profile says, under its root. A profile that
			// does not load is reported now: nothing may be written in the wrong place first.
			prof, err := profile.Load(o.root)
			if err != nil {
				return &exitError{code: 2, msg: "graph index: " + err.Error()}
			}
			o.dir = filepath.Join(o.root, prof.Graph.Dir)
		}
		verbose, _ := f.GetBool("verbose")
		if verbose {
			logrus.SetLevel(logrus.InfoLevel)
		} else {
			logrus.SetLevel(logrus.ErrorLevel) // the report below says what the log would
		}
		o.openSchema = func() (func(context.Context) (*dbexplorer.Snapshot, error), func(), string) {
			return schemaFromConfig(o.root)
		}
		err := runGraphIndex(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		var ee *exitError
		if err != nil && !errors.As(err, &ee) {
			msg := err.Error()
			if !strings.HasPrefix(msg, "graph index:") {
				msg = "graph index: " + msg
			}
			return &exitError{code: 2, msg: msg}
		}
		return err
	},
}

// exitError ends the program with a status. The report has already been printed;
// msg, when there is one, goes to stderr.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// graphIndexOptions are the flags of `govibe graph index`.
type graphIndexOptions struct {
	root, dir      string
	calls, git     bool
	failOn         string
	baseline       string
	updateBaseline bool
	reason         string // why the findings new to the baseline are accepted
	noInspect      bool   // do not ask the app for its routes
	format         string

	// inspect asks the application for the facts only its binary knows (its routes and
	// who registered them, the modules it was built from); nil runs it with `go run`.
	inspect func(ctx context.Context, root string) (*routeinfo.Inspect, error)

	// openSchema opens the live database's schema reader, when the configuration has
	// a database; nil, or a nil result, means the migrations alone. close releases it.
	openSchema func() (schema func(context.Context) (*dbexplorer.Snapshot, error), closer func(), note string)
}

// schemaFromConfig reads the live schema when MARIADB_DSN is set, as the dev
// server does, so a local run sees drift and unapplied migrations too. The
// configuration is the project's own env.yml, read from root: the tool is not the
// app, so it has no embedded copy. A database that cannot be reached is a note, not
// a failure: the layer falls back to the migrations, as it does in the server.
func schemaFromConfig(root string) (func(context.Context) (*dbexplorer.Snapshot, error), func(), string) {
	cfg, err := config.Load(os.DirFS(root))
	if errors.Is(err, fs.ErrNotExist) {
		// A project without a root env.yml (it predates the split, or relies on the
		// framework defaults) still has MARIADB_DSN from the environment.
		cfg, err = config.Load(nil)
	}
	if err != nil || cfg.Database.MariaDB.DSN == "" {
		return nil, nil, ""
	}
	dbx, err := kernel.OpenDB(cfg.Database.MariaDB.DSN)
	if err != nil || dbx == nil {
		return nil, nil, "MARIADB_DSN is set but the database could not be opened, so the schema is read from the migrations alone"
	}
	ex := dbexplorer.New(dbx, dbexplorer.Options{})
	return ex.Snapshot, func() { _ = dbx.Close() }, ""
}

func runGraphIndex(ctx context.Context, o graphIndexOptions, out, errOut io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	switch o.format {
	case "text", "json", "github":
	default:
		return fmt.Errorf("unknown --format %q: use text, json or github", o.format)
	}
	if _, err := codegraph.AtOrAbove(nil, o.failOn); err != nil {
		return fmt.Errorf("--fail-on: %w", err)
	}
	gating := o.failOn != "" && o.failOn != "none"
	if o.updateBaseline && o.baseline == "" {
		return errors.New("--update-baseline needs --baseline FILE")
	}
	var baseline map[string]bool
	var noReason []string
	if o.baseline != "" && !o.updateBaseline {
		b, err := codegraph.LoadBaseline(o.baseline)
		if err != nil {
			return err
		}
		baseline, noReason = b.Accepted(), b.WithoutReason()
	}

	if err := os.MkdirAll(o.dir, 0o755); err != nil {
		return fmt.Errorf("graph store directory: %w", err)
	}
	h, err := graphdb.Open(o.dir)
	if err != nil {
		return fmt.Errorf("%w\nthe store at %s is probably held by a running dev server (it keeps the file locked): "+
			"POST /dev/graph/reindex there instead, or give --dir a scratch directory", err, o.dir)
	}
	defer h.Close()

	opts, routesNote, err := o.indexerOptions(ctx)
	if err != nil {
		return err
	}
	var dbNote string
	if o.openSchema != nil {
		var closer func()
		opts.Schema, closer, dbNote = o.openSchema()
		if closer != nil {
			defer closer()
		}
	}
	ix := codegraph.New(h.QuadStore, opts)
	rep, err := ix.Run(ctx)
	if err != nil {
		return err
	}
	m, err := ix.Model(ctx)
	if err != nil {
		return err
	}

	failOn := o.failOn
	if o.updateBaseline && !gating {
		failOn = "error" // a baseline of nothing would accept nothing
	}
	var gate *codegraph.GateResult
	if !rep.Failed() {
		if gate, err = m.Gate(codegraph.GateOptions{FailOn: failOn, Baseline: baseline, NoReason: noReason}); err != nil {
			return err
		}
	}

	r := &graphReport{Root: ix.RootDir(), Store: o.dir, Report: rep, Gate: gate, DBNote: dbNote, RoutesNote: routesNote, Baseline: o.baseline}
	switch {
	case o.updateBaseline && gate != nil:
		list, _ := codegraph.AtOrAbove(m.AllFindings(), failOn)
		n, err := codegraph.WriteBaseline(o.baseline, list, o.reason)
		if err != nil {
			return &exitError{code: 2, msg: "graph index: " + err.Error()}
		}
		r.BaselineWritten = n
	}
	if err := r.write(out, o.format); err != nil {
		return err
	}

	switch {
	case rep.Failed():
		return &exitError{code: 2, msg: "graph index: " + failedLayers(rep)}
	case !o.updateBaseline && gating && len(gate.New) > 0:
		return &exitError{code: 1, msg: fmt.Sprintf("graph index: %d new %s at or above %q (accept them with --update-baseline --reason \"...\", or fix them)",
			len(gate.New), plural(len(gate.New), "finding", "findings"), gate.FailOn)}
	case !o.updateBaseline && gating && len(gate.NoReason) > 0:
		return &exitError{code: 1, msg: fmt.Sprintf("graph index: %d baseline %s no reason (an accepted finding says why: add a \"reason\" to each entry of %s)",
			len(gate.NoReason), plural(len(gate.NoReason), "entry has", "entries have"), o.baseline)}
	}
	return nil
}

func failedLayers(rep *codegraph.Report) string {
	var parts []string
	for _, l := range rep.Layers {
		if l.Status == "error" {
			parts = append(parts, l.Name+": "+l.Error)
		}
	}
	return "a layer failed: " + strings.Join(parts, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// graphReport is what a run prints.
type graphReport struct {
	Root            string                `json:"root"`
	Store           string                `json:"store"`
	Report          *codegraph.Report     `json:"report"`
	Gate            *codegraph.GateResult `json:"gate,omitempty"`
	DBNote          string                `json:"database_note,omitempty"`
	RoutesNote      string                `json:"routes_note,omitempty"`
	Baseline        string                `json:"baseline,omitempty"`
	BaselineWritten int                   `json:"baseline_written,omitempty"`
}

func (r *graphReport) write(w io.Writer, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Fprintf(w, "graph index: %s\n", r.Root)
	fmt.Fprintf(w, "store: %s\n\n", r.Store)
	fmt.Fprintf(w, "%-10s %-8s %7s  %s\n", "layer", "status", "quads", "note")
	for _, l := range r.Report.Layers {
		note := l.Note
		if l.Error != "" {
			note = l.Error
		} else if len(l.Warnings) > 0 {
			note = fmt.Sprintf("%d skipped: %s", len(l.Warnings), firstOf(l.Warnings))
		}
		fmt.Fprintf(w, "%-10s %-8s %7d  %s\n", l.Name, l.Status, l.Quads, note)
	}
	fmt.Fprintf(w, "\n%d quads in %d ms", r.Report.Quads, r.Report.Build.DurationMS)
	if r.Report.Build.GitSHA != "" {
		fmt.Fprintf(w, " at %s", r.Report.Build.GitSHA)
		if r.Report.Build.Dirty {
			fmt.Fprint(w, "+dirty")
		}
	}
	fmt.Fprintln(w)
	if r.DBNote != "" {
		fmt.Fprintf(w, "note: %s\n", r.DBNote)
	}
	if r.RoutesNote != "" {
		fmt.Fprintf(w, "routes: %s\n", r.RoutesNote)
	}
	if r.Gate == nil {
		return nil
	}

	g := r.Gate
	fmt.Fprintf(w, "\nfindings: %d error, %d warning, %d info\n", g.BySeverity["error"], g.BySeverity["warning"], g.BySeverity["info"])
	if len(g.NotRun) > 0 {
		var names []string
		for _, n := range g.NotRun {
			names = append(names, n.Name)
		}
		fmt.Fprintf(w, "rules that did not run (no finding from them is not a clean bill): %s\n", strings.Join(names, ", "))
	}
	if r.BaselineWritten > 0 {
		fmt.Fprintf(w, "baseline: wrote %d %s to %s\n", r.BaselineWritten, plural(r.BaselineWritten, "finding", "findings"), r.Baseline)
	}
	if g.FailOn == "none" || r.BaselineWritten > 0 {
		return nil // nothing to judge: no threshold, or the baseline was just made from what there is
	}
	if len(g.NoReason) > 0 {
		fmt.Fprintf(w, "baseline entries with no reason (an accepted finding says why): %s\n", strings.Join(g.NoReason, ", "))
	}
	fmt.Fprintf(w, "gate (fail on %s): %d new, %d accepted by the baseline", g.FailOn, len(g.New), len(g.Accepted))
	if len(g.Fixed) > 0 {
		fmt.Fprintf(w, ", %d baseline %s no longer found (fixed: drop them with --update-baseline)", len(g.Fixed), plural(len(g.Fixed), "entry", "entries"))
	}
	fmt.Fprintln(w)
	for _, f := range g.New {
		fmt.Fprintf(w, "  NEW %-7s %-22s %s%s\n", severityOf(f), f.Rule, place(f), f.Message)
		if format == "github" {
			fmt.Fprintf(w, "::%s %s::%s\n", annotationKind(f.Severity), annotationProps(f), escapeAnnotation(f.Message))
		}
	}
	return nil
}

func severityOf(f codegraph.FindingRef) string { return strings.ToLower(f.Severity) }

func place(f codegraph.FindingRef) string {
	switch {
	case f.Path != "" && f.Line > 0:
		return fmt.Sprintf("%s:%d: ", f.Path, f.Line)
	case f.Path != "":
		return f.Path + ": "
	}
	return ""
}

func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// annotationKind maps a severity to a GitHub Actions workflow command.
func annotationKind(sev string) string {
	switch strings.ToLower(sev) {
	case "error", "high", "critical":
		return "error"
	case "warning", "warn", "medium":
		return "warning"
	}
	return "notice"
}

func annotationProps(f codegraph.FindingRef) string {
	var props []string
	if f.Path != "" {
		props = append(props, "file="+escapeProperty(filepath.ToSlash(f.Path)))
		if f.Line > 0 {
			props = append(props, fmt.Sprintf("line=%d", f.Line))
		}
	}
	props = append(props, "title="+escapeProperty(f.Rule))
	return strings.Join(props, ",")
}

// escapeAnnotation and escapeProperty are the escapes GitHub's workflow commands
// need so that a message cannot end its own command or start another.
func escapeAnnotation(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// indexerOptions are the options of the indexer for the project at o.root: for an
// application (a project that depends on the framework) what its own binary reports
// (the route table, who registered each route, the modules it was built from) and
// the framework's manifest, which names the framework's side of the graph. note says
// where the routes came from, or why there are none.
func (o graphIndexOptions) indexerOptions(ctx context.Context) (opts codegraph.Options, note string, err error) {
	app := codegraph.RequiresFramework(o.root)
	var version string
	switch {
	case !app:
		note = "none: this project does not depend on the framework, so it has no binary to ask"
	case o.noInspect:
		note = "none: --no-inspect (the route rules do not run)"
	default:
		run := o.inspect
		if run == nil {
			run = inspectApp
		}
		doc, err := run(ctx, o.root)
		if err != nil {
			return opts, "", fmt.Errorf("%w\nthe application could not be asked for its routes: fix the build, or index without them with --no-inspect", err)
		}
		opts = codegraph.OptionsFromInspect(doc)
		version = doc.Framework.Version
		note = fmt.Sprintf("%d from the application's own `inspect --json` (%d registered by the framework)", len(doc.Routes), countMountedBy(doc, routeinfo.MountedByFramework))
	}
	if app {
		mf, err := devtools.FrameworkManifest(ctx, version)
		if err != nil {
			return opts, "", fmt.Errorf("the framework manifest: %w", err)
		}
		opts.Manifest = mf
	}
	opts.Root, opts.Calls, opts.Git = o.root, o.calls, o.git
	return opts, note, nil
}

func countMountedBy(doc *routeinfo.Inspect, who string) int {
	n := 0
	for _, r := range doc.Routes {
		if r.MountedBy == who {
			n++
		}
	}
	return n
}

// inspectApp runs the application's own `inspect --json`: the facts only its binary
// knows, with no database, no dev tools mounted and no scheduler, so it needs no
// environment. The dev tag is passed so that files tagged for development are part
// of what is asked about.
func inspectApp(ctx context.Context, root string) (*routeinfo.Inspect, error) {
	c := exec.CommandContext(ctx, "go", "run", "-tags", devTags, ".", "inspect", "--json")
	c.Dir = root
	// The binary needs no settings from the environment to say what it declares (that is what
	// `inspect` is for), so it does not get any: no secret an analysis job holds reaches it.
	c.Env = inspectEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("`go run -tags %s . inspect --json` in %s: %w\n%s", devTags, root, err, strings.TrimSpace(lastLines(stderr.String(), 15)))
	}
	return routeinfo.ParseInspect(stdout.Bytes())
}

// inspectEnv keeps what the Go toolchain and a process need to run (PATH, HOME, temp
// directories, locale, GO* and the variables a Windows toolchain wants) and drops the rest.
func inspectEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "GO"), strings.HasPrefix(name, "LC_"), strings.HasPrefix(name, "CGO_"):
		case name == "PATH", name == "HOME", name == "USER", name == "LANG", name == "TMPDIR", name == "TEMP", name == "TMP",
			name == "SystemRoot", name == "SYSTEMROOT", name == "USERPROFILE", name == "LOCALAPPDATA", name == "APPDATA", name == "XDG_CACHE_HOME",
			name == "SSL_CERT_FILE", name == "SSL_CERT_DIR", name == "HTTPS_PROXY", name == "HTTP_PROXY", name == "NO_PROXY":
		default:
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
