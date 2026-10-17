package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/petars-dev/govibe/internal/kernel"
	"github.com/petars-dev/govibe/internal/scaffold"
)

func init() {
	rootCmd.AddCommand(newCmd)
	f := newCmd.Flags()
	f.String("dir", "", "the directory to create (default: the app's name, under the current directory); it must not exist or be empty")
	f.String("db", scaffold.DBNone, "the database variant: none, or mariadb (env.yml declares the DSN and the dialect)")
	f.String("replace", "", "contributors: a checkout of the framework the app's go.mod points at instead of a published version")
	f.Bool("no-git", false, "do not `git init` and make the first commit")
	f.Bool("no-check", false, "do not run `go tool govibe check` at the end")
	newCmd.SilenceUsage = true
	newCmd.SilenceErrors = true
}

var newCmd = &cobra.Command{
	Use:   "new <module>",
	Short: "Create a new application that depends on the framework",
	Long: `Create a new GoVibe application: a Go module of its own that requires the framework at
this tool's version and holds only application code. It starts as the Hello GoVibe demo
(GET /, GET /api/hello, GET /hello), ordinary app code in the usual layers that you keep
as a smoke test or delete when you build your own; SKILL.md says what to delete.

The tree is rendered from templates embedded in this tool, then: go mod tidy, go tool
templ generate, git init and a first commit, and go tool govibe check (the gate, which
finds nothing in a new app). The tools (templ, air, govibe) are pinned in go.mod and run
with "go tool", so the tool that judges the project is the version it builds against.

The app's name is the last element of the module path. Afterwards: cd into it and run
"make dev"; the page is at http://localhost:8080 and the dev panel at /dev.`,
	Example: `  govibe new github.com/acme/shop
  govibe new github.com/acme/shop --db mariadb --dir ~/src/shop

  # working on the framework itself: build against a checkout instead of a release
  govibe new github.com/acme/shop --replace ~/src/govibe`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		o := newOptions{module: args[0]}
		o.dir, _ = f.GetString("dir")
		o.db, _ = f.GetString("db")
		o.replace, _ = f.GetString("replace")
		o.noGit, _ = f.GetBool("no-git")
		o.noCheck, _ = f.GetBool("no-check")
		err := runNew(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		var ee *exitError
		if err != nil && !errors.As(err, &ee) {
			return &exitError{code: 2, msg: "govibe new: " + err.Error()}
		}
		return err
	},
}

// newOptions are the flags of `govibe new`.
type newOptions struct {
	module, dir, db, replace string
	noGit, noCheck           bool

	// version is the framework version the app requires: this tool's own, read from its
	// build information when empty.
	version string
	// run runs one step (a command line) in dir, writing to out and errOut; nil runs it
	// as a process.
	run func(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error
}

// toolVersion is the version of the framework this tool was built from: a release
// (v0.3.1), a commit (a pseudo-version), or "(devel)" for a build of a checkout; a
// modified checkout carries "+dirty".
func toolVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Path != kernel.FrameworkModule() {
		return "(devel)"
	}
	return bi.Main.Version
}

// fetchable reports whether a new app's `go get` could resolve the framework at v: a
// valid version with no build metadata ("+dirty"), and not the placeholder v0.0.0.
func fetchable(v string) bool {
	return semver.IsValid(v) && semver.Build(v) == "" && v != "v0.0.0"
}

func runNew(ctx context.Context, o newOptions, out, errOut io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	version := o.version
	if version == "" {
		version = toolVersion()
	}
	so := scaffold.Options{Module: o.module, DB: o.db, FrameworkModule: kernel.FrameworkModule(), FrameworkVersion: version}
	if o.replace != "" {
		abs, err := filepath.Abs(o.replace)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return fmt.Errorf("--replace %s: not a checkout of the framework (no go.mod there)", abs)
		}
		so.Replace = filepath.ToSlash(abs)
	} else if !fetchable(version) {
		return fmt.Errorf("this tool was built from a checkout of the framework (version %s), so a new app could not fetch the framework at that version.\n"+
			"Pass --replace <the checkout> to build the app against it, or install the tool from a release: go install %s/cmd/govibe@latest", version, so.FrameworkModule)
	}
	if err := so.Complete(); err != nil {
		return err
	}
	dir := o.dir
	if dir == "" {
		dir = so.Name
	}
	if err := emptyDir(dir); err != nil {
		return err
	}
	tree, err := scaffold.Render(so)
	if err != nil {
		return err
	}
	for _, p := range tree.Paths() {
		fp := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(fp, tree[p], 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "govibe new: %s\nwrote %d files to %s (framework %s %s)\n", so.Module, len(tree), dir, so.FrameworkModule, versionNote(so))

	run := o.run
	if run == nil {
		run = runStep
	}
	steps := [][]string{
		{"go", "mod", "tidy"},
		{"go", "tool", "templ", "generate"},
	}
	if !o.noGit {
		steps = append(steps, []string{"git", "init", "-q"}, []string{"git", "add", "-A"}, gitCommit(dir, "govibe new "+so.Module))
	}
	if !o.noCheck {
		steps = append(steps, []string{"go", "tool", "govibe", "check"})
	}
	for _, s := range steps {
		fmt.Fprintf(out, "==> %s\n", strings.Join(displayArgs(s), " "))
		if err := run(ctx, dir, out, errOut, s...); err != nil {
			hint := ""
			switch {
			case s[0] == "go" && s[1] == "mod" && so.Replace == "":
				hint = "\n(the framework could not be fetched at " + so.FrameworkVersion + "? a checkout works with --replace <path>)"
			case s[0] == "git":
				hint = "\n(--no-git skips the repository)"
			case s[0] == "go" && s[1] == "tool" && s[2] == "govibe":
				hint = "\n(the app is written; fix what the check reports, or run it again with `make check`)"
			}
			return fmt.Errorf("%s failed in %s: %w%s", strings.Join(displayArgs(s), " "), dir, err, hint)
		}
	}
	fmt.Fprintf(out, "\nDone. Next:\n  cd %s\n  make dev      # http://localhost:8080, the dev panel at /dev\n  make check    # the gate CI runs\nThe rules and what to delete to start your own app: SKILL.md\n", dir)
	return nil
}

func versionNote(so scaffold.Options) string {
	if so.Replace != "" {
		return "from " + so.Replace
	}
	return so.FrameworkVersion
}

// emptyDir accepts a directory that does not exist or holds nothing.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty (%d entries): refusing to write into it", dir, len(entries))
	}
	return nil
}

// gitCommit is the commit command, with an identity of its own when git has none
// configured (a CI runner), so the first commit does not fail on that.
func gitCommit(dir, message string) []string {
	args := []string{"git"}
	c := exec.Command("git", "config", "user.email")
	c.Dir = dir
	if out, err := c.Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		args = append(args, "-c", "user.name=govibe", "-c", "user.email=govibe@localhost")
	}
	return append(args, "commit", "-q", "-m", message)
}

// displayArgs hides the identity flags of a commit from the step line.
func displayArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "user.") {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// runStep runs one command in dir, wired to the writers. GOWORK=off: the new module is
// built on its own even when it is created inside a workspace.
func runStep(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error {
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GOWORK=off")
	c.Stdout, c.Stderr = out, errOut
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("exit status %d", ee.ExitCode())
		}
		return err
	}
	return nil
}
