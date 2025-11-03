# SAGE Multi-Agent System Makefile

# Version
VERSION := 1.0.0
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go parameters with gvm
GOCMD := source ~/.gvm/scripts/gvm && gvm use go1.25.2 && go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOMOD := $(GOCMD) mod

# Build directory
BUILD_DIR := build/bin

# Agent targets
AGENTS := root payment medical client gateway planning ordering

# Build flags
LDFLAGS := -ldflags "-w -s -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"
BUILD_FLAGS := -trimpath

# Colors
GREEN := \033[0;32m
YELLOW := \033[1;33m
NC := \033[0m

# Default target
.PHONY: all
all: build

# Build all agents
.PHONY: build
build: $(BUILD_DIR)
	@echo "$(GREEN)Building all agents...$(NC)"
	@for agent in $(AGENTS); do \
		echo "$(YELLOW)Building $$agent...$(NC)"; \
		$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$$agent ./cmd/$$agent || exit 1; \
	done
	@echo "$(GREEN)✅ All agents built successfully in $(BUILD_DIR)/$(NC)"
	@ls -lh $(BUILD_DIR)/

# Build individual agents
.PHONY: root
root: $(BUILD_DIR)
	@echo "$(YELLOW)Building root agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/root ./cmd/root
	@echo "$(GREEN)✅ Root agent built$(NC)"

.PHONY: payment
payment: $(BUILD_DIR)
	@echo "$(YELLOW)Building payment agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/payment ./cmd/payment
	@echo "$(GREEN)✅ Payment agent built$(NC)"

.PHONY: medical
medical: $(BUILD_DIR)
	@echo "$(YELLOW)Building medical agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/medical ./cmd/medical
	@echo "$(GREEN)✅ Medical agent built$(NC)"

.PHONY: client
client: $(BUILD_DIR)
	@echo "$(YELLOW)Building client agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/client ./cmd/client
	@echo "$(GREEN)✅ Client agent built$(NC)"

.PHONY: gateway
gateway: $(BUILD_DIR)
	@echo "$(YELLOW)Building gateway...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/gateway ./cmd/gateway
	@echo "$(GREEN)✅ Gateway built$(NC)"

.PHONY: planning
planning: $(BUILD_DIR)
	@echo "$(YELLOW)Building planning agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/planning ./cmd/planning
	@echo "$(GREEN)✅ Planning agent built$(NC)"

.PHONY: ordering
ordering: $(BUILD_DIR)
	@echo "$(YELLOW)Building ordering agent...$(NC)"
	@$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/ordering ./cmd/ordering
	@echo "$(GREEN)✅ Ordering agent built$(NC)"

# Create build directory
$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

# Clean all build artifacts
.PHONY: clean
clean:
	@echo "$(YELLOW)Cleaning build artifacts...$(NC)"
	@rm -rf build/
	@rm -f root payment medical client gateway planning ordering
	@rm -f coverage.out coverage.html
	@$(GOCLEAN)
	@echo "$(GREEN)✅ Clean complete$(NC)"

# Dependency management
.PHONY: deps
deps:
	@echo "$(YELLOW)Downloading dependencies...$(NC)"
	@$(GOMOD) download
	@echo "$(GREEN)✅ Dependencies downloaded$(NC)"

.PHONY: tidy
tidy:
	@echo "$(YELLOW)Tidying go.mod...$(NC)"
	@$(GOMOD) tidy
	@echo "$(GREEN)✅ go.mod tidied$(NC)"

# Run tests
.PHONY: test
test:
	@echo "$(YELLOW)Running tests...$(NC)"
	@$(GOTEST) -short ./...
	@echo "$(GREEN)✅ Tests complete$(NC)"

.PHONY: test-verbose
test-verbose:
	@echo "$(YELLOW)Running tests with verbose output...$(NC)"
	@$(GOTEST) -v ./...

.PHONY: test-coverage
test-coverage:
	@echo "$(YELLOW)Running tests with coverage...$(NC)"
	@$(GOTEST) -coverprofile=coverage.out ./...
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✅ Coverage report: coverage.html$(NC)"

# Development helpers
.PHONY: fmt
fmt:
	@echo "$(YELLOW)Formatting code...$(NC)"
	@$(GOCMD) fmt ./...
	@echo "$(GREEN)✅ Code formatted$(NC)"

.PHONY: vet
vet:
	@echo "$(YELLOW)Running go vet...$(NC)"
	@$(GOCMD) vet ./...
	@echo "$(GREEN)✅ Vet complete$(NC)"

# Show help
.PHONY: help
help:
	@echo "$(GREEN)SAGE Multi-Agent Build System$(NC)"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Build Targets:"
	@echo "  $(YELLOW)all$(NC)         - Build all agents (default)"
	@echo "  $(YELLOW)build$(NC)       - Build all agents"
	@echo "  $(YELLOW)root$(NC)        - Build root agent only"
	@echo "  $(YELLOW)payment$(NC)     - Build payment agent only"
	@echo "  $(YELLOW)medical$(NC)     - Build medical agent only"
	@echo "  $(YELLOW)client$(NC)      - Build client agent only"
	@echo "  $(YELLOW)gateway$(NC)     - Build gateway only"
	@echo "  $(YELLOW)planning$(NC)    - Build planning agent only"
	@echo "  $(YELLOW)ordering$(NC)    - Build ordering agent only"
	@echo ""
	@echo "Maintenance:"
	@echo "  $(YELLOW)clean$(NC)       - Remove all build artifacts"
	@echo "  $(YELLOW)deps$(NC)        - Download dependencies"
	@echo "  $(YELLOW)tidy$(NC)        - Tidy go.mod"
	@echo "  $(YELLOW)test$(NC)        - Run tests"
	@echo "  $(YELLOW)fmt$(NC)         - Format code"
	@echo "  $(YELLOW)vet$(NC)         - Run go vet"
	@echo ""
	@echo "Build output: $(BUILD_DIR)/"
	@echo "Go version: 1.25.2 (via gvm)"
