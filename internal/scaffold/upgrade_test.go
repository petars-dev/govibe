package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/profile"
)

// an app on disk, generated at v0.1.0, with a go.mod that pins the version given.
func upgradable(t *testing.T, pin string) string {
	t.Helper()
	tree := render(t, Options{Module: mod})
	root := t.TempDir()
	write(t, root, tree)
	gomod := strings.Replace(string(tree["go.mod"]), fw+" v0.1.0", fw+" "+pin, 1)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644)
	return root
}

func stateOf(u *Upgrade, file string) string {
	for _, c := range u.Changes {
		if c.File == file {
			return c.State
		}
	}
	return "?"
}

// Nothing to do when go.mod pins the version the files were generated at.
func TestUpgradeIsANoOpAtTheSameVersion(t *testing.T) {
	root := upgradable(t, "v0.1.0")
	u, err := PlanUpgrade(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.From != "v0.1.0" || u.To != "v0.1.0" || len(u.Refused) != 0 {
		t.Fatalf("plan = %+v", u)
	}
	for _, c := range u.Changes {
		if c.State != "unchanged" || c.Diff != "" {
			t.Errorf("%s: %s", c.File, c.State)
		}
	}
	if u.ManifestChanges() {
		t.Error("the manifest would change")
	}
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
}

// After a bump, the manifest records the new version; a managed file the app edited
// is refused with a diff, and rewritten with force; the owned files are untouched.
func TestUpgradeRewritesManagedFilesAndRefusesEditedOnes(t *testing.T) {
	root := upgradable(t, "v0.2.0")
	// The app edited the Makefile and deleted .air.toml; README.md is its own.
	mk, _ := os.ReadFile(filepath.Join(root, "Makefile"))
	os.WriteFile(filepath.Join(root, "Makefile"), append(mk, "\nlint:\n\tgolangci-lint run\n"...), 0o644)
	os.Remove(filepath.Join(root, ".air.toml"))
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# mine\n"), 0o644)
	skill, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	os.WriteFile(filepath.Join(root, "SKILL.md"), append(skill, "\n### Domain\nOrders and invoices.\n"...), 0o644)

	u, err := PlanUpgrade(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.From != "v0.1.0" || u.To != "v0.2.0" {
		t.Errorf("from %s to %s", u.From, u.To)
	}
	if got := strings.Join(u.Refused, ","); got != "Makefile" {
		t.Errorf("refused = %q", got)
	}
	if stateOf(u, "Makefile") != "edited" || stateOf(u, ".air.toml") != "restored" || stateOf(u, "SKILL.md") != "unchanged" || stateOf(u, ".github/workflows/govibe.yml") != "unchanged" {
		t.Errorf("states: %+v", u.Changes)
	}
	for _, c := range u.Changes {
		if c.File == "Makefile" && (!strings.Contains(c.Diff, "-lint:") || !strings.Contains(c.Diff, "--- Makefile (yours)")) {
			t.Errorf("Makefile diff:\n%s", c.Diff)
		}
	}
	if err := u.Apply(); err == nil || !strings.Contains(err.Error(), "Makefile") {
		t.Errorf("Apply with a refused file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".air.toml")); err == nil {
		t.Error("a refused plan wrote .air.toml")
	}

	u, err = PlanUpgrade(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Refused) != 0 || stateOf(u, "Makefile") != "forced" {
		t.Fatalf("forced plan: %+v", u.Changes)
	}
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "Makefile"))
	if strings.Contains(string(got), "lint:") {
		t.Error("--force kept the edit")
	}
	if _, err := os.Stat(filepath.Join(root, ".air.toml")); err != nil {
		t.Error(".air.toml was not restored")
	}
	readme, _ := os.ReadFile(filepath.Join(root, "README.md"))
	if string(readme) != "# mine\n" {
		t.Error("README.md, an owned file, was touched")
	}
	skill, _ = os.ReadFile(filepath.Join(root, "SKILL.md"))
	if !strings.Contains(string(skill), "### Domain\nOrders and invoices.") {
		t.Error("the owned section of SKILL.md was lost")
	}
	p, err := profile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Managed.Version != "v0.2.0" {
		t.Errorf("manifest version = %s", p.Managed.Version)
	}
	if d := CheckManaged(root, p.Managed); len(d) != 0 {
		t.Errorf("after the upgrade the files drift: %v", d)
	}
	// Idempotent.
	u, _ = PlanUpgrade(root, false)
	for _, c := range u.Changes {
		if c.State != "unchanged" {
			t.Errorf("second run: %s is %s", c.File, c.State)
		}
	}
}

// The managed block of SKILL.md is replaced in place; a SKILL.md without the markers is
// an edited file, and --force puts the block back at the top.
func TestUpgradeReplacesTheSkillBlockOnly(t *testing.T) {
	root := upgradable(t, "v0.2.0")
	p, _ := profile.Load(root)
	// Pretend the framework's block changed: record another hash for it, so the file reads
	// as "not edited" while the rendered block differs from it.
	skill, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	stale := strings.Replace(string(skill), "## Stack", "## Stack (old)", 1)
	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(stale), 0o644)
	h, _ := Hash("SKILL.md", []byte(stale))
	yml, _ := os.ReadFile(filepath.Join(root, "govibe.yml"))
	os.WriteFile(filepath.Join(root, "govibe.yml"), []byte(strings.Replace(string(yml), p.Managed.Files["SKILL.md"], h, 1)), 0o644)

	u, err := PlanUpgrade(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(u, "SKILL.md") != "updated" {
		t.Fatalf("SKILL.md is %s", stateOf(u, "SKILL.md"))
	}
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if strings.Contains(string(got), "(old)") || !strings.Contains(string(got), "## This application") {
		t.Errorf("SKILL.md after the upgrade:\n%s", got)
	}

	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# mine\n\nNo markers.\n"), 0o644)
	u, _ = PlanUpgrade(root, false)
	if stateOf(u, "SKILL.md") != "edited" {
		t.Errorf("without markers: %s", stateOf(u, "SKILL.md"))
	}
	u, _ = PlanUpgrade(root, true)
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "SKILL.md"))
	if !strings.HasPrefix(string(got), BlockStart) || !strings.HasSuffix(string(got), "No markers.\n") {
		t.Errorf("forced SKILL.md:\n%s", got)
	}
}

