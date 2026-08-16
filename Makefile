# Frontend + Linux backend into dist/.
.PHONY: all build frontend backend help

DIST_BIN := dist/gpx_forecast_linux_amd64

all: build

help:
	@echo "make build     webpack production build + Linux backend -> dist/"
	@echo "make frontend  webpack only"
	@echo "make backend   Linux amd64 gpx_forecast for the Grafana container"

build:
	$(MAKE) frontend
	$(MAKE) backend

frontend:
	npm install
	npm run build

ifeq ($(OS),Windows_NT)
backend:
	cmd /C "set GOOS=linux&& set GOARCH=amd64&& go build -o dist/gpx_forecast_linux_amd64 ./pkg"
else
backend:
	GOOS=linux GOARCH=amd64 go build -o $(DIST_BIN) ./pkg
endif
