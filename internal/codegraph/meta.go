package codegraph

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cayleygraph/quad"
)

// metaLayer is the name of the layer that owns `build:latest` and the
// `layer:<name>` run nodes: the volatile facts about the indexer's own runs.
// Tests that compare generated output exclude it.
const metaLayer = "meta"

// buildID is the single node describing the most recent run.
const buildID = quad.IRI("build:latest")

func layerNodeID(name string) quad.IRI { return quad.IRI("layer:" + idKey(name)) }

// gitState is the repository state recorded on the build node. It is the only
// place git state appears; layer output never depends on it.
type gitState struct {
	SHA   string
	Dirty bool
	OK    bool // false when root is not a git work tree or git is not installed
}

// readGit asks git for the short HEAD sha and whether the work tree is dirty.
// It takes no index lock (GIT_OPTIONAL_LOCKS=0) so it cannot collide with the
// developer's own git commands, and gives up after a few seconds.
func readGit(ctx context.Context, root string) gitState {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	run := func(args ...string) (string, bool) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	sha, ok := run("rev-parse", "--short", "HEAD")
	if !ok || sha == "" {
		return gitState{}
	}
	status, ok := run("status", "--porcelain")
	if !ok {
		return gitState{SHA: sha, OK: true}
	}
	return gitState{SHA: sha, Dirty: status != "", OK: true}
}

// metaQuads renders the build node and one run node per layer. layers carries
// each layer's own last run, which may predate the run that rewrites this layer.
func metaQuads(rep *Report, layers []LayerReport, git gitState) []quad.Quad {
	set := newQuadSet(metaLayer)

	set.add(buildID, PredType, KindBuild)
	set.str(buildID, PredName, "latest")
	if git.OK {
		set.str(buildID, PredGitSHA, git.SHA)
		set.flag(buildID, PredDirty, git.Dirty)
	}
	set.add(buildID, PredIndexedAt, quad.Time(rep.Build.IndexedAt))
	set.add(buildID, PredDurationMS, quad.Int(rep.Build.DurationMS))
	set.str(buildID, PredIndexerVersion, rep.Build.IndexerVersion)
	set.num(buildID, PredQuads, rep.Quads)
	set.num(buildID, PredAdded, rep.Added)
	set.num(buildID, PredRemoved, rep.Removed)

	for _, l := range layers {
		id := layerNodeID(l.Name)
		set.add(buildID, PredHasLayer, id)
		set.add(id, PredType, KindLayer)
		set.str(id, PredName, l.Name)
		set.str(id, PredStatus, l.Status)
		set.str(id, PredFingerprint, l.Fingerprint)
		set.add(id, PredDurationMS, quad.Int(l.DurationMS))
		set.add(id, PredIndexedAt, quad.Time(l.IndexedAt))
		if l.Status == statusOK || l.Status == statusPartial {
			set.num(id, PredQuads, l.Quads)
			set.num(id, PredAdded, l.Added)
			set.num(id, PredRemoved, l.Removed)
		}
		if l.Note != "" {
			set.str(id, PredNote, l.Note)
		}
		if l.Error != "" {
			set.str(id, PredError, l.Error)
		}
	}
	return set.quads
}
