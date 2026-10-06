package codegraph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cayleygraph/quad"
)

const gitLayerName = "git"

// What the git layer reads and how it judges two files to change together. They
// are constants, not settings: the numbers only have to be reasonable, and a
// reader of the graph needs them to mean the same thing everywhere.
const (
	gitMaxCommits        = 1000 // the history window: the newest commits `git log` is asked for
	gitMaxFilesPerCommit = 40   // a commit that touches more is a bulk change (a format, a move), not a coupling
	gitMinTogether       = 3    // commits that must have touched both files
	gitMinCoupling       = 30   // percent of a file's commits the other must be in
	gitMaxCoChanged      = 5    // edges per node
	gitTimeout           = 30 * time.Second
	gitMaxOutput         = 64 << 20
	maxSubjectRunes      = 120

	methodCoChange = "co_change" // annotation method of a co_changed_with edge
)

// gitLayer records what the repository's history says about the files the graph
// knows: when each was last committed and by which commit, how often it changed
// and how much (churn), how many people changed it, and which other files
// usually changed in the same commits. It answers "what else did people touch
// when they changed this", which no reading of the code can.
//
// It attaches attributes to the file, doc and spec nodes other layers own, and
// adds one edge, `co_changed_with`, whose annotation carries how often. Author
// names and addresses are read to count distinct authors and never stored. It is
// opt-in (Options.Git): it runs `git log`.
type gitLayer struct{}

func (gitLayer) name() string   { return gitLayerName }
func (gitLayer) deps() []string { return []string{"code", "templates", "docs"} }
func (gitLayer) optIn() string {
	return "opt-in: it runs `git log`. Set GRAPH_GIT=true, run `govibe graph index --git`, or call graph_reindex with layers [\"git\"]"
}

// fingerprint is the commit HEAD points at: the history only changes with it
// (the work tree is not part of the history).
func (gitLayer) fingerprint(w *world) (string, error) {
	h := w.gitHeadInfo()
	if h.head == "" {
		return "none", nil
	}
	s := h.head
	if h.shallow {
		s += "+shallow"
	}
	return s, nil
}

func (gitLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	head := w.gitHeadInfo()
	if head.head == "" {
		return &layerOutput{skipped: head.why, clear: true}, nil
	}
	hist, err := w.gitHistory()
	if err != nil {
		return nil, err
	}
	nodes, err := w.gitNodes()
	if err != nil {
		return nil, err
	}

	set := newQuadSet(gitLayerName)
	byPath := map[string]*gitStat{}
	together := map[[2]string]int{}
	for _, c := range hist.commits {
		var mapped []string
		for _, f := range c.files {
			if _, ok := nodes[f.path]; !ok {
				continue
			}
			st := byPath[f.path]
			if st == nil {
				st = &gitStat{authors: map[string]bool{}, last: c}
				byPath[f.path] = st
			}
			st.commits++
			st.churn += f.added + f.deleted
			st.authors[c.email] = true
			mapped = append(mapped, f.path)
		}
		if len(c.files) > gitMaxFilesPerCommit {
			continue
		}
		sort.Strings(mapped)
		for i := range mapped {
			for j := i + 1; j < len(mapped); j++ {
				together[[2]string{mapped[i], mapped[j]}]++
			}
		}
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		st, id := byPath[p], nodes[p]
		set.str(id, PredLastCommit, st.last.sha[:min(12, len(st.last.sha))])
		set.add(id, PredLastCommitAt, quad.Time(st.last.at))
		if st.last.subject != "" {
			set.str(id, PredLastSubject, st.last.subject)
		}
		set.num(id, PredCommits, st.commits)
		set.num(id, PredChurn, st.churn)
		set.num(id, PredAuthors, len(st.authors))
	}

	// Who changes with whom, per file: the files it shares most of its commits
	// with, strongest first, and not more than a handful.
	type pair struct {
		to       string
		together int
		pct      int
	}
	partners := map[string][]pair{}
	for k, n := range together {
		if n < gitMinTogether {
			continue
		}
		for _, d := range [][2]string{{k[0], k[1]}, {k[1], k[0]}} {
			pct := 100 * n / byPath[d[0]].commits
			if pct >= gitMinCoupling {
				partners[d[0]] = append(partners[d[0]], pair{to: d[1], together: n, pct: pct})
			}
		}
	}
	for _, p := range paths {
		list := partners[p]
		sort.Slice(list, func(i, j int) bool {
			if list[i].pct != list[j].pct {
				return list[i].pct > list[j].pct
			}
			if list[i].together != list[j].together {
				return list[i].together > list[j].together
			}
			return list[i].to < list[j].to
		})
		if len(list) > gitMaxCoChanged {
			list = list[:gitMaxCoChanged]
		}
		for _, q := range list {
			from, to := nodes[p], nodes[q.to]
			set.add(from, PredCoChangedWith, to)
			aid := annID(from, PredCoChangedWith, to)
			set.add(aid, PredType, KindAnnotation)
			set.add(aid, PredSubject, from)
			set.str(aid, PredPredicate, string(PredCoChangedWith))
			set.add(aid, PredObject, to)
			set.str(aid, PredMethod, methodCoChange)
			set.num(aid, PredConfidence, q.pct)
			set.num(aid, PredTogether, q.together)
		}
	}

	out := &layerOutput{quads: set.quads}
	if head.shallow {
		out.warnings = append(out.warnings, fmt.Sprintf("this is a shallow clone: only the %d commits present were read, so churn and co-change are incomplete (fetch the full history)", len(hist.commits)))
	}
	return out, nil
}

