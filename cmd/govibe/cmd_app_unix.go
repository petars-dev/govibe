//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// ownProcessGroup starts the command in a process group of its own, so the terminal's
// Ctrl-C no longer reaches it directly: the tool forwards it (signalApp), once.
func ownProcessGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// signalApp sends s to the command's whole process group: `go run`, which ignores
// SIGINT and exits on SIGTERM without telling its child, and the application.
func signalApp(c *exec.Cmd, s os.Signal) {
	if sig, ok := s.(syscall.Signal); ok && c.Process != nil {
		if err := syscall.Kill(-c.Process.Pid, sig); err == nil {
			return
		}
	}
	_ = c.Process.Signal(s)
}
