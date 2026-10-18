package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petars-dev/govibe/internal/scaffold"
)

func init() {
	rootCmd.AddCommand(upgradeCmd)
	f := upgradeCmd.Flags()
	f.String("root", ".", "the project root: the directory holding go.mod")
	f.Bool("force", false, "overwrite a managed file this app edited")
	f.Bool("dry-run", false, "show what would change and write nothing")
	f.Bool("no-notes", false, "do not print the upgrade notes")
	upgradeCmd.SilenceUsage = true
	upgradeCmd.SilenceErrors = true
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Rewrite the managed files for the framework version go.mod pins, and print the upgrade notes",
	Long: `Bring the files the framework generated (the Makefile, .air.toml, the workflow under
.github/workflows and the managed block of SKILL.md) up to the framework version the
app's go.mod requires, after you bumped it:

  go get github.com/petars-dev/govibe@<version> && go mod tidy
  go tool govibe upgrade

Each managed file is rendered for that version and compared with the file on disk and
with the hash govibe.yml recorded when it was written. A file you never touched is
rewritten and its diff shown; a file you edited is refused with its diff, and nothing
is written unless --force. Files that are yours (env.yml, README.md, your docs, every Go
file, the rest of SKILL.md) are never touched. govibe.yml's managed section is updated
with the new version and hashes. The upgrade notes for the versions crossed are printed
from the framework's UPGRADING doc.

Exit status: 0 when the files are up to date, 1 when an edited file was refused, 2 when
the upgrade could not run.`,
	Example: `  go tool govibe upgrade
  go tool govibe upgrade --dry-run
  go tool govibe upgrade --force     # overwrite the Makefile you edited (keep your changes elsewhere)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cmd.Flags()
		o := upgradeOptions{}
		o.root, _ = f.GetString("root")
		o.force, _ = f.GetBool("force")
		o.dryRun, _ = f.GetBool("dry-run")
		o.noNotes, _ = f.GetBool("no-notes")
		err := runUpgrade(o, cmd.OutOrStdout())
		var ee *exitError
		if err != nil && !errors.As(err, &ee) {
			return &exitError{code: 2, msg: "govibe upgrade: " + err.Error()}
		}
		return err
	},
}

type upgradeOptions struct {
	root                   string
	force, dryRun, noNotes bool
}

func runUpgrade(o upgradeOptions, out io.Writer) error {
	u, err := scaffold.PlanUpgrade(o.root, o.force)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "govibe upgrade: %s (framework %s -> %s)\n", u.Root, u.From, u.To)
	changed := 0
	for _, c := range u.Changes {
		note := ""
		switch c.State {
		case "edited":
			note = " (you edited it: refused; --force overwrites it)"
		case "forced":
			note = " (you edited it: overwritten on --force)"
		case "restored":
			note = " (it was missing)"
		case "created":
			note = " (new in this version)"
		}
		fmt.Fprintf(out, "  %-9s %s%s\n", c.State, c.File, note)
		if c.Diff != "" {
			changed++
			fmt.Fprint(out, indent(c.Diff, "    "))
			fmt.Fprintln(out)
		}
	}
	switch {
	case len(u.Refused) > 0:
		fmt.Fprintf(out, "\n%d managed %s edited by this app: nothing was written. Move your changes out of them (the Makefile can include another file) and run again, or --force to overwrite them.\n",
			len(u.Refused), plural(len(u.Refused), "file was", "files were"))
	case o.dryRun:
		fmt.Fprintf(out, "\ndry run: %d %s would be written", changed, plural(changed, "file", "files"))
		if u.ManifestChanges() {
			fmt.Fprint(out, ", and govibe.yml's managed section")
		}
		fmt.Fprintln(out)
	default:
		if err := u.Apply(); err != nil {
			return err
		}
		switch {
		case changed == 0 && !u.ManifestChanges():
			fmt.Fprintln(out, "\nthe managed files are up to date; nothing was written")
		case changed == 0:
			fmt.Fprintln(out, "\nthe managed files are up to date; govibe.yml now records framework "+u.To)
		default:
			fmt.Fprintf(out, "\nwrote %d %s and govibe.yml (managed: %s). Review the diff, then: go build ./... && go tool govibe check\n", changed, plural(changed, "file", "files"), u.To)
		}
	}
	if !o.noNotes {
		notes, err := scaffold.UpgradeNotes(u.From, u.To)
		if err != nil {
			return err
		}
		if len(notes) > 0 {
			fmt.Fprintf(out, "\nUpgrade notes (%s -> %s), from the framework's UPGRADING doc:\n", u.From, u.To)
			for _, n := range notes {
				fmt.Fprintf(out, "\n## %s\n%s\n", n.Version, n.Text)
			}
		}
	}
	if len(u.Refused) > 0 {
		return &exitError{code: 1, msg: "govibe upgrade: " + strings.Join(u.Refused, ", ") + " refused (edited)"}
	}
	return nil
}
