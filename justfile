# nostop Justfile

binary_name := "nostop"
build_dir := "build"
cmd_dir := "./cmd/nostop"
version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
commit := `git rev-parse --short HEAD 2>/dev/null || echo "unknown"`
build_time := `date -u '+%Y-%m-%d_%H:%M:%S'`
ldflags := "-X main.buildVersion=" + version + " -X main.buildCommit=" + commit + " -X main.buildTime=" + build_time

# Build the binary (default recipe)
build:
    go build -ldflags "{{ldflags}}" -o {{binary_name}} {{cmd_dir}}
    codesign -s - {{binary_name}}

# Build with race detector
build-race:
    go build -ldflags "{{ldflags}}" -race -o {{binary_name}} {{cmd_dir}}
    codesign -s - {{binary_name}}

# Build for all platforms
build-all: build-linux build-darwin build-windows

# Build linux binaries
build-linux:
    mkdir -p {{build_dir}}
    GOOS=linux GOARCH=amd64 go build -ldflags "{{ldflags}}" -o {{build_dir}}/{{binary_name}}-linux-amd64 {{cmd_dir}}
    GOOS=linux GOARCH=arm64 go build -ldflags "{{ldflags}}" -o {{build_dir}}/{{binary_name}}-linux-arm64 {{cmd_dir}}

# Build macOS binaries
build-darwin:
    mkdir -p {{build_dir}}
    GOOS=darwin GOARCH=amd64 go build -ldflags "{{ldflags}}" -o {{build_dir}}/{{binary_name}}-darwin-amd64 {{cmd_dir}}
    codesign -s - {{build_dir}}/{{binary_name}}-darwin-amd64
    GOOS=darwin GOARCH=arm64 go build -ldflags "{{ldflags}}" -o {{build_dir}}/{{binary_name}}-darwin-arm64 {{cmd_dir}}
    codesign -s - {{build_dir}}/{{binary_name}}-darwin-arm64

# Build windows binary
build-windows:
    mkdir -p {{build_dir}}
    GOOS=windows GOARCH=amd64 go build -ldflags "{{ldflags}}" -o {{build_dir}}/{{binary_name}}-windows-amd64.exe {{cmd_dir}}

# Install to /usr/local/bin
install:
    go build -ldflags "{{ldflags}}" -o {{binary_name}} {{cmd_dir}}
    cp {{binary_name}} /usr/local/bin/{{binary_name}}
    codesign -s - /usr/local/bin/{{binary_name}}

# Run tests
test:
    go test -v ./...

# Run tests with coverage report
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Run tests with race detector
test-race:
    go test -v -race ./...

# Run benchmarks
bench:
    go test -bench=. -benchmem ./...

# Format code
fmt:
    gofmt -s -w .

# Run go vet
vet:
    go vet ./...

# Lint: format then vet
lint: fmt vet

# Tidy go.mod
tidy:
    go mod tidy

# Download dependencies
deps:
    go mod download

# Verify dependencies
verify:
    go mod verify

# Clean build artifacts
clean:
    rm -f {{binary_name}}
    rm -rf {{build_dir}}
    rm -f coverage.out coverage.html

# Build and run
run: build
    ./{{binary_name}}

# Build and run with verbose output
run-verbose: build
    ./{{binary_name}} --verbose

# Generate default config file
config: build
    ./{{binary_name}} --write-config

# Show current configuration
show-config: build
    ./{{binary_name}} --show-config

# Development mode: build with race detector and run
dev: build-race
    ./{{binary_name}}

# Quick check: format, vet, test
check: fmt vet test

# Full CI pipeline
ci: deps lint test-race build

# Print version info
version:
    @echo "Version: {{version}}"
    @echo "Commit:  {{commit}}"
    @echo "Built:   {{build_time}}"
