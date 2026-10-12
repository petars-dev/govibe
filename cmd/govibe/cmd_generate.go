package main

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"os/exec"
)

func init() { rootCmd.AddCommand(generateCmd) }

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Run templ generate (codegen)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Delegate to installed templ tool to avoid bundling.
		c := exec.Command("templ", "generate")
		c.Stdout = cmd.OutOrStdout()
		c.Stderr = cmd.ErrOrStderr()
		logrus.Info("running: templ generate")
		return c.Run()
	},
}
