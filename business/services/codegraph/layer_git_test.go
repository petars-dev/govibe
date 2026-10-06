package codegraph

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/quad"
)

// testRepo is a throw-away git repository with a fixed identity and fixed dates,
// isolated from the developer's own git configuration (signing, hooks, aliases).
type testRepo struct {
	t    *testing.T
	root string
	n    int
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &testRepo{t: t, root: t.TempDir()}
	r.git(nil, "init", "-q", "-b", "main")
	writeFile(t, r.root, "go.mod", "module example.com/m\n\ngo 1.25.0\n")
	return r
}

func (r *testRepo) git(extraEnv []string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	home := filepath.Join(r.root, ".git", "home") // inside .git: never part of the work tree
	_ = os.MkdirAll(home, 0o755)
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files (path -> content), stages everything and commits as author.
func (r *testRepo) commit(author, subject string, files map[string]string) {
	r.t.Helper()
	for p, c := range files {
		writeFile(r.t, r.root, p, c)
	}
	r.n++
	when := time.Date(2026, 1, r.n, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	env := []string{
		"GIT_AUTHOR_NAME=" + author, "GIT_AUTHOR_EMAIL=" + author + "@example.com", "GIT_AUTHOR_DATE=" + when,
		"GIT_COMMITTER_NAME=" + author, "GIT_COMMITTER_EMAIL=" + author + "@example.com", "GIT_COMMITTER_DATE=" + when,
	}
	r.git(nil, "add", "-A")
	r.git(env, "commit", "-q", "--no-gpg-sign", "-m", subject)
}

func (r *testRepo) head() string { return r.git(nil, "rev-parse", "HEAD") }

// rev is a file whose every revision changes exactly one line: creating it adds
// two lines, editing it adds one and deletes one, so a commit's churn per file is 2.
func rev(n int) string { return fmt.Sprintf("package a\n// rev %d\n", n) }

// A repository with a known history: a.go and b.go change together four times,
// c.go changes alone, a bulk commit touches fifty files, and two people take part.
//
//	1 alice  a b GUIDE Makefile      (Makefile has no node)
//	2 alice  a b
//	3 bob    a b GUIDE
//	4 bob    a b
//	5 alice  c
//	6 alice  c
//	7 alice  a + 50 generated files  (a bulk change: not a coupling)
func historyRepo(t *testing.T) *testRepo {
	t.Helper()
	r := newTestRepo(t)
	r.commit("alice", "Start the module", map[string]string{"a/a.go": rev(1), "a/b.go": rev(1), "docs/public/GUIDE.md": "# Guide\n\nv1\n", "Makefile": "all:\n"})
	r.commit("alice", "Touch a and b", map[string]string{"a/a.go": rev(2), "a/b.go": rev(2)})
	r.commit("bob", "Change a, b and the guide", map[string]string{"a/a.go": rev(3), "a/b.go": rev(3), "docs/public/GUIDE.md": "# Guide\n\nv2\n"})
	r.commit("bob", "Change a and b again", map[string]string{"a/a.go": rev(4), "a/b.go": rev(4)})
	r.commit("alice", "Add c", map[string]string{"a/c.go": rev(5)})
	r.commit("alice", "Edit c", map[string]string{"a/c.go": rev(6)})
	bulk := map[string]string{"a/a.go": rev(7)}
	for i := 0; i < 50; i++ {
		bulk[fmt.Sprintf("a/gen%02d.go", i)] = rev(7)
	}
	r.commit("alice", "Regenerate everything", bulk)
	return r
}

func buildGit(t *testing.T, root string) (*layerOutput, facts) {
	t.Helper()
	out := buildLayer(t, newTestWorld(t, root, Options{}), gitLayer{})
	return out, facts{out.quads}
}

func TestGitLayerHistory(t *testing.T) {
	r := historyRepo(t)
	out, f := buildGit(t, r.root)
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}

	// Per file: the newest commit, when, how many commits, how much churn, how many people.
	head := r.head()
	if got := f.one(t, "file:a/a.go", "last_commit"); got != str(head[:12]) {
		t.Errorf("a.go last_commit %v, want %s", got, head[:12])
	}
	if got := f.one(t, "file:a/a.go", "last_subject"); got != str("Regenerate everything") {
		t.Errorf("a.go last_subject %v", got)
	}
	if got := f.one(t, "file:a/a.go", "last_commit_at"); got != quad.Time(time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("a.go last_commit_at %v", got)
	}
	for _, tc := range []struct {
		id                      string
		commits, churn, authors int
	}{
		{"file:a/a.go", 5, 10, 2},
		{"file:a/b.go", 4, 8, 2},
		{"file:a/c.go", 2, 4, 1},
		{"file:a/gen00.go", 1, 2, 1},
		{"doc:docs/public/GUIDE.md", 2, 5, 2}, // three lines created, one replaced
	} {
		for pred, want := range map[string]int{"commits": tc.commits, "churn": tc.churn, "authors": tc.authors} {
			if got := f.one(t, tc.id, pred); got != quad.Int(want) {
				t.Errorf("%s %s = %v, want %d", tc.id, pred, got, want)
			}
		}
	}
	if f.exists("file:Makefile") || f.exists("file:go.mod") {
		t.Error("a path with no node in the graph is not recorded")
	}

	// a and b changed together in four commits: 4 of a's 5, 4 of b's 4.
	ab := string(annID("file:a/a.go", PredCoChangedWith, "file:a/b.go"))
	ba := string(annID("file:a/b.go", PredCoChangedWith, "file:a/a.go"))
	if !f.has("file:a/a.go", "co_changed_with", iri("file:a/b.go")) || !f.has("file:a/b.go", "co_changed_with", iri("file:a/a.go")) {
		t.Fatalf("a and b should change together in both directions")
	}
	for _, tc := range []struct {
		aid  string
		conf int
	}{{ab, 80}, {ba, 100}} {
		if got := f.one(t, tc.aid, "confidence"); got != quad.Int(tc.conf) {
			t.Errorf("%s confidence %v, want %d", tc.aid, got, tc.conf)
		}
		if got := f.one(t, tc.aid, "together"); got != quad.Int(4) {
			t.Errorf("%s together %v", tc.aid, got)
		}
		if got := f.one(t, tc.aid, "method"); got != str(methodCoChange) {
			t.Errorf("%s method %v", tc.aid, got)
		}
		if got := f.one(t, tc.aid, "predicate"); got != str("co_changed_with") {
			t.Errorf("%s predicate %v", tc.aid, got)
		}
	}
	// Two commits are too few to call a pair a coupling, and a bulk commit
	// (fifty files) is no evidence about any pair in it.
	if f.has("file:a/a.go", "co_changed_with", iri("doc:docs/public/GUIDE.md")) {
		t.Error("a.go and GUIDE.md changed together twice only")
	}
	for _, q := range out.quads {
		if q.Predicate == quad.Value(PredCoChangedWith) && strings.Contains(q.Object.String(), "gen") {
			t.Errorf("a bulk commit made a coupling: %v", q)
		}
	}
	if got := f.out("file:a/c.go", "co_changed_with"); len(got) != 0 {
		t.Errorf("c.go changed alone: %v", got)
	}

	// Who made a commit is counted, never recorded.
	for _, q := range out.quads {
		text := q.Object.String()
		if strings.Contains(text, "alice") || strings.Contains(text, "bob") || strings.Contains(text, "@example.com") {
			t.Errorf("a person's identity reached the graph: %v", q)
		}
	}
}

func TestGitLayerIsDeterministic(t *testing.T) {
	r := historyRepo(t)
	first, _ := buildGit(t, r.root)
	second, _ := buildGit(t, r.root)
	if a, b := nquads(t, first.quads), nquads(t, second.quads); a != b {
		t.Error("two builds of the same history differ")
	}
}

func TestGitLayerOutsideAWorkTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.25.0\n")
	writeFile(t, root, "a/a.go", "package a\n")
	out, err := (gitLayer{}).build(context.Background(), newTestWorld(t, root, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if out.skipped == "" || !out.clear || len(out.quads) != 0 {
		t.Errorf("not a git work tree: %+v", out)
	}
}

func TestGitLayerNoCommitsYet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := newTestRepo(t)
	writeFile(t, r.root, "a/a.go", "package a\n")
	out, err := (gitLayer{}).build(context.Background(), newTestWorld(t, r.root, Options{}))
	if err != nil || out.skipped == "" {
		t.Errorf("a repository with no commit has no history: %+v, %v", out, err)
	}
}

func TestGitLayerShallowClone(t *testing.T) {
	r := historyRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	r.git(nil, "clone", "-q", "--depth", "1", "file://"+r.root, clone)
	out, f := buildGit(t, clone)
	if len(out.warnings) != 1 || !strings.Contains(out.warnings[0], "shallow clone") {
		t.Errorf("a shallow clone must say its history is cut: %v", out.warnings)
	}
	if got := f.one(t, "file:a/a.go", "commits"); got != quad.Int(1) {
		t.Errorf("commits %v: a shallow clone has the newest commit only", got)
	}
}

// The history is read once per HEAD: a change that is not a commit (a doc edit)
// rebuilds the layer, because it resolves against the docs, but not `git log`.
func TestGitLayerCachesTheHistoryByHead(t *testing.T) {
	ctx := context.Background()
	r := historyRepo(t)
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: r.root, Git: true})
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first := ix.gitCache.hist
	if first == nil || ix.gitCache.head == "" {
		t.Fatal("the history was not cached")
	}

	appendTo(t, r.root, "docs/public/GUIDE.md", "\nmore\n")
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil {
		t.Fatalf("refresh: %v %v", rep, err)
	}
	if got := ran(rep); !contains2(got, "git") {
		t.Fatalf("the docs changed, and the git layer resolves against them: ran %v", got)
	}
	if ix.gitCache.hist != first {
		t.Error("the history was read again though HEAD did not move")
	}

	r.commit("alice", "Edit the guide", map[string]string{"docs/public/GUIDE.md": "# Guide\n\nv3\n"})
	if rep, err := ix.Refresh(ctx); err != nil || rep == nil || !contains2(ran(rep), "git") {
		t.Fatalf("a new commit makes the layer stale: %v %v", rep, err)
	}
	if ix.gitCache.hist == first {
		t.Error("HEAD moved but the old history was reused")
	}
	g := facts{storedLabel(t, h, "git")}
	if got := g.one(t, "doc:docs/public/GUIDE.md", "commits"); got != quad.Int(3) {
		t.Errorf("GUIDE.md commits %v after the new commit, want 3", got)
	}
}

