SHELL := /bin/bash
MODE ?= mock
CASE ?= healthy
CHAIN ?= a
PORT ?= 8080

.PHONY: build test up down scenario verify
build:
	cd go && go build -o ../bin/plantlab .
test:
	cd go && go test -race ./...
up: build
	@test "$(MODE)" = mock || (echo 'Live AI is not implemented in this increment; no provider calls will be made.'; exit 1)
	./bin/plantlab up -port $(PORT)
down:
	./bin/plantlab down
scenario:
	./bin/plantlab scenario -case $(CASE) -chain $(CHAIN)
verify: test
	promtool check config config/prometheus.yml
	promtool test rules config/rules.test.yml
	amtool check-config config/alertmanager.yml