func TestUpgradeNeedsAManifestAndAPin(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.25.0\n\nrequire "+fw+" v0.1.0\n"), 0o644)
	if _, err := PlanUpgrade(root, false); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Errorf("no manifest: %v", err)
	}
	root = upgradable(t, "v0.1.0")
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.25.0\n"), 0o644)
	if _, err := PlanUpgrade(root, false); err == nil || !strings.Contains(err.Error(), "does not require") {
		t.Errorf("no pin: %v", err)
	}
	// A replace pins the placeholder version.
	root = upgradable(t, "v0.1.0")
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.25.0\n\nrequire "+fw+" v0.1.0\n\nreplace "+fw+" => /work/govibe\n"), 0o644)
	u, err := PlanUpgrade(root, false)
	if err != nil || u.To != "v0.0.0" {
		t.Errorf("replace: %v %+v", err, u)
	}
}

// The managed section is swapped in place: the app's own keys and comments above it stay.
func TestReplaceManagedBlockKeepsTheRest(t *testing.T) {
	yml := "# mine\nlayout:\n  controllers: [http]\n\nmanaged:\n  version: v0.1.0\n  files:\n    Makefile: sha256:aa\n"
	got := string(replaceManagedBlock([]byte(yml), "managed:\n  version: v0.2.0\n  files:\n    Makefile: sha256:bb\n"))
	if got != "# mine\nlayout:\n  controllers: [http]\n\nmanaged:\n  version: v0.2.0\n  files:\n    Makefile: sha256:bb\n" {
		t.Errorf("got:\n%s", got)
	}
	// A key after the managed section survives too.
	got = string(replaceManagedBlock([]byte(yml+"\nrules:\n  config.unused: off\n"), "managed:\n  version: v0.2.0\n"))
	if !strings.HasSuffix(got, "managed:\n  version: v0.2.0\n\nrules:\n  config.unused: off\n") {
		t.Errorf("got:\n%s", got)
	}
	if got := string(replaceManagedBlock([]byte("# only comments\n"), "managed:\n  version: v1\n")); got != "# only comments\n\nmanaged:\n  version: v1\n" {
		t.Errorf("appended:\n%s", got)
	}
}

func TestDiff(t *testing.T) {
	if d := Diff("f", []byte("a\nb\n"), []byte("a\nb\n")); d != "" {
		t.Errorf("equal files differ:\n%s", d)
	}
	d := Diff("f", []byte("a\nb\nc\nd\ne\nf\ng\nh\n"), []byte("a\nb\nc\nd\nE\nf\ng\nh\ni\n"))
	want := "--- f (yours)\n+++ f (framework)\n@@ -2,7 +2,8 @@\n b\n c\n d\n-e\n+E\n f\n g\n h\n+i\n"
	if d != want {
		t.Errorf("diff:\n%s\nwant:\n%s", d, want)
	}
}

// The notes between two versions come from the embedded UPGRADING doc.
func TestUpgradeNotes(t *testing.T) {
	notes, err := UpgradeNotes("v0.1.0", "v0.0.0-20261017120000-abcdef123456")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		found = found || n.Version == "Unreleased"
	}
	if !found {
		t.Errorf("a pseudo-version gets no unreleased notes: %+v", notes)
	}
	notes, _ = UpgradeNotes("v0.1.0", "v0.1.0")
	if len(notes) != 0 {
		t.Errorf("the same release has notes: %+v", notes)
	}
}
