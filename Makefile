.PHONY: build run clean test deps help install

# Binary name
BINARY_NAME=aiops-bot

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
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/aiops-bot

build-linux: ## Build for Linux
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-linux-amd64 -v ./cmd/aiops-bot

build-mac: ## Build for macOS
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-darwin-amd64 -v ./cmd/aiops-bot
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o $(BINARY_NAME)-darwin-arm64 -v ./cmd/aiops-bot

build-windows: ## Build for Windows
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o $(BINARY_NAME)-windows-amd64.exe -v ./cmd/aiops-bot

build-all: build-linux build-mac build-windows ## Build for all platforms

run: ## Run the application
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/aiops-bot && ./$(BINARY_NAME)

test: ## Run tests
	$(GOTEST) -v ./...

test-coverage: ## Run tests with coverage
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

clean: ## Clean build artifacts
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME)-*
	rm -f coverage.out coverage.html

install: ## Install the binary to GOPATH/bin
	$(GOCMD) install ./cmd/aiops-bot

fmt: ## Format code
	$(GOCMD) fmt ./...

vet: ## Run go vet
	$(GOCMD) vet ./...

lint: ## Run golangci-lint (requires golangci-lint installed)
	golangci-lint run

docker-build: ## Build Docker image
	docker build -t aiops-bot:latest .

docker-run: ## Run Docker container
	docker run --env-file .env aiops-bot:latest