// gitStat is what the window says about one file.
type gitStat struct {
	commits int
	churn   int
	authors map[string]bool
	last    *gitCommit // the newest commit that touched it
}

// gitHeadInfo is where the history stands.
type gitHeadInfo struct {
	head    string // the full hash HEAD points at; "" when there is no history to read
	shallow bool
	why     string // when head is "": why the layer has nothing to say
}

// gitCommit is one commit of the window.
type gitCommit struct {
	sha     string
	at      time.Time
	email   string // counted, never stored
	subject string
	files   []gitChange
}

type gitChange struct {
	path           string
	added, deleted int
}

// gitHistory is the parsed window, newest commit first. It does not depend on
// which files the graph knows, so one read serves every rebuild of the layer
// until HEAD moves.
type gitHistory struct {
	commits []*gitCommit
}

// gitHeadInfo reads HEAD once per run.
func (w *world) gitHeadInfo() gitHeadInfo {
	v, _ := w.gitHead.get(func() (gitHeadInfo, error) {
		out, err := runGit(w.ctx, w.root, "rev-parse", "HEAD", "--is-shallow-repository")
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return gitHeadInfo{why: "git is not installed"}, nil
			}
			return gitHeadInfo{why: "the root is not a git work tree with at least one commit"}, nil
		}
		f := strings.Fields(string(out))
		if len(f) == 0 {
			return gitHeadInfo{why: "the root is not a git work tree with at least one commit"}, nil
		}
		return gitHeadInfo{head: f[0], shallow: len(f) > 1 && f[1] == "true"}, nil
	})
	return v
}

// gitHistory reads the window for the HEAD the world saw. An earlier run's read
// of the same HEAD is reused, except on an explicit reindex.
func (w *world) gitHistory() (*gitHistory, error) {
	return w.gitHist.get(func() (*gitHistory, error) {
		head := w.gitHeadInfo()
		key := head.head
		if head.shallow {
			key += "+shallow"
		}
		if c := w.x.gitCache; !w.force && c.hist != nil && c.head == key {
			return c.hist, nil
		}
		h, err := readGitHistory(w.ctx, w.root)
		if err != nil {
			return nil, err
		}
		w.x.gitCache.head, w.x.gitCache.hist = key, h
		return h, nil
	})
}

// gitNodes maps a repo-relative path to the node the graph has for it: a Go or
// templ file, a doc or a spec. A path with no node is not recorded, so no edge
// points at a node that does not exist.
func (w *world) gitNodes() (map[string]quad.IRI, error) {
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}
	nodes := map[string]quad.IRI{}
	for rel := range ci.files {
		nodes[rel] = fileID(rel)
	}
	ti, err := w.templates()
	if err != nil {
		return nil, fmt.Errorf("templates index: %w", err)
	}
	for _, f := range ti.files {
		nodes[f.rel] = fileID(f.rel)
	}
	di, err := w.docs()
	if err != nil {
		return nil, fmt.Errorf("docs index: %w", err)
	}
	for _, d := range di.docs {
		nodes[d.rel] = d.id
	}
	return nodes, nil
}

// Separators of the log format: the record separator opens a commit, the unit
// separator splits its header. Neither can occur in a hash, a number, an address
// or (in practice) a subject.
const (
	gitRS = "\x1e"
	gitUS = "\x1f"
)

// readGitHistory runs `git log` over the newest gitMaxCommits non-merge commits.
// Renames are not followed (a renamed file starts a new history): following them
// makes the numstat output ambiguous and the answer little better.
func readGitHistory(ctx context.Context, root string) (*gitHistory, error) {
	out, err := runGit(ctx, root,
		"-c", "core.quotepath=off", "-c", "log.showRoot=true",
		"log", "--no-merges", "--no-renames", "--relative", "--numstat", "--no-show-signature", "--no-color", "--no-ext-diff",
		"--max-count="+strconv.Itoa(gitMaxCommits),
		"--format="+gitRS+"%H"+gitUS+"%ct"+gitUS+"%ae"+gitUS+"%s", "HEAD", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseGitLog(out), nil
}

// parseGitLog reads the output of readGitHistory's command line.
func parseGitLog(out []byte) *gitHistory {
	h := &gitHistory{}
	for _, rec := range bytes.Split(out, []byte(gitRS)) {
		if len(bytes.TrimSpace(rec)) == 0 {
			continue
		}
		header, body, _ := strings.Cut(string(rec), "\n")
		f := strings.SplitN(header, gitUS, 4)
		if len(f) < 4 {
			continue
		}
		secs, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		c := &gitCommit{sha: f[0], at: time.Unix(secs, 0).UTC(), email: f[2], subject: truncate(strings.Join(strings.Fields(f[3]), " "), maxSubjectRunes)}
		for _, line := range strings.Split(body, "\n") {
			cols := strings.SplitN(line, "\t", 3)
			if len(cols) != 3 || cols[2] == "" || strings.HasPrefix(cols[2], `"`) {
				continue // not a numstat line, or a path git had to quote
			}
			ch := gitChange{path: cols[2]}
			ch.added, _ = strconv.Atoi(cols[0]) // "-" for a binary file: counted as no lines
			ch.deleted, _ = strconv.Atoi(cols[1])
			c.files = append(c.files, ch)
		}
		h.commits = append(h.commits, c)
	}
	return h
}

// runGit runs git in root without taking the index lock, so it cannot collide
// with the developer's own git commands, and gives up after gitTimeout.
func runGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, err
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return nil, err
	}
	if len(out) > gitMaxOutput {
		return nil, fmt.Errorf("git printed more than %d bytes", gitMaxOutput)
	}
	return out, nil
}
