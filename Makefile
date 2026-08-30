# Frontend + Linux backend into dist/.
.PHONY: all build frontend backend ini-template help

DIST_BIN := dist/gpx_forecast_linux_amd64
DIST_DS_BIN := dist/forecast-datasource/gpx_forecast_linux_amd64

all: build

help:
	@echo "make build         webpack production build + Linux backend -> dist/"
	@echo "make frontend      webpack only"
	@echo "make backend       Linux amd64 gpx_forecast for the Grafana container"
	@echo "make ini-template  copy conf/forecast.ini.template into dist/"

build:
	$(MAKE) frontend
	$(MAKE) backend
	$(MAKE) ini-template

frontend:
	npm install
	npm run build

ifeq ($(OS),Windows_NT)
backend:
	cmd /C "if not exist dist mkdir dist"
	cmd /C "set GOOS=linux&& set GOARCH=amd64&& go build -o dist/gpx_forecast_linux_amd64 ./pkg"
	cmd /C "if not exist dist\forecast-datasource mkdir dist\forecast-datasource"
	cmd /C "copy /Y dist\gpx_forecast_linux_amd64 dist\forecast-datasource\gpx_forecast_linux_amd64"

ini-template:
	cmd /C "copy /Y conf\forecast.ini.template dist\forecast.ini.template"
else
backend:
	mkdir -p dist/forecast-datasource
	GOOS=linux GOARCH=amd64 go build -o $(DIST_BIN) ./pkg
	cp $(DIST_BIN) $(DIST_DS_BIN)

ini-template:
	cp conf/forecast.ini.template dist/forecast.ini.template
endif
