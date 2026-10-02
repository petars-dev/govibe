package main

import (
    "github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
    Use:   "govibe",
    Short: "GoVibe CLI",
    Long:  "GoVibe command-line tooling for the GoVibe web framework.",
}

func Execute() error { return rootCmd.Execute() }
