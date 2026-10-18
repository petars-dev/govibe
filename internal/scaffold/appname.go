package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
)

// appNameRe finds the Name field of the App literal in main.go.
var appNameRe = regexp.MustCompile(`(?m)^\s*Name:\s*"([^"]+)",`)

// AppName is the app's name as its main.go declares it (the Name field of its App),
// else the module path's last element.
func AppName(root, module string) string {
	if b, err := os.ReadFile(filepath.Join(root, "main.go")); err == nil {
		if m := appNameRe.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	return NameOf(module)
}
