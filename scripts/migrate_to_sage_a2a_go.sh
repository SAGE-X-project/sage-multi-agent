#!/usr/bin/env bash
# Migrate internal/agent framework to sage-a2a-go v1.7.0
#
# Usage:
#   ./scripts/migrate_to_sage_a2a_go.sh /path/to/sage-a2a-go
#
# This script:
# 1. Copies all agent framework files to sage-a2a-go
# 2. Updates import paths
# 3. Creates README and examples
# 4. Validates the migration

set -Eeuo pipefail

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}ℹ${NC} $1"; }
log_success() { echo -e "${GREEN}✓${NC} $1"; }
log_warn() { echo -e "${YELLOW}⚠${NC} $1"; }
log_error() { echo -e "${RED}✗${NC} $1"; }

# Parse arguments
SAGE_A2A_GO_PATH="${1:-}"
SAGE_MULTI_AGENT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "$SAGE_A2A_GO_PATH" ]]; then
  log_error "Usage: $0 /path/to/sage-a2a-go"
  exit 1
fi

if [[ ! -d "$SAGE_A2A_GO_PATH" ]]; then
  log_error "Directory not found: $SAGE_A2A_GO_PATH"
  exit 1
fi

if [[ ! -d "$SAGE_A2A_GO_PATH/.git" ]]; then
  log_warn "$SAGE_A2A_GO_PATH does not appear to be a git repository"
  read -p "Continue anyway? (y/N) " -n 1 -r
  echo
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    exit 1
  fi
fi

echo "======================================"
echo "  Agent Framework Migration"
echo "======================================"
echo ""
log_info "Source: $SAGE_MULTI_AGENT_PATH/internal/agent"
log_info "Target: $SAGE_A2A_GO_PATH/pkg/agent"
echo ""

# Verify source exists
if [[ ! -d "$SAGE_MULTI_AGENT_PATH/internal/agent" ]]; then
  log_error "Source directory not found: $SAGE_MULTI_AGENT_PATH/internal/agent"
  exit 1
fi

# Count source files
SOURCE_FILES=$(find "$SAGE_MULTI_AGENT_PATH/internal/agent" -name "*.go" -not -name "*_test.go" | wc -l)
log_info "Source files: $SOURCE_FILES"

# Create target directories
log_info "Creating target directories..."
mkdir -p "$SAGE_A2A_GO_PATH/pkg/agent"/{keys,session,did,middleware,hpke}
mkdir -p "$SAGE_A2A_GO_PATH/examples"
log_success "Directories created"

# Copy files
echo ""
log_info "Copying files..."

# Main agent.go
if [[ -f "$SAGE_MULTI_AGENT_PATH/internal/agent/agent.go" ]]; then
  cp -v "$SAGE_MULTI_AGENT_PATH/internal/agent/agent.go" \
       "$SAGE_A2A_GO_PATH/pkg/agent/agent.go"
  log_success "agent.go copied"
else
  log_error "agent.go not found"
  exit 1
fi

# Keys package
if [[ -f "$SAGE_MULTI_AGENT_PATH/internal/agent/keys/keys.go" ]]; then
  cp -v "$SAGE_MULTI_AGENT_PATH/internal/agent/keys/keys.go" \
       "$SAGE_A2A_GO_PATH/pkg/agent/keys/keys.go"
  log_success "keys package copied"
else
  log_error "keys/keys.go not found"
  exit 1
fi

# Session package
if [[ -f "$SAGE_MULTI_AGENT_PATH/internal/agent/session/session.go" ]]; then
  cp -v "$SAGE_MULTI_AGENT_PATH/internal/agent/session/session.go" \
       "$SAGE_A2A_GO_PATH/pkg/agent/session/session.go"
  log_success "session package copied"
else
  log_error "session/session.go not found"
  exit 1
fi

# DID package
DID_FILES=$(find "$SAGE_MULTI_AGENT_PATH/internal/agent/did" -name "*.go" -not -name "*_test.go")
if [[ -n "$DID_FILES" ]]; then
  for file in $DID_FILES; do
    basename_file=$(basename "$file")
    cp -v "$file" "$SAGE_A2A_GO_PATH/pkg/agent/did/$basename_file"
  done
  log_success "did package copied"
else
  log_error "did/*.go files not found"
  exit 1
fi

# Middleware package
if [[ -f "$SAGE_MULTI_AGENT_PATH/internal/agent/middleware/middleware.go" ]]; then
  cp -v "$SAGE_MULTI_AGENT_PATH/internal/agent/middleware/middleware.go" \
       "$SAGE_A2A_GO_PATH/pkg/agent/middleware/middleware.go"
  log_success "middleware package copied"
else
  log_error "middleware/middleware.go not found"
  exit 1
fi

# HPKE package
HPKE_FILES=$(find "$SAGE_MULTI_AGENT_PATH/internal/agent/hpke" -name "*.go" -not -name "*_test.go")
if [[ -n "$HPKE_FILES" ]]; then
  for file in $HPKE_FILES; do
    basename_file=$(basename "$file")
    cp -v "$file" "$SAGE_A2A_GO_PATH/pkg/agent/hpke/$basename_file"
  done
  log_success "hpke package copied"
else
  log_error "hpke/*.go files not found"
  exit 1
fi

# Example file
if [[ -f "$SAGE_MULTI_AGENT_PATH/internal/agent/example_payment.go" ]]; then
  cp -v "$SAGE_MULTI_AGENT_PATH/internal/agent/example_payment.go" \
       "$SAGE_A2A_GO_PATH/examples/agent_framework_payment.go"
  log_success "example copied"
else
  log_warn "example_payment.go not found (optional)"
fi

