// Package graphdb embeds a Cayley graph database in the binary for dev-only
// graph storage and exploration (see /dev/graph). Quads persist to a single
// bolt file under DefaultDir.
package graphdb

import (
    "fmt"
    "time"

    "github.com/cayleygraph/cayley"
    "github.com/cayleygraph/cayley/graph"
    _ "github.com/cayleygraph/cayley/graph/kv/bolt"
    _ "github.com/cayleygraph/cayley/query/gizmo"
    _ "github.com/cayleygraph/quad/json"
    _ "github.com/cayleygraph/quad/nquads"
)

// DefaultDir is where the embedded store persists its data, as
// DefaultDir/indexes.bolt.
const DefaultDir = "docs/graph"

// openTimeout bounds how long Open waits for the bolt file's exclusive lock.
// Cayley's bundled bolt backend opens with no lock-wait timeout of its own,
// so without this a second process pointed at the same dir (e.g. a stray
// duplicate dev-server instance) would hang here forever.
const openTimeout = 5 * time.Second

// Open initializes (on first run) and opens a bolt-backed Cayley quad store at dir.
func Open(dir string) (*cayley.Handle, error) {
    type result struct {
        h   *cayley.Handle
        err error
    }
    ch := make(chan result, 1)
    go func() {
        if err := graph.InitQuadStore("bolt", dir, nil); err != nil && err != graph.ErrDatabaseExists {
            ch <- result{nil, err}
            return
        }
        h, err := cayley.NewGraph("bolt", dir, nil)
        ch <- result{h, err}
    }()
    select {
    case r := <-ch:
        return r.h, r.err
    case <-time.After(openTimeout):
        return nil, fmt.Errorf("graphdb: timed out opening %s after %s (bolt file may be locked by another process)", dir, openTimeout)
    }
}
