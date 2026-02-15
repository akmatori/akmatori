.PHONY: build run clean test test-all test-adapters test-mcp test-agent test-coverage verify deps help install build-agent

# Binary name
BINARY_NAME=akmatori

# Build directory
BUILD_DIR=./bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

build: ## Build the application
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/akmatori

build-linux: ## Build for Linux
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-linux-amd64 -v ./cmd/akmatori

build-mac: ## Build for macOS
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-darwin-amd64 -v ./cmd/akmatori
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BINARY_NAME)-darwin-arm64 -v ./cmd/akmatori

build-windows: ## Build for Windows
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-windows-amd64.exe -v ./cmd/akmatori

build-all: build-linux build-mac build-windows ## Build for all platforms

run: ## Run the application
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/akmatori && ./$(BINARY_NAME)

test: ## Run all Go tests
	$(GOTEST) -v ./...

test-all: ## Run all tests including MCP gateway and agent-worker
	$(GOTEST) -v ./...
	cd mcp-gateway && $(GOTEST) -v ./...
	cd agent-worker && npm test

test-adapters: ## Run alert adapter tests only (fast)
	$(GOTEST) -v ./internal/alerts/adapters/...

test-mcp: ## Run MCP gateway tests only (fast)
	cd mcp-gateway && $(GOTEST) -v ./internal/tools/...

test-agent: ## Run agent-worker tests only
	cd agent-worker && npm test

build-agent: ## Build agent-worker Docker image
	docker build -t akmatori-agent ./agent-worker

test-coverage: ## Run tests with coverage
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

verify: ## Run pre-commit verification (vet + tests)
	$(GOCMD) vet ./...
	$(GOTEST) ./...
	cd mcp-gateway && $(GOCMD) vet ./...
	cd mcp-gateway && $(GOTEST) ./...
	cd agent-worker && npm test

clean: ## Clean build artifacts
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME)-*
	rm -f coverage.out coverage.html

install: ## Install the binary to GOPATH/bin
	$(GOCMD) install ./cmd/akmatori

fmt: ## Format code
	$(GOCMD) fmt ./...

vet: ## Run go vet
	$(GOCMD) vet ./...

lint: ## Run golangci-lint (requires golangci-lint installed)
	golangci-lint run

docker-build: ## Build Docker image
	docker build -t akmatori:latest .

docker-run: ## Run Docker container
	docker run --env-file .env akmatori:latest

docker-up: ## Start all containers with docker-compose (includes directory init)
	docker-compose up -d

docker-down: ## Stop all containers
	docker-compose down

docker-logs: ## Show logs from all containers
	docker-compose logs -f

docker-restart: ## Restart all containers
	docker-compose restart

docker-clean: ## Stop containers and remove volumes (WARNING: destroys data)
	docker-compose down -v
	rm -rf ./akmatori_data
