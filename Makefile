# SAGE Multi-Agent System Makefile
# Build, test, and manage the multi-agent system

# Version
VERSION := 1.0.0
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Binary names
BINARY_CLI=cli
BINARY_ROOT=root
BINARY_ORDERING=ordering
BINARY_PLANNING=planning
BINARY_REGISTER=register
BINARY_CLIENT=client
BINARY_ENHANCED_CLIENT=enhanced_client

# Directories
BIN_DIR=bin
CLI_DIR=cli
CLIENT_DIR=client
TOOLS_DIR=tools

# Build flags
LDFLAGS=-ldflags "-w -s -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"
BUILD_FLAGS=-trimpath

# Colors for output
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[1;33m
NC=\033[0m # No Color

.PHONY: all build clean test help

# Default target
all: build

# Help target
help:
	@echo "$(GREEN)SAGE Multi-Agent System - Makefile$(NC)"
	@echo ""
	@echo "Available targets:"
	@echo "  $(YELLOW)all$(NC)              - Build all components (default)"
	@echo "  $(YELLOW)build$(NC)            - Build all binaries"
	@echo "  $(YELLOW)build-agents$(NC)     - Build only agent binaries"
	@echo "  $(YELLOW)build-cli$(NC)        - Build CLI client"
	@echo "  $(YELLOW)build-root$(NC)       - Build root agent"
	@echo "  $(YELLOW)build-ordering$(NC)   - Build ordering agent"
	@echo "  $(YELLOW)build-planning$(NC)   - Build planning agent"
	@echo "  $(YELLOW)build-register$(NC)   - Build registration tool"
	@echo "  $(YELLOW)build-client$(NC)     - Build client servers"
	@echo "  $(YELLOW)clean$(NC)            - Remove all binaries and build artifacts"
	@echo "  $(YELLOW)test$(NC)             - Run all tests"
	@echo "  $(YELLOW)test-verbose$(NC)     - Run tests with verbose output"
	@echo "  $(YELLOW)test-coverage$(NC)    - Run tests with coverage report"
	@echo "  $(YELLOW)deps$(NC)             - Download and verify dependencies"
	@echo "  $(YELLOW)tidy$(NC)             - Tidy go.mod and go.sum"
	@echo "  $(YELLOW)run-root$(NC)         - Run root agent"
	@echo "  $(YELLOW)run-ordering$(NC)     - Run ordering agent"
	@echo "  $(YELLOW)run-planning$(NC)     - Run planning agent"
	@echo "  $(YELLOW)run-cli$(NC)          - Run CLI client"
	@echo "  $(YELLOW)install$(NC)          - Install binaries to GOPATH/bin"
	@echo ""

# Build all binaries
build: deps build-dir
	@echo "$(GREEN)Building all components...$(NC)"
	@$(MAKE) build-agents
	@$(MAKE) build-cli
	@$(MAKE) build-register
	@$(MAKE) build-client
	@echo "$(GREEN)✅ All components built successfully!$(NC)"
	@echo "Binaries are available in $(BIN_DIR)/"
	@ls -la $(BIN_DIR)/

# Build only agent binaries
build-agents: build-root build-ordering build-planning
	@echo "$(GREEN)✅ All agents built$(NC)"

# Create bin directory
build-dir:
	@mkdir -p $(BIN_DIR)

# Build individual components
build-cli: build-dir
	@echo "$(YELLOW)Building CLI client...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_CLI) ./$(CLI_DIR)
	@echo "$(GREEN)✅ CLI client built$(NC)"

build-root: build-dir
	@echo "$(YELLOW)Building root agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_ROOT) ./$(CLI_DIR)/root
	@echo "$(GREEN)✅ Root agent built$(NC)"

build-ordering: build-dir
	@echo "$(YELLOW)Building ordering agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_ORDERING) ./$(CLI_DIR)/ordering
	@echo "$(GREEN)✅ Ordering agent built$(NC)"

build-planning: build-dir
	@echo "$(YELLOW)Building planning agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_PLANNING) ./$(CLI_DIR)/planning
	@echo "$(GREEN)✅ Planning agent built$(NC)"

build-register: build-dir
	@echo "$(YELLOW)Building registration tool...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_REGISTER) ./$(CLI_DIR)/register
	@echo "$(GREEN)✅ Registration tool built$(NC)"

build-client: build-dir
	@echo "$(YELLOW)Building client servers...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_CLIENT) ./$(CLIENT_DIR)/main.go
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_ENHANCED_CLIENT) ./$(CLIENT_DIR)/enhanced_main.go
	@echo "$(GREEN)✅ Client servers built$(NC)"

# Clean build artifacts
clean:
	@echo "$(YELLOW)Cleaning build artifacts...$(NC)"
	@$(GOCLEAN)
	@rm -rf $(BIN_DIR)
	@rm -f coverage.out coverage.html
	@find . -maxdepth 1 -type f -perm +111 -name "*" ! -name "*.sh" -delete 2>/dev/null || true
	@echo "$(GREEN)✅ Clean complete$(NC)"

# Run tests
test:
	@echo "$(YELLOW)Running tests...$(NC)"
	@$(GOTEST) -short \
		./adapters/... \
		./config/... \
		./gateway/... \
		./types/... \
		./websocket/... 2>/dev/null || true
	@echo "$(GREEN)✅ Tests complete$(NC)"

# Run tests with verbose output
test-verbose:
	@echo "$(YELLOW)Running tests with verbose output...$(NC)"
	@$(GOTEST) -v \
		./adapters/... \
		./config/... \
		./gateway/... \
		./types/... \
		./websocket/...

# Run tests with coverage
test-coverage:
	@echo "$(YELLOW)Running tests with coverage...$(NC)"
	@$(GOTEST) -cover \
		./adapters/... \
		./config/... \
		./gateway/... \
		./types/... \
		./websocket/...
	@echo ""
	@echo "$(YELLOW)Generating coverage report...$(NC)"
	@$(GOTEST) -coverprofile=coverage.out \
		./adapters/... \
		./config/... \
		./gateway/... \
		./types/... \
		./websocket/...
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html 2>/dev/null || true
	@echo "$(GREEN)✅ Coverage report generated: coverage.html$(NC)"

# Dependency management
deps:
	@echo "$(YELLOW)Downloading dependencies...$(NC)"
	@$(GOGET) -v ./...
	@echo "$(GREEN)✅ Dependencies downloaded$(NC)"

tidy:
	@echo "$(YELLOW)Tidying go.mod...$(NC)"
	@$(GOMOD) tidy
	@echo "$(GREEN)✅ go.mod tidied$(NC)"

# Run targets (for development)
run-root:
	@echo "$(YELLOW)Starting root agent...$(NC)"
	@$(GOCMD) run ./$(CLI_DIR)/root -port 8080

run-ordering:
	@echo "$(YELLOW)Starting ordering agent...$(NC)"
	@$(GOCMD) run ./$(CLI_DIR)/ordering -port 8083

run-planning:
	@echo "$(YELLOW)Starting planning agent...$(NC)"
	@$(GOCMD) run ./$(CLI_DIR)/planning -port 8084

run-cli:
	@echo "$(YELLOW)Starting CLI client...$(NC)"
	@$(GOCMD) run ./$(CLI_DIR)

# Install binaries to GOPATH/bin
install: build
	@echo "$(YELLOW)Installing binaries to GOPATH/bin...$(NC)"
	@cp $(BIN_DIR)/$(BINARY_CLI) $(GOPATH)/bin/sage-cli
	@cp $(BIN_DIR)/$(BINARY_ROOT) $(GOPATH)/bin/sage-root
	@cp $(BIN_DIR)/$(BINARY_ORDERING) $(GOPATH)/bin/sage-ordering
	@cp $(BIN_DIR)/$(BINARY_PLANNING) $(GOPATH)/bin/sage-planning
	@cp $(BIN_DIR)/$(BINARY_REGISTER) $(GOPATH)/bin/sage-register
	@echo "$(GREEN)✅ Installation complete$(NC)"
	@echo ""
	@echo "Installed binaries:"
	@echo "  sage-cli       - CLI client"
	@echo "  sage-root      - Root agent"
	@echo "  sage-ordering  - Ordering agent"
	@echo "  sage-planning  - Planning agent"
	@echo "  sage-register  - Registration tool"

# Development helpers
.PHONY: fmt lint vet

fmt:
	@echo "$(YELLOW)Formatting code...$(NC)"
	@$(GOCMD) fmt ./...
	@echo "$(GREEN)✅ Code formatted$(NC)"

lint:
	@echo "$(YELLOW)Running linter...$(NC)"
	@command -v golangci-lint >/dev/null 2>&1 || { echo "$(RED)golangci-lint not installed$(NC)"; exit 1; }
	@golangci-lint run
	@echo "$(GREEN)✅ Lint complete$(NC)"

vet:
	@echo "$(YELLOW)Running go vet...$(NC)"
	@$(GOCMD) vet ./...
	@echo "$(GREEN)✅ Vet complete$(NC)"

# Quick start targets
.PHONY: quick-start stop-all

quick-start: build
	@echo "$(GREEN)Starting all agents...$(NC)"
	@./scripts/start-backend.sh

stop-all:
	@echo "$(YELLOW)Stopping all agents...$(NC)"
	@./scripts/stop-backend.sh

# Docker targets (if needed in the future)
.PHONY: docker-build docker-run docker-clean

docker-build:
	@echo "$(YELLOW)Building Docker images...$(NC)"
	@echo "$(RED)Docker support not yet implemented$(NC)"

docker-run:
	@echo "$(YELLOW)Running Docker containers...$(NC)"
	@echo "$(RED)Docker support not yet implemented$(NC)"

docker-clean:
	@echo "$(YELLOW)Cleaning Docker artifacts...$(NC)"
	@echo "$(RED)Docker support not yet implemented$(NC)"