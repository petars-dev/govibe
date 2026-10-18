package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
)

func main() {
	// Inside an application that pins another version of the tool, that version runs.
	if cwd, err := os.Getwd(); err == nil {
		if d := shim(os.Args[1:], cwd, os.Environ(), toolVersion()); d.Cmd != nil {
			os.Exit(runShim(d))
		}
	}
	if err := Execute(); err != nil {
		// A command that has already said what it has to say ends with its own status.
		var ee *exitError
		if errors.As(err, &ee) {
			if ee.msg != "" {
				fmt.Fprintln(os.Stderr, ee.msg)
			}
			os.Exit(ee.code)
		}
		logrus.Fatal(err)
	}
}
