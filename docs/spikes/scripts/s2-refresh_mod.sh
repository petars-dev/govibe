#!/bin/zsh
# Re-create the renamed framework copy, the file:// proxy and the module-cache app, all under $SPIKES.
set -e
cd $SPIKES
chmod -R u+w modcache 2>/dev/null || true
rm -rf fw-mod proxy modcache/github.com/petars-dev modcache/cache/download/github.com/petars-dev
rsync -a --exclude=docs/graph --exclude=cmd/spikeindex fw/ fw-mod/
(cd fw-mod && grep -rl 'petars-dev/govibe' . --include='*.go' --include='go.mod' | xargs perl -pi -e 's#(?<![/\w.])petars-dev/govibe#github.com/petars-dev/govibe#g')
(cd zipper && GOFLAGS=-mod=mod go run . $SPIKES/fw-mod $SPIKES/proxy github.com/petars-dev/govibe v0.1.0)
rsync -a --exclude=go.mod --exclude=go.sum app/ app-mod/
(cd app-mod && grep -rl 'petars-dev/govibe' . --include='*.go' | xargs perl -pi -e 's#(?<![/\w.])petars-dev/govibe#github.com/petars-dev/govibe#g')
cd app-mod
rm -f go.sum
printf 'module example.com/shop\n\ngo 1.25.0\n\nrequire github.com/petars-dev/govibe v0.1.0\n' > go.mod
export GOMODCACHE=$SPIKES/modcache GOPROXY="file://$SPIKES/proxy,https://proxy.golang.org" GONOSUMDB=github.com/petars-dev/govibe GOFLAGS="-mod=mod -modcacherw"
go mod tidy
go build -o $SPIKES/shopmod-plain .
go build -trimpath -o $SPIKES/shopmod-trim .
echo REFRESH_OK
