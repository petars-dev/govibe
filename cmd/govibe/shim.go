package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/petars-dev/govibe/internal/kernel"
)

// The version shim. A globally installed govibe is needed once, for `govibe new`; inside
// an application every other command should run as the version the application pins in
// its go.mod (`tool github.com/petars-dev/govibe/cmd/govibe`), so that the tool that
// judges a project is the version it builds against. When this binary is another
// version, it hands the command to `go tool govibe` in the same directory and exits with
// its status. GOVIBE_SHIM=off keeps this binary; the child runs with GOVIBE_SHIMMED=1,
// so it never shims again.

// shimDecision is what the shim found.
type shimDecision struct {
	Cmd    *exec.Cmd // the delegation to run; nil to run this binary
	Reason string    // one line for stderr, when Cmd is set
	Pinned string    // the framework version go.mod pins, "" when there is none
}

// noShimCommands are the commands this binary always runs itself: creating an app
// (there is no go.mod yet, or it is another project's) and saying what it is.
var noShimCommands = map[string]bool{"new": true, "version": true, "help": true, "completion": true}

// shim decides whether to delegate args to the pinned tool. cwd is where the user ran
// the command, env the process environment, tool this binary's version.
func shim(args []string, cwd string, env []string, tool string) shimDecision {
	if getenv(env, "GOVIBE_SHIMMED") != "" || isOff(getenv(env, "GOVIBE_SHIM")) {
		return shimDecision{}
	}
	sub := subcommand(args)
	if sub == "" || noShimCommands[sub] {
		return shimDecision{}
	}
	start := cwd
	if r := rootFlag(args); r != "" {
		if !filepath.IsAbs(r) {
			r = filepath.Join(cwd, r)
		}
		start = r
	} else if r := getenv(env, "GOVIBE_ROOT"); r != "" {
		start = r
	}
	root := findGoMod(start)
	if root == "" {
		return shimDecision{}
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return shimDecision{}
	}
	pinned, pinsTool, replaced := pinOf(b, kernel.FrameworkModule())
	d := shimDecision{Pinned: pinned}
	switch {
	case pinned == "", !pinsTool:
		return d // not an application of the framework, or one that does not pin the tool
	case replaced, pinned == "v0.0.0":
		return d // built against a checkout: `go tool govibe` would be this flow again
	case pinned == tool:
		return d
	}
	c := exec.Command("go", append([]string{"tool", "govibe"}, args...)...)
	c.Dir = cwd
	c.Env = append(append([]string(nil), env...), "GOVIBE_SHIMMED=1")
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	d.Cmd = c
	d.Reason = fmt.Sprintf("govibe: go.mod pins %s %s and this tool is %s: running `go tool govibe %s` instead (GOVIBE_SHIM=off keeps this one)",
		kernel.FrameworkModule(), pinned, tool, strings.Join(args, " "))
	return d
}

// pinOf reads what go.mod says about the framework: the version it requires, whether
// it pins the tool, and whether it replaces the framework with a directory.
func pinOf(goMod []byte, framework string) (pinned string, pinsTool, replaced bool) {
	mf, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return "", false, false
	}
	for _, r := range mf.Require {
		if r.Mod.Path == framework {
			pinned = r.Mod.Version
		}
	}
	for _, t := range mf.Tool {
		if t.Path == framework+"/cmd/govibe" {
			pinsTool = true
		}
	}
	for _, r := range mf.Replace {
		if r.Old.Path == framework && r.New.Version == "" {
			replaced = true
		}
	}
	return pinned, pinsTool, replaced
}

// subcommand is the first argument that is not a flag.
func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// rootFlag is the value of --root, when the command line carries one.
func rootFlag(args []string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--root="); ok {
			return v
		}
		if a == "--root" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// findGoMod is the nearest directory at or above start that holds a go.mod.
func findGoMod(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func getenv(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

func isOff(v string) bool {
	switch strings.ToLower(v) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// runShim runs a delegation and returns the exit status to end this process with.
func runShim(d shimDecision) int {
	fmt.Fprintln(os.Stderr, d.Reason)
	err := d.Cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	default:
		fmt.Fprintln(os.Stderr, "govibe: go tool govibe:", err)
		return 2
	}
}
