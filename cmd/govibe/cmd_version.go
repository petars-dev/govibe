package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/petars-dev/govibe/internal/kernel"
)

func init() { rootCmd.AddCommand(versionCmd) }

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the tool's version, and the version the current project pins when it differs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		tool := toolVersion()
		fmt.Fprintf(cmd.OutOrStdout(), "govibe %s (%s %s/%s)\n", tool, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		cwd, err := os.Getwd()
		if err != nil {
			return
		}
		root := findGoMod(cwd)
		if root == "" {
			return
		}
		b, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			return
		}
		pinned, pinsTool, replaced := pinOf(b, kernel.FrameworkModule())
		switch {
		case pinned == "":
		case replaced:
			fmt.Fprintf(cmd.OutOrStdout(), "the project in %s builds against a checkout of the framework\n", root)
		case pinned != tool:
			fmt.Fprintf(cmd.OutOrStdout(), "the project in %s pins %s %s", root, kernel.FrameworkModule(), pinned)
			if pinsTool {
				fmt.Fprint(cmd.OutOrStdout(), "; its other commands run as `go tool govibe`, that version")
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
	},
}
