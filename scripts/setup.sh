#!/usr/bin/env bash
# setup.sh — Quick setup for sketchlib-go.
#
# Installs/verifies all prerequisites and prepares the Go module so the
# library, tests, and cross-language tools can be built without further
# manual steps.
#
# Usage:
#   ./scripts/setup.sh           # from sketchlib-go root
#   bash sketchlib-go/scripts/setup.sh   # from repo root
#
# What this script does:
#   1. Checks Go version (>= 1.21 required)
#   2. Checks / installs protoc-gen-go and protoc-gen-go-grpc
#   3. Downloads Go module dependencies (go mod download)
#   4. Regenerates protobuf bindings from proto/sketchlib.proto
#   5. Runs a quick smoke-build of all packages
#   6. Builds the xtest_producer binary

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$GO_DIR/.." && pwd)"
PROTO_SRC="$REPO_ROOT/proto/sketchlib.proto"
PROTO_OUT="$GO_DIR/proto/sketchlibpb"

# ---------------------------------------------------------------------------
# Colour helpers
# ---------------------------------------------------------------------------
BOLD=$'\033[1m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[0;33m'
RED=$'\033[0;31m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'

info()  { echo "${CYAN}[setup-go]${RESET} $*"; }
ok()    { echo "${GREEN}[setup-go]${RESET} $*"; }
warn()  { echo "${YELLOW}[setup-go]${RESET} $*"; }
die()   { echo "${RED}[setup-go] FATAL:${RESET} $*" >&2; exit 1; }
step()  { echo ""; echo "${BOLD}${CYAN}── $* ──${RESET}"; }

# ---------------------------------------------------------------------------
# 1. Go version check
# ---------------------------------------------------------------------------
step "Checking Go toolchain"

if ! command -v go &>/dev/null; then
    die "go not found. Install Go >= 1.21 from https://go.dev/dl/"
fi

GO_VER="$(go version | awk '{print $3}' | sed 's/go//')"
GO_MAJOR="$(echo "$GO_VER" | cut -d. -f1)"
GO_MINOR="$(echo "$GO_VER" | cut -d. -f2)"

info "Found Go $GO_VER"

if [[ "$GO_MAJOR" -lt 1 ]] || { [[ "$GO_MAJOR" -eq 1 ]] && [[ "$GO_MINOR" -lt 21 ]]; }; then
    die "Go >= 1.21 required (found $GO_VER)"
fi
ok "Go version OK"

# ---------------------------------------------------------------------------
# 2. protoc + protoc-gen-go
# ---------------------------------------------------------------------------
step "Checking protobuf tooling"

if ! command -v protoc &>/dev/null; then
    warn "protoc not found — skipping proto regeneration."
    warn "Install protoc: https://grpc.io/docs/protoc-installation/"
    SKIP_PROTO=1
else
    PROTOC_VER="$(protoc --version | awk '{print $2}')"
    info "Found protoc $PROTOC_VER"
    SKIP_PROTO=0
fi

if [[ "${SKIP_PROTO}" == "0" ]]; then
    GOBIN="$(go env GOPATH)/bin"
    export PATH="$PATH:$GOBIN"

    for plugin in protoc-gen-go protoc-gen-go-grpc; do
        if ! command -v "$plugin" &>/dev/null; then
            warn "$plugin not found — installing…"
            case "$plugin" in
                protoc-gen-go)
                    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest ;;
                protoc-gen-go-grpc)
                    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest ;;
            esac
            ok "Installed $plugin"
        else
            info "Found $plugin ($(command -v "$plugin"))"
        fi
    done
fi

# ---------------------------------------------------------------------------
# 3. Download Go module dependencies
# ---------------------------------------------------------------------------
step "Downloading Go module dependencies"
(
    cd "$GO_DIR"
    info "go mod download"
    go mod download
    go mod verify
)
ok "Module cache ready"

# ---------------------------------------------------------------------------
# 4. Regenerate protobuf bindings
# ---------------------------------------------------------------------------
if [[ "${SKIP_PROTO:-0}" == "0" ]]; then
    step "Regenerating protobuf bindings"
    mkdir -p "$PROTO_OUT"
    protoc \
        --proto_path="$REPO_ROOT/proto" \
        --go_out="$GO_DIR/proto/sketchlibpb" \
        --go_opt=paths=source_relative \
        "$PROTO_SRC"
    ok "Generated $PROTO_OUT/sketchlib.pb.go"
else
    step "Skipping proto regeneration (protoc not available)"
    if [[ ! -f "$PROTO_OUT/sketchlib.pb.go" ]]; then
        warn "No generated .pb.go file found. Build may fail."
        warn "Run: protoc --proto_path=\$REPO/proto --go_out=... proto/sketchlib.proto"
    else
        info "Using existing $PROTO_OUT/sketchlib.pb.go"
    fi
fi

# ---------------------------------------------------------------------------
# 5. Smoke-build all packages
# ---------------------------------------------------------------------------
step "Building all packages"
(
    cd "$GO_DIR"
    info "go build ./..."
    go build ./...
)
ok "All packages build successfully"

# ---------------------------------------------------------------------------
# 6. Build xtest_producer binary
# ---------------------------------------------------------------------------
step "Building xtest_producer"
(
    cd "$GO_DIR"
    info "go build ./cmd/xtest_producer"
    go build -o /tmp/xtest_producer_check ./cmd/xtest_producer
    rm -f /tmp/xtest_producer_check
)
ok "xtest_producer builds successfully"

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
echo ""
echo "${BOLD}${GREEN}sketchlib-go setup complete.${RESET}"
echo ""
echo "  Run tests        : cd $GO_DIR && go test ./..."
echo "  Cross-lang test  : $GO_DIR/tests/cross_language/run_test.sh"
echo ""
