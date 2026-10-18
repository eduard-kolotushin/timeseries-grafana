# Frontend + Linux backend into dist/.
#
# The recipes are POSIX. make runs them through sh.exe when PATH has one (a Git Bash
# session) and through cmd.exe otherwise (PowerShell or cmd, where Git for Windows only
# adds its `cmd` directory and sh.exe stays invisible): the two do not share a syntax —
# `mkdir -p` is a cmd syntax error and cmd's `set X=Y && …` is a POSIX no-op — so one
# makefile cannot be right in both. Pin Git's shell, and every launcher runs the same
# recipes; `make SHELL=/path/to/sh.exe` overrides it (make quotes the path itself).
ifeq ($(OS),Windows_NT)
GIT_SH := $(if $(wildcard C:/Program\ Files/Git/bin/sh.exe),C:/Program Files/Git/bin/sh.exe,$(wildcard $(subst \,/,$(LOCALAPPDATA))/Programs/Git/bin/sh.exe))
ifneq ($(strip $(GIT_SH)),)
SHELL := $(GIT_SH)
endif
endif

.PHONY: all build frontend backend migrate compute gate-check deps-report ini-template docs help

DIST_BIN := dist/gpx_forecast_linux_amd64
DIST_DS_BIN := dist/forecast-datasource/gpx_forecast_linux_amd64
DIST_MIGRATE_BIN := dist/gpx_forecast_migrate_linux_amd64
DIST_COMPUTE_BIN := dist/gpx_forecast_compute_linux_amd64

# Cold-cache scratch for the company gate: the module cache `make gate-check` fills and the
# inventory `make deps-report` writes. See docs/COMPANY-BUILD.md.
GATE_CACHE ?= .gate-cache

# The module cache is deliberately outside the repo: an in-tree cache is walked by gofmt-style
# recursive tools (which do not honour the go tool's dot-directory rule), while a temp dir keeps
# `gofmt -l .` exactly the check CI runs.
GATE_MODCACHE ?= $(subst \,/,$(if $(TEMP),$(TEMP),/tmp))/forecast-gate-mod-$(notdir $(CURDIR))

# node/npm on PATH are nvm shims and can point at an install that is gone ("Node.js
# v22.x.x is not installed or cannot be found"), so resolve a runtime that starts: the
# PATH one when it works, else the newest install under the nvm root, which is the
# version nvm reports as current. `make NODE=… NPM=…` overrides either. NODE_DIR goes
# first on PATH for the frontend recipes because npm's `node_modules/.bin` shims call
# `node` from PATH: resolving a working npm is not enough on its own.
NODE := $(subst \,/,$(shell node -v >/dev/null 2>&1 && command -v node))
ifeq ($(strip $(NODE)),)
# cd first so the shell prints the path in its own form: a `C:/…` entry in PATH is
# passed through to the native child verbatim, where the shim still wins.
NODE := $(shell cd "$(subst \,/,$(LOCALAPPDATA))/Author Software/nvm/installs" 2>/dev/null && ls -1d "$$PWD"/v*/node.exe | sort -V | tail -1)
endif
NODE_DIR := $(patsubst %/node.exe,%,$(NODE))
NPM := $(if $(findstring /node.exe,$(NODE)),$(patsubst %/node.exe,%/npm.cmd,$(NODE)),npm)

all: build

help:
	@echo "make build         webpack production build + Linux backend -> dist/"
	@echo "make frontend      webpack only, via $(NPM) on $(NODE)"
	@echo "make backend       Linux amd64 gpx_forecast for the Grafana container"
	@echo "make migrate       Linux amd64 migration CLI -> dist/"
	@echo "make compute       Linux amd64 gpx_forecast_compute for the standalone compute service"
	@echo "make ini-template  copy conf/forecast.ini.template into dist/"
	@echo "make docs          copy docs/ into dist/"
	@echo "make gate-check    CI Go steps from a cold module cache behind the configured proxy"
	@echo "make deps-report   per-module version inventory -> $(GATE_CACHE)/deps-report.md"

