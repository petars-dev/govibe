package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petars-dev/govibe/internal/generate"
)

func init() {
	rootCmd.AddCommand(makeCmd)
	pf := makeCmd.PersistentFlags()
	pf.String("root", ".", "the project root: the directory holding go.mod")
	pf.Bool("force", false, "overwrite a file that exists")
	pf.Bool("dry-run", false, "print what would be written and change nothing")
	pf.Bool("no-templ", false, "do not run `go tool templ generate` after writing a .templ file")
	for _, k := range makeKinds {
		makeCmd.AddCommand(k)
	}
}

var makeCmd = &cobra.Command{
	Use:   "make",
	Short: "Generate a page, resource, action, service, store, migration, middleware, command, config or spec",
	Long: `Generate application code that obeys the layering rules, in the layout the project's
govibe.yml declares (the generated app's by default), and register it where it belongs:
a route or a command at the marker comment in the wiring ("// govibe:routes" in
routes/routes.go, "// govibe:commands" in routes/console.go) and a line under the
"Routes", "Commands" or "Tables" heading of the app's docs, so the graph's docs rules
stay green. When a marker or a heading is missing, the snippet is printed instead of
guessed at. A file that exists is never overwritten without --force.

Names may be written as order, rate-limit, rateLimit or RateLimit; a command is noun:verb.`,
	Example: `  go tool govibe make page about                      # assets/templates/about.templ, controllers/about.go, GET /about
  go tool govibe make resource order                  # the slice: migration, storage/orderstore, business/services/orders, controllers/orders.go, GET /api/orders[/{id}]
  go tool govibe make command items:prune --schedule "0 3 * * *" --short "Delete archived items"
  go tool govibe make migration add_status_to_orders
  go tool govibe make config                           # config/config.go bound to the "<app>" section of env.yml
  go tool govibe make spec payments                    # docs/specs/SPEC_PAYMENTS.md with the required sections`,
}

// makeKinds are the subcommands, one per generator.
var makeKinds = func() []*cobra.Command {
	kind := func(name, args, short string, flags func(*cobra.Command), opts func(*cobra.Command, *generate.Options)) *cobra.Command {
		c := &cobra.Command{
			Use:           name + " " + args,
			Short:         short,
			Args:          cobra.ExactArgs(1),
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, a []string) error {
				o := makeOptions{kind: name, name: a[0]}
				f := cmd.Flags()
				o.root, _ = f.GetString("root")
				o.gen.Force, _ = f.GetBool("force")
				o.dryRun, _ = f.GetBool("dry-run")
				o.noTempl, _ = f.GetBool("no-templ")
				if opts != nil {
					opts(cmd, &o.gen)
				}
				return makeExit(runMake(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr()))
			},
		}
		if flags != nil {
			flags(c)
		}
		if args == "" {
			c.Use = name
			c.Args = cobra.NoArgs
			c.RunE = func(cmd *cobra.Command, a []string) error {
				o := makeOptions{kind: name}
				f := cmd.Flags()
				o.root, _ = f.GetString("root")
				o.gen.Force, _ = f.GetBool("force")
				o.dryRun, _ = f.GetBool("dry-run")
				if opts != nil {
					opts(cmd, &o.gen)
				}
				return makeExit(runMake(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr()))
			}
		}
		return c
	}
	return []*cobra.Command{
		kind("page", "<name>", "A templ page, the controller that renders it, and its route",
			func(c *cobra.Command) { c.Flags().String("path", "", "the route (default /<name>)") },
			func(c *cobra.Command, o *generate.Options) { o.Path, _ = c.Flags().GetString("path") }),
		kind("resource", "<noun>", "The vertical slice for a noun: table, store, service, JSON controller, routes and docs", nil, nil),
		kind("action", "<name>", "A business action (single-function logic) and its test", nil, nil),
		kind("service", "<name>", "A business service package and its test", nil, nil),
		kind("store", "<noun>", "A store package over a table (storage/<noun>store) and its test", nil, nil),
		kind("migration", "<name>", "A goose SQL migration, numbered after the last one",
			func(c *cobra.Command) {
				c.Flags().String("table", "", "create this table (id, created_at) and document it")
			},
			func(c *cobra.Command, o *generate.Options) { o.Table, _ = c.Flags().GetString("table") }),
		kind("middleware", "<name>", "An app middleware and its test", nil, nil),
		kind("command", "<noun:verb>", "A CLI command: its body, its declaration and, with --schedule, a test on the fake clock",
			func(c *cobra.Command) {
				c.Flags().String("schedule", "", `run it on this schedule too: "0 3 * * *", "@daily", "@every 10m"`)
				c.Flags().String("short", "", "its one-line description")
			},
			func(c *cobra.Command, o *generate.Options) {
				o.Schedule, _ = c.Flags().GetString("schedule")
				o.Short, _ = c.Flags().GetString("short")
			}),
		kind("config", "", "A typed config package bound to the app's own section of env.yml",
			func(c *cobra.Command) {
				c.Flags().String("section", "", "the env.yml section to bind (default: the app's name)")
			},
			func(c *cobra.Command, o *generate.Options) { o.Section, _ = c.Flags().GetString("section") }),
		kind("spec", "<name>", "A spec in docs/specs with the sections the rules require", nil, nil),
	}
}()

func makeExit(err error) error {
	var ee *exitError
	if err != nil && !errors.As(err, &ee) {
		return &exitError{code: 2, msg: "govibe make: " + err.Error()}
	}
	return err
}

// makeOptions are the flags of one `govibe make <kind>`.
type generateOptions = generate.Options

type makeOptions struct {
	kind, name, root string
	gen              generate.Options
	dryRun, noTempl  bool

	// run runs a step (templ generate) in dir; nil runs it as a process.
	run func(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error
}

func runMake(ctx context.Context, o makeOptions, out, errOut io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p, err := generate.Load(o.root)
	if err != nil {
		return err
	}
	res, err := generate.Make(p, o.kind, o.name, o.gen)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "govibe make %s %s\n", o.kind, o.name)
	for _, f := range res.Files {
		verb := "write"
		if f.Edit {
			verb = "edit "
		}
		if o.dryRun {
			verb = "would " + strings.TrimSpace(verb)
		}
		fmt.Fprintf(out, "  %s %s\n", verb, f.Path)
	}
	if !o.dryRun {
		for _, f := range res.Files {
			fp := filepath.Join(p.Root, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(fp, f.Content, 0o644); err != nil {
				return err
			}
		}
	}
	for _, s := range res.Snippets {
		fmt.Fprintf(out, "\nAdd by hand, %s:\n%s\n", s.Where, indent(s.Text, "  "))
	}
	if len(res.Notes) > 0 {
		fmt.Fprintln(out)
		for _, n := range res.Notes {
			fmt.Fprintf(out, "note: %s\n", n)
		}
	}
	if res.Templ && !o.dryRun {
		if o.noTempl {
			fmt.Fprintln(out, "\nrun `go tool templ generate` before building: a .templ file was written")
		} else {
			run := o.run
			if run == nil {
				run = runStep
			}
			fmt.Fprintln(out, "\n==> go tool templ generate")
			if err := run(ctx, p.Root, out, errOut, "go", "tool", "templ", "generate"); err != nil {
				return fmt.Errorf("go tool templ generate failed: %w (the files are written; run it again by hand)", err)
			}
		}
	}
	if !o.dryRun {
		fmt.Fprintln(out, "\nNext: go build ./... && go test ./... ; then `make check` (every route and command is named in a doc).")
	}
	return nil
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}
