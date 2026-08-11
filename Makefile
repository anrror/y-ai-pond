.PHONY: build test lint vet ci proto docker-build docker-build-edge docker-up docker-up-prod docker-up-edge clean

# Default target
.DEFAULT_GOAL := build

VERSION ?= 0.1.0

# Build all packages
build:
	go build ./...

# Run all unit tests with race detector (no hardware/DB integration tests)
test:
	go test -race -shuffle=on -count=1 ./...

# Run linter (requires golangci-lint installed)
lint:
	golangci-lint run ./...

# Run go vet
vet:
	go vet ./...

# CI full verification (build + test + lint + vet)
ci: build test lint vet

# Generate Protobuf code (requires buf installed)
proto:
	buf generate

# Build server Docker image (requires BuildKit + y-ai-agent-base at ../)
docker-build:
	docker build --build-context yaiagentbase=../y-ai-agent-base -t y-ai-pond:$(VERSION) .

# Build edge controller Docker image
docker-build-edge:
	docker build -f Dockerfile.edge --build-context yaiagentbase=../y-ai-agent-base -t y-ai-pond-edge:$(VERSION) .

# Start development stack with Docker Compose (includes override)
docker-up:
	docker compose up --build -d

# Start production stack (without dev overrides)
docker-up-prod:
	docker compose -f docker-compose.yml up --build -d

# Start edge deployment
docker-up-edge:
	docker compose -f docker-compose.edge.yml up --build -d

# Clean build artifacts and Docker volumes (use with caution)
clean:
	rm -rf bin/ dist/ coverage.out
	@echo "Run 'docker compose down -v' to remove Docker volumes manually."