build:
	$(MAKE) frontend
	$(MAKE) backend
	$(MAKE) migrate
	$(MAKE) compute
	$(MAKE) ini-template
	$(MAKE) docs

frontend:
	PATH="$(NODE_DIR):$$PATH" "$(NPM)" install
	PATH="$(NODE_DIR):$$PATH" "$(NPM)" run build

# The quoted paths are load-bearing: Windows make runs a recipe line through the shell
# only when it contains a shell character, and runs `mkdir -p dist/x` directly instead
# ("process_begin: CreateProcess … failed").
backend:
	mkdir -p "dist/forecast-datasource"
	GOOS=linux GOARCH=amd64 go build -o "$(DIST_BIN)" ./pkg
	cp "$(DIST_BIN)" "$(DIST_DS_BIN)"

# gpx_forecast_migrate is the CI/CD entry point: it applies the embedded migrations
# (pkg/store/migrations) to a database before Grafana starts. It is not required to
# run — the plugin applies the same set at its first store use.
migrate:
	mkdir -p "dist"
	GOOS=linux GOARCH=amd64 go build -o "$(DIST_MIGRATE_BIN)" ./cmd/migrate

# gpx_forecast_compute is the standalone compute service: the same /forecast handler
# with the Grafana SDK's identity replaced by the shared token and X-Forecast-Org.
# CGO off keeps it static, so the sandbox can run it on alpine and the chart on
# distroless.
compute:
	mkdir -p "dist"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$(DIST_COMPUTE_BIN)" ./cmd/compute

ini-template:
	mkdir -p "dist"
	cp "conf/forecast.ini.template" "dist/forecast.ini.template"

# The corporate distribution carries the guide, so `docs/` ships inside dist/. The destination is
# cleared first: `cp -r` copies a directory *into* an existing one, which would nest the guide as
# dist/docs/docs on every rebuild. mkdir keeps the target usable before dist/ exists.
docs:
	mkdir -p "dist"
	rm -rf "dist/docs"
	cp -r "docs" "dist/docs"

# Cold-cache replica of the CI Go steps behind whatever module proxy is configured: a build that
# only works from a warm local module cache fails here instead of in the pipeline. Two CI steps
# are deliberately not mirrored: the Format check (this Windows tree's CRLF-only files would
# always flag; CI checks out LF) and the store-gated tests, which skip unless FORECAST_TEST_PG
# is exported — point it at the sandbox Postgres to run them. `-race` stays in CI (this dev box
# has no gcc). See docs/COMPANY-BUILD.md.
gate-check:
	@go env GOPROXY GOSUMDB GOPRIVATE HTTP_PROXY HTTPS_PROXY NO_PROXY
	rm -rf "$(GATE_MODCACHE)"
	GOMODCACHE="$(GATE_MODCACHE)" go mod download all
	GOMODCACHE="$(GATE_MODCACHE)" go test ./pkg/... ./cmd/...
	GOMODCACHE="$(GATE_MODCACHE)" sh -c 'GOOS=linux GOARCH=amd64 go build -o "$(GATE_CACHE)/gpx_forecast_linux_amd64" ./pkg && GOOS=linux GOARCH=amd64 go build -o "$(GATE_CACHE)/gpx_forecast_migrate_linux_amd64" ./cmd/migrate && GOOS=linux GOARCH=amd64 go build -o "$(GATE_CACHE)/gpx_forecast_compute_linux_amd64" ./cmd/compute'
	GOMODCACHE="$(GATE_MODCACHE)" go vet -tags mage .

# Per-module version inventory for a security review (docs/COMPANY-BUILD.md).
deps-report:
	mkdir -p "$(GATE_CACHE)"
	sh tools/deps-report.sh "$(GATE_CACHE)/deps-report.md"
