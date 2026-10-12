//go:build windows

package main

import (
	"os"
	"os/exec"
)

// There are no process groups to use here: the signal goes to the process the tool started.
func ownProcessGroup(c *exec.Cmd) {}

func signalApp(c *exec.Cmd, s os.Signal) { _ = c.Process.Signal(s) }