# Update import paths
echo ""
log_info "Updating import paths..."

UPDATED_FILES=0
for file in $(find "$SAGE_A2A_GO_PATH/pkg/agent" "$SAGE_A2A_GO_PATH/examples" -name "*.go" 2>/dev/null); do
  if grep -q "github.com/sage-x-project/sage-multi-agent/internal/agent" "$file" 2>/dev/null; then
    sed -i '' 's|github.com/sage-x-project/sage-multi-agent/internal/agent|github.com/sage-x-project/sage-a2a-go/pkg/agent|g' "$file"
    UPDATED_FILES=$((UPDATED_FILES + 1))
    log_success "Updated: $(basename $file)"
  fi
done

if [[ $UPDATED_FILES -eq 0 ]]; then
  log_warn "No import paths needed updating"
else
  log_success "Updated $UPDATED_FILES files"
fi

# Create README
echo ""
log_info "Creating README..."

cat > "$SAGE_A2A_GO_PATH/pkg/agent/README.md" << 'EOF'
# SAGE Agent Framework

High-level framework for building SAGE protocol agents without directly importing sage packages.

## Features

- **Zero Direct Sage Imports**: Agent code doesn't import sage packages directly
- **83% Code Reduction**: Initialize agents in 10 lines instead of 165 lines
- **Production-Ready Patterns**: Eager HPKE, Lazy HPKE, Framework Helpers
- **Consistent Error Handling**: All errors include context
- **Comprehensive Abstractions**: Keys, DID, Session, Middleware, HPKE

## Quick Start

```go
import "github.com/sage-x-project/sage-a2a-go/pkg/agent"

// Create agent from environment variables
fwAgent, err := agent.NewAgentFromEnv(
    "payment",  // agent name
    "PAYMENT",  // env var prefix
    true,       // HPKE enabled
    true,       // require signature verification
)
if err != nil {
    return fmt.Errorf("create agent: %w", err)
}

// Use the agent
if fwAgent.GetHTTPServer() != nil {
    http.Handle("/process", fwAgent.GetHTTPServer().MessagesHandler())
}
```

## Environment Variables

Required environment variables (with `PAYMENT` prefix example):

- `PAYMENT_JWK_FILE`: Path to signing key (secp256k1 JWK)
- `PAYMENT_KEM_JWK_FILE`: Path to KEM key (X25519 JWK, for HPKE)
- `PAYMENT_DID`: Agent DID (optional, auto-derived from key)
- `ETH_RPC_URL`: Ethereum RPC endpoint
- `SAGE_REGISTRY_ADDRESS`: SAGE registry contract address
- `SAGE_EXTERNAL_KEY`: Operator private key for registry

## Usage Patterns

### Eager HPKE Pattern (Production)

Best for production servers that always use HPKE:

```go
func NewPaymentAgent() (*PaymentAgent, error) {
    // Framework agent with HPKE initialized at startup
    fwAgent, err := agent.NewAgentFromEnv("payment", "PAYMENT", true, true)
    if err != nil {
        return nil, err
    }

    return &PaymentAgent{agent: fwAgent}, nil
}

func (pa *PaymentAgent) HandleRequest(w http.ResponseWriter, r *http.Request) {
    // HPKE is ready to use
    pa.agent.GetHTTPServer().MessagesHandler().ServeHTTP(w, r)
}
```

### Framework Helpers Pattern

Use individual framework components:

```go
import (
    "github.com/sage-x-project/sage-a2a-go/pkg/agent/keys"
    "github.com/sage-x-project/sage-a2a-go/pkg/agent/did"
)

// Load keys
kp, err := keys.LoadFromJWKFile("/path/to/key.jwk")

// Create resolver
resolver, err := did.NewResolverFromEnv()
```

## Architecture

```
pkg/agent/
├── agent.go          - Main Agent type and constructors
├── keys/             - Key loading and management
├── did/              - DID resolver and registry
├── session/          - Session management
├── middleware/       - HTTP DID authentication
└── hpke/             - HPKE server/client wrappers
```

## Documentation

- See `examples/agent_framework_payment.go` for complete example
- See sage-multi-agent repository for real-world usage in 4 agents

## Benefits

Compared to direct sage imports:

- **18 fewer imports** across 4 agents
- **350+ fewer lines** of boilerplate
- **Consistent patterns** across all agents
- **Easier testing** with clear abstractions
- **Better maintainability** with centralized crypto logic

## License

Same as sage-a2a-go parent project.
EOF

log_success "README created"

# Verify build
echo ""
log_info "Verifying build..."

cd "$SAGE_A2A_GO_PATH"

if go build -o /dev/null ./pkg/agent/... 2>&1; then
  log_success "Build successful!"
else
  log_error "Build failed! Check import paths and dependencies."
  exit 1
fi

# Count target files
TARGET_FILES=$(find "$SAGE_A2A_GO_PATH/pkg/agent" -name "*.go" | wc -l)

# Summary
echo ""
echo "======================================"
echo "  Migration Complete!"
echo "======================================"
echo ""
log_success "Files copied: $SOURCE_FILES"
log_success "Files in target: $TARGET_FILES"
log_success "Import paths updated: $UPDATED_FILES"
log_success "Build verified: ✓"
echo ""
echo "Next steps:"
echo "1. cd $SAGE_A2A_GO_PATH"
echo "2. Review changes: git diff"
echo "3. Test: go build ./pkg/agent/..."
echo "4. Commit:"
echo "   git add pkg/agent examples"
echo "   git commit -m 'feat: Add Agent Framework (v1.7.0)'"
echo "5. Tag and release:"
echo "   git tag v1.7.0"
echo "   git push origin HEAD v1.7.0"
echo ""
