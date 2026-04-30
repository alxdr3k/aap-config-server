.PHONY: build build-server build-agent test test-race test-integration test-e2e lint coverage docker-build docker-build-agent clean

DOCKER_IMAGE       ?= aap/config-server
DOCKER_AGENT_IMAGE ?= aap/config-agent
DOCKER_TAG         ?= latest

build: build-server build-agent

build-server:
	go build -o bin/config-server ./cmd/config-server

build-agent:
	go build -o bin/config-agent ./cmd/config-agent

test:
	go test ./... -timeout 60s

test-race:
	go test ./... -race -timeout 60s

test-integration:
	go test -tags=integration ./... -timeout 120s

test-e2e:
	go test -tags=e2e ./... -timeout 300s

lint:
	golangci-lint run ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

docker-build:
	docker build --target config-server -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-build-agent:
	docker build --target config-agent -t $(DOCKER_AGENT_IMAGE):$(DOCKER_TAG) .

clean:
	rm -rf bin/ coverage.out coverage.html
