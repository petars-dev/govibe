package main

import (
	"strings"
	"testing"
)

// serve and migrate are the application's own commands: the tool runs `go run .` in
// the project root with the same arguments, wired to its terminal.
func TestAppCommandRunsTheApplicationInTheRoot(t *testing.T) {
	c := appCommand("/work/shop", "migrate", "status")
	if got := strings.Join(c.Args, " "); got != "go run . migrate status" {
		t.Errorf("args = %q", got)
	}
	if c.Dir != "/work/shop" {
		t.Errorf("dir = %q", c.Dir)
	}
	if c.Stdin == nil || c.Stdout == nil || c.Stderr == nil {
		t.Error("the application must be wired to the terminal")
	}
}

func TestServeAndMigrateAreRegistered(t *testing.T) {
	for _, args := range [][]string{{"serve"}, {"migrate", "up"}, {"migrate", "down"}, {"migrate", "status"}} {
		cmd, rest, err := rootCmd.Find(args)
		if err != nil || cmd == rootCmd || len(rest) != 0 {
			t.Errorf("%v: %v %v %v", args, cmd, rest, err)
		}
	}
}
