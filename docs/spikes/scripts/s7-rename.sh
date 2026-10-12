#!/usr/bin/env bash
# S7 / Phase 2 T2.1: rename the Go module path across a project tree.
#
#   s7-rename.sh [ROOT] [OLD] [NEW]
#   defaults: ROOT=. OLD=petars-dev/govibe NEW=github.com/petars-dev/govibe
#
# - Rewrites every text file under ROOT that mentions OLD, except the paths in EXCLUDES
#   (the spec that describes the rename, git internals, derived graph stores).
# - Idempotent: a reference that is already NEW (preceded by "github.com/") is not touched,
#   and OLD followed by a path-name character ("petars-dev/govibe-extra") is not touched.
# - Does not touch nested modules with their own module name (testdata/fixture is
#   example.com/fixture and keeps it).
# - Re-runs gofmt on the changed .go files that were gofmt-clean before (import blocks re-sort when the
#   path gains a host); files that were already unformatted are left as they were.
# - Fails loudly if OLD still appears anywhere outside EXCLUDES.
set -euo pipefail

ROOT=${1:-.}
OLD=${2:-petars-dev/govibe}
NEW=${3:-github.com/petars-dev/govibe}
cd "$ROOT"

# Paths (relative, grep -v patterns) that must keep mentioning OLD.
EXCLUDES='^(\./)?(\.git/|\.idea/|\.vscode/|docs/graph/.*\.bolt$|docs/spikes/|docs/specs/SPEC_FRAMEWORK_EXTRACTION\.md$)'

old_re=$(printf '%s' "$OLD" | sed 's/[.[\*^$/]/\\&/g')
# OLD, not preceded by a path/host character, not followed by a name character.
PERL_RE="(?<![A-Za-z0-9._/-])${old_re}(?![A-Za-z0-9_-])"

# Candidate files: tracked and untracked-but-not-ignored files inside a git repo (ignored files such as
# .idea/ and the graph's bolt store are not ours to rewrite; an untracked file is, or one added since the
# last commit would keep the old path and the check below would never see it), else every text file. Portable (BSD grep has no -P): a fixed-string prefilter, then
# perl decides on the real pattern.
candidates() {
	if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		git ls-files -z -co --exclude-standard | xargs -0 grep -IlF -- "$OLD" 2>/dev/null || true
	else
		grep -rIlF --exclude-dir=.git -- "$OLD" . 2>/dev/null || true
	fi
}
find_refs() {
	candidates |
		xargs perl -lne 'if (m#'"$PERL_RE"'#) { print $ARGV; close ARGV }' 2>/dev/null |
		grep -Ev "$EXCLUDES" || true
}

changed=$(find_refs)
if [ -z "$changed" ]; then
	echo "nothing to rewrite (already renamed?)"
else
	# Only re-gofmt files that were gofmt-clean before: the rename must not add unrelated formatting noise.
	unclean=$(mktemp)
	echo "$changed" | grep '\.go$' | xargs gofmt -l | sort >"$unclean" || true
	clean_go=$(echo "$changed" | grep '\.go$' | sort | comm -23 - "$unclean" || true)
	rm -f "$unclean"
	echo "$changed" | xargs perl -pi -e "s#${PERL_RE}#${NEW}#g"
	[ -z "$clean_go" ] || echo "$clean_go" | xargs gofmt -w
	echo "rewrote $(echo "$changed" | wc -l | tr -d ' ') files"
fi

# Verify: no stale reference left, module line is right, the fixture is untouched.
left=$(find_refs)
if [ -n "$left" ]; then echo "STALE references remain in:"; echo "$left"; exit 1; fi
grep -q "^module ${NEW}\$" go.mod || { echo "go.mod module line is not ${NEW}"; exit 1; }
echo "ok: module $(go list -m)"
