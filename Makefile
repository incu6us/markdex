ONNX_PATH ?= /opt/homebrew/lib/libonnxruntime.dylib
QDRANT_URL ?= http://localhost:6333
QDRANT_VERSION ?= v1.18.2
ADDR ?= :4334

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
BIN ?= bin/markdex-$(GOOS)-$(GOARCH)

.PHONY: run-qdrant ui-build run build test docker-build docker-up docker-stop docker-down docker-logs

## run-qdrant: start a local Qdrant instance
run-qdrant:
	docker run -d -p 6333:6333 qdrant/qdrant:$(QDRANT_VERSION)

## ui-build: install deps and rebuild the web UI into web/dist
ui-build:
	cd web && npm install && npm run build

## run: rebuild the UI and run the backend (which also serves the UI at $(ADDR))
run: ui-build
	ONNX_PATH=$(ONNX_PATH) go run . -addr $(ADDR) -qdrant $(QDRANT_URL)

## build: build the UI and backend into one OS-specific binary (UI embedded)
build: ui-build
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BIN) .
	@echo "built $(BIN)"

## test: run the Go test suite
test:
	go test ./...

## eval: measure retrieval quality against a golden set (needs a running stack + ingested data)
##   override the golden set: make eval GOLDEN=path.json
GOLDEN ?= cmd/eval/golden/go-style-guide.json
eval:
	go run ./cmd/eval -addr http://localhost$(ADDR) -golden $(GOLDEN)

## docker-build: rebuild the app image (picks up code changes)
docker-build:
	docker compose build app

## docker-up: build and start the full stack (app + qdrant) in the background
docker-up:
	docker compose up --build -d

## docker-stop: stop the stack without removing containers or volumes
docker-stop:
	docker compose stop

## docker-down: stop and remove the stack (use `make docker-down ARGS=-v` to also drop volumes)
docker-down:
	docker compose down $(ARGS)

## docker-logs: follow the app + qdrant logs
docker-logs:
	docker compose logs -f