func contains2(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestParseGitLog(t *testing.T) {
	const rs, us = gitRS, gitUS
	log := rs + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + us + "1767268800" + us + "x@example.com" + us + "Subject  with\ttabs" + "\n\n" +
		"3\t1\ta/a.go\n" +
		"-\t-\tassets/logo.png\n" +
		"0\t7\tdocs/with space.md\n" +
		"1\t1\t\"quoted\\tpath.go\"\n" +
		rs + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + us + "1767182400" + us + "y@example.com" + us + "" + "\n\n" +
		"2\t0\tb.go\n"
	h := parseGitLog([]byte(log))
	if len(h.commits) != 2 {
		t.Fatalf("%d commits", len(h.commits))
	}
	c := h.commits[0]
	if c.subject != "Subject with tabs" || c.email != "x@example.com" || c.at.Unix() != 1767268800 {
		t.Errorf("header: %+v", c)
	}
	want := []gitChange{{"a/a.go", 3, 1}, {"assets/logo.png", 0, 0}, {"docs/with space.md", 0, 7}}
	if fmt.Sprint(c.files) != fmt.Sprint(want) {
		t.Errorf("files %v, want %v (a path git quotes is left out)", c.files, want)
	}
	if got := h.commits[1]; got.subject != "" || len(got.files) != 1 || got.files[0].added != 2 {
		t.Errorf("second commit: %+v", got)
	}
	if got := parseGitLog(nil); len(got.commits) != 0 {
		t.Errorf("empty output: %+v", got)
	}
}
