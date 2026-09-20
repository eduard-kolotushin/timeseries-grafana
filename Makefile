# Frontend + Linux backend into dist/.
#
# The recipes need a POSIX shell — make picks Git Bash's sh.exe on Windows — so both
# platforms run the same lines. The old `ifeq ($(OS),Windows_NT)` branch used
# `cmd /C "…"`, and that shell rewrites `/C` and `/Y` into paths, so every line of it
# was a silent no-op: `make backend` produced nothing on Windows.
.PHONY: all build frontend backend ini-template help

DIST_BIN := dist/gpx_forecast_linux_amd64
DIST_DS_BIN := dist/forecast-datasource/gpx_forecast_linux_amd64

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
	@echo "make ini-template  copy conf/forecast.ini.template into dist/"

build:
	$(MAKE) frontend
	$(MAKE) backend
	$(MAKE) ini-template

frontend:
	PATH="$(NODE_DIR):$$PATH" "$(NPM)" install
	PATH="$(NODE_DIR):$$PATH" "$(NPM)" run build

backend:
	mkdir -p dist/forecast-datasource
	GOOS=linux GOARCH=amd64 go build -o $(DIST_BIN) ./pkg
	cp $(DIST_BIN) $(DIST_DS_BIN)

ini-template:
	cp conf/forecast.ini.template dist/forecast.ini.template
