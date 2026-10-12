SHELL := /bin/bash

# The framework repository: the framework module at the root, the reference app in
# examples/hello (its own module; go.work joins them), the tool in cmd/govibe.
# `make dev` runs the reference app with the dev tools compiled in.
APP_DIR := examples/hello
APP_NAME := hello
PORT ?= 8080
APP_ENV ?= local
LOG_LEVEL ?= info
DEV_TAGS := govibe_dev

# Tooling paths (can be overridden):
AIR_BIN ?= $(shell go env GOPATH)/bin/air
TEMPL_BIN ?= $(shell go env GOPATH)/bin/templ

.PHONY: dev run build tidy templ test test-dev check cli cli-generate graph-index graph-check graph-check-app air air-build docs

tidy:
	go mod tidy
	cd $(APP_DIR) && go mod tidy

# Generate the reference app's templ output. Run it inside the app directory: with no
# path templ also rewrites the indexer's fixture under internal/codegraph/testdata.
templ:
	@[ -x "$(TEMPL_BIN)" ] || go install github.com/a-h/templ/cmd/templ@latest
	cd $(APP_DIR) && "$(TEMPL_BIN)" generate

# The reference app with the dev tools (the panel at /dev) compiled in.
dev: templ
	cd $(APP_DIR) && APP_ENV=dev LOG_LEVEL=debug PORT=$(PORT) go run -tags $(DEV_TAGS) .

# The reference app as a production build runs: no dev tags.
run: templ
	cd $(APP_DIR) && APP_ENV=$(APP_ENV) LOG_LEVEL=$(LOG_LEVEL) PORT=$(PORT) go run .

# A single binary of the reference app, built the way a deployment builds it.
build: templ
	GO111MODULE=on go build -trimpath -ldflags='-s -w' -o $(APP_NAME) ./$(APP_DIR)

# The framework's tests, then the reference app's. The second run compiles the dev
# tools in, so the code only a govibe_dev build contains is tested too.
test:
	go test ./... ./$(APP_DIR)/...

test-dev:
	go test -tags $(DEV_TAGS) ./... ./$(APP_DIR)/...

check: test test-dev graph-check graph-check-app

docs:
	@echo "Run 'make dev', then open http://localhost:$(PORT)/dev/docs/app and /dev/docs/specs"

cli:
	go build -o govibe ./cmd/govibe

cli-generate: cli
	cd $(APP_DIR) && ../../govibe generate

# Project graph, offline (see docs/specs/SPEC_GRAPH_INDEXER.md). Both use a scratch
# store, so they work while a dev server holds the real one.
graph-index: cli
	./govibe graph index --dir "$${TMPDIR:-/tmp}/govibe-graph" --calls --git

# The CI gate for the framework: fail on an error finding that docs/graph/baseline.json
# does not accept (it is empty).
graph-check: cli
	./govibe graph index --dir "$${TMPDIR:-/tmp}/govibe-graph" --calls --fail-on error --baseline docs/graph/baseline.json

# The same gate for the reference app, judged as an application.
graph-check-app: cli
	./govibe graph index --root $(APP_DIR) --dir "$${TMPDIR:-/tmp}/govibe-graph-app" --calls --fail-on error --baseline $(APP_DIR)/docs/graph/baseline.json

# Air integration (single dev tool): rebuilds and restarts the reference app when
# anything in the repository changes, the framework included.
air:
	@[ -x "$(AIR_BIN)" ] || GOBIN=$$(go env GOPATH)/bin go install github.com/air-verse/air@latest
	"$(AIR_BIN)"

# Command used by Air to build & run during reloads
air-build:
	@[ -x "$(TEMPL_BIN)" ] || go install github.com/a-h/templ/cmd/templ@latest
	cd $(APP_DIR) && "$(TEMPL_BIN)" generate
	GO111MODULE=on go build -tags $(DEV_TAGS) -o tmp/hello-dev ./$(APP_DIR)
