package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/petars-dev/govibe/internal/profile"
)

// Managed are the files the framework generates and may rewrite on upgrade. Every
// other file of a generated app is the app's own and is never touched again. SKILL.md is
// managed for its block between the markers alone; the rest of it is the app's.
var Managed = []string{"Makefile", ".air.toml", ".github/workflows/govibe.yml", "SKILL.md"}

// The markers of the managed block of SKILL.md.
const (
	BlockStart = "<!-- govibe:managed:start -->"
	BlockEnd   = "<!-- govibe:managed:end -->"
)

// Hash is the hash recorded for a managed file: of its managed block for SKILL.md, of
// the whole file otherwise.
func Hash(name string, content []byte) (string, error) {
	if name == "SKILL.md" {
		block, err := Block(content)
		if err != nil {
			return "", fmt.Errorf("SKILL.md: %w", err)
		}
		content = []byte(block)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Block is the managed block of a SKILL.md, markers included.
func Block(skill []byte) (string, error) {
	s := string(skill)
	i := strings.Index(s, BlockStart)
	if i < 0 {
		return "", fmt.Errorf("no %s marker", BlockStart)
	}
	j := strings.Index(s[i:], BlockEnd)
	if j < 0 {
		return "", fmt.Errorf("no %s marker after the start", BlockEnd)
	}
	return s[i : i+j+len(BlockEnd)], nil
}

// Manifest renders govibe.yml for a new app: the comments that explain the file, and the
// managed section with the hash of each managed file in tree.
func Manifest(o Options, tree Tree) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`# govibe.yml: the project profile and manifest of this GoVibe application. Every key
# is optional; the defaults are the layout this app was generated with (see "GoVibe:
# Configuration" under /dev/docs in a dev build). Uncomment a key to change it.

# Where things are: the settings files, the migrations, the docs, the graph store.
# config: [env.yml]
# migrations: storage/migrations/sql
# docs:
#   dirs: [docs/public]
#   specs: docs/specs
#   spec_prefix: SPEC_
#   root: [README.md, SKILL.md]
# graph:
#   dir: docs/graph
#   derived: ["docs/graph/*.bolt"]

# Which directory is which architecture layer (controllers, business, storage,
# middleware, commands, wiring, views, dev, support). An entry replaces the default
# for that layer.
# layout:
#   controllers: [controllers]

# A rule of the graph switched off or given another severity: off, error, warning, info.
# rules:
#   docs.route_undocumented: off

# The files the framework generated and rewrites on upgrade, with the hash of what it
# wrote: an upgrade refuses to overwrite one this app has edited. Do not edit by hand.
managed:
`)
	block, err := ManagedBlock(o.FrameworkVersion, tree)
	if err != nil {
		return nil, err
	}
	b.WriteString(strings.TrimPrefix(block, "managed:\n"))
	return []byte(b.String()), nil
}

// Drift is the state of one managed file against the manifest.
type Drift struct {
	File  string
	State string // "ok", "edited", "missing", "unlisted", "unreadable"
	Note  string
}

func (d Drift) String() string {
	if d.Note == "" {
		return d.File + ": " + d.State
	}
	return d.File + ": " + d.State + " (" + d.Note + ")"
}

// CheckManaged compares the managed files under root with the manifest in govibe.yml.
// A project with no manifest (not scaffolded) has nothing to check: nil. Only files
// that are not as the manifest recorded them are returned.
func CheckManaged(root string, m *profile.Managed) []Drift {
	if m == nil {
		return nil
	}
	var out []Drift
	names := make([]string, 0, len(m.Files))
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := m.Files[name]
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out = append(out, Drift{File: name, State: "missing", Note: "the manifest in govibe.yml lists it; govibe upgrade restores it"})
			continue
		case err != nil:
			out = append(out, Drift{File: name, State: "unreadable", Note: err.Error()})
			continue
		}
		got, err := Hash(name, content)
		if err != nil {
			out = append(out, Drift{File: name, State: "edited", Note: err.Error() + "; govibe upgrade will not rewrite it"})
			continue
		}
		if got != want {
			out = append(out, Drift{File: name, State: "edited", Note: "it differs from what the framework wrote; govibe upgrade will not rewrite it without --force"})
		}
	}
	for _, name := range Managed {
		if _, listed := m.Files[name]; !listed {
			out = append(out, Drift{File: name, State: "unlisted", Note: "a managed file the manifest does not record"})
		}
	}
	return out
}
