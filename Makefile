BINARY_DIR     := ./bin
API_BINARY     := $(BINARY_DIR)/trustforge-api
WORKER_BINARY  := $(BINARY_DIR)/trustforge-worker
GUEST_BINARY   := $(BINARY_DIR)/guest_agent

GO             := go
GOFLAGS        := -ldflags="-s -w"
GOOS_GUEST     := linux
GOARCH_GUEST   := amd64

.PHONY: all build api guest clean test lint docker

all: build

build: api guest

## api: Build the TrustForge API + worker server
api:
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -o $(API_BINARY) ./cmd/api
	@echo "Built: $(API_BINARY)"

## guest: Cross-compile the guest agent for Alpine Linux in the VM
guest:
	@mkdir -p $(BINARY_DIR)
	GOOS=$(GOOS_GUEST) GOARCH=$(GOARCH_GUEST) CGO_ENABLED=0 \
		$(GO) build $(GOFLAGS) -o $(GUEST_BINARY) ./cmd/guest_agent
	@echo "Built: $(GUEST_BINARY) (linux/amd64)"

## test: Run all tests
test:
	$(GO) test ./... -v -race -timeout 60s

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## docker: Build the Docker image
docker:
	docker build -t trustforge:latest .

## run: Run the API server locally
run: api
	TRUSTFORGE_CONFIG=config.yaml $(API_BINARY)

## clean: Remove build artifacts
clean:
	rm -rf $(BINARY_DIR)

## base-image: Build the Alpine base.ext4 for Firecracker
## This is run once on the host to create the read-only base image.
base-image:
	@echo "Building base Alpine ext4 image..."
	@scripts/build_base_image.sh

help:
	@grep -E '^## ' Makefile | sed 's/## //'
