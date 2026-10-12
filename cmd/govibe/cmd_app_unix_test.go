//go:build !windows

package main

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `go run` exits on SIGTERM without telling the program it started, so the tool
// signals the whole process group: the grandchild must die too.
func TestSignalReachesTheWholeProcessGroup(t *testing.T) {
	c := exec.Command("sh", "-c", "sleep 60 & echo $!; wait")
	ownProcessGroup(c)
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("pid %q: %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	signalApp(c, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the shell did not stop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(grandchild, 0) == nil { // signal 0: does the process exist
		if time.Now().After(deadline) {
			t.Fatal("the grandchild survived the signal")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
