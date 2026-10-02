SHELL := /bin/bash

APP_NAME := govibe
PORT ?= 8080
APP_ENV ?= local
LOG_LEVEL ?= info

# Tooling paths (can be overridden):
AIR_BIN ?= $(shell go env GOPATH)/bin/air

.PHONY: dev run build tidy templ tailwind docs cli cli-migrate cli-serve cli-generate air air-build

tidy:
	go mod tidy

templ:
	@[ -x "$$(go env GOPATH)/bin/templ" ] || go install github.com/a-h/templ/cmd/templ@latest
	"$$(go env GOPATH)/bin/templ" generate

run: templ
	APP_ENV=$(APP_ENV) LOG_LEVEL=$(LOG_LEVEL) PORT=$(PORT) go run .

build: templ
	GO111MODULE=on go build -trimpath -ldflags='-s -w' -o $(APP_NAME)

dev: templ
	# Example: APP_ENV=dev LOG_LEVEL=debug make run
	APP_ENV=dev LOG_LEVEL=debug PORT=$(PORT) go run .

docs:
	@echo "Open http://localhost:$(PORT)/dev/docs/app and /dev/docs/specs"

tailwind:
	@echo "Ensure .tools/tailwind exists (v4 standalone)."
	@.tools/tailwind -i ./assets/static/input.css -o ./assets/static/tailwind.css --minify || true

cli:
	go build -o govibe ./cmd/govibe

cli-migrate: cli
	./govibe migrate status || true

cli-serve: cli
	APP_ENV=$(APP_ENV) LOG_LEVEL=$(LOG_LEVEL) PORT=$(PORT) ./govibe serve

cli-generate: cli
	./govibe generate

# Air integration (single dev tool):
air:
	@[ -x "$(AIR_BIN)" ] || GOBIN=$$(go env GOPATH)/bin go install github.com/air-verse/air@latest
	APP_ENV=dev LOG_LEVEL=debug PORT=$(PORT) "$(AIR_BIN)"

# Command used by Air to build & run during reloads
air-build:
	@[ -x "$$(go env GOPATH)/bin/templ" ] || go install github.com/a-h/templ/cmd/templ@latest
	"$$(go env GOPATH)/bin/templ" generate
	GO111MODULE=on go build -o tmp/govibe-dev .
