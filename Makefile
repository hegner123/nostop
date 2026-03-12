# RLM Makefile

# Variables
BINARY_NAME := rlm
BUILD_DIR := build
CMD_DIR := ./cmd/rlm
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOVET := $(GO) vet
GOFMT := gofmt

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build:
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_NAME) $(CMD_DIR)

# Build with race detector (for development)
.PHONY: build-race
build-race:
	$(GOBUILD) $(LDFLAGS) -race -o $(BINARY_NAME) $(CMD_DIR)

# Build for multiple platforms
.PHONY: build-all
build-all: build-linux build-darwin build-windows

.PHONY: build-linux
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)

.PHONY: build-darwin
build-darwin:
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_DIR)

.PHONY: build-windows
build-windows:
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)

# Install to GOPATH/bin
.PHONY: install
install:
	$(GOBUILD) $(LDFLAGS) -o $(GOPATH)/bin/$(BINARY_NAME) $(CMD_DIR)

# Run tests
.PHONY: test
test:
	$(GOTEST) -v ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run tests with race detector
.PHONY: test-race
test-race:
	$(GOTEST) -v -race ./...

# Run benchmarks
.PHONY: bench
bench:
	$(GOTEST) -bench=. -benchmem ./...

# Lint and format
.PHONY: fmt
fmt:
	$(GOFMT) -s -w .

.PHONY: vet
vet:
	$(GOVET) ./...

.PHONY: lint
lint: fmt vet
	@echo "Lint complete"

# Tidy dependencies
.PHONY: tidy
tidy:
	$(GO) mod tidy

# Download dependencies
.PHONY: deps
deps:
	$(GO) mod download

# Verify dependencies
.PHONY: verify
verify:
	$(GO) mod verify

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Run the application
.PHONY: run
run: build
	./$(BINARY_NAME)

# Run with verbose output
.PHONY: run-verbose
run-verbose: build
	./$(BINARY_NAME) --verbose

# Generate default config
.PHONY: config
config: build
	./$(BINARY_NAME) --write-config

# Show current config
.PHONY: show-config
show-config: build
	./$(BINARY_NAME) --show-config

# Development mode: build and run with race detector
.PHONY: dev
dev: build-race
	./$(BINARY_NAME)

# Quick check: fmt, vet, test
.PHONY: check
check: fmt vet test
	@echo "All checks passed"

# Full CI pipeline
.PHONY: ci
ci: deps lint test-race build
	@echo "CI pipeline complete"

# Print version info
.PHONY: version
version:
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"

# Help
.PHONY: help
help:
	@echo "RLM Makefile targets:"
	@echo ""
	@echo "  build          Build the binary"
	@echo "  build-race     Build with race detector"
	@echo "  build-all      Build for all platforms"
	@echo "  install        Install to GOPATH/bin"
	@echo ""
	@echo "  test           Run tests"
	@echo "  test-coverage  Run tests with coverage report"
	@echo "  test-race      Run tests with race detector"
	@echo "  bench          Run benchmarks"
	@echo ""
	@echo "  fmt            Format code"
	@echo "  vet            Run go vet"
	@echo "  lint           Run fmt and vet"
	@echo ""
	@echo "  tidy           Tidy go.mod"
	@echo "  deps           Download dependencies"
	@echo "  verify         Verify dependencies"
	@echo ""
	@echo "  clean          Remove build artifacts"
	@echo "  run            Build and run"
	@echo "  dev            Build with race detector and run"
	@echo ""
	@echo "  config         Generate default config file"
	@echo "  show-config    Display current configuration"
	@echo ""
	@echo "  check          Quick check (fmt, vet, test)"
	@echo "  ci             Full CI pipeline"
	@echo "  version        Print version info"
	@echo "  help           Show this help"
